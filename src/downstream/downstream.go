package main

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/IBM/sarama"
	"github.com/google/uuid"
	"sitia.nu/airgap/src/kafka"
	"sitia.nu/airgap/src/logging"
	"sitia.nu/airgap/src/mtu"
	"sitia.nu/airgap/src/protocol"
	"sitia.nu/airgap/src/udp"
	"sitia.nu/airgap/src/version"
)

// A private key, the filename and the hash of the file
// If a file is removed in the OS, it will be removed from
// the list of keys. If it is changed, the new key will be
// loaded and if it's added in the OS, it will be added to
// the list of keys.
type KeyInfo struct {
	privateKey         *rsa.PrivateKey
	privateKeyFilename string
	privateKeyHash     string
}

// TransferConfiguration struct definition
type TransferConfiguration struct {
	id                string
	nic               string
	targetIP          string
	targetPort        int
	bootstrapServers  string
	topic             string
	mtu               uint16
	key               []byte    // symmetric
	keyInfos          []KeyInfo // array of private keys
	privateKeyGlob    string
	target            string
	logLevel          string
	logFileName       string
	certFile          string
	keyFile           string
	caFile            string
	producer          sarama.AsyncProducer
	clientID          string
	topicTranslations string
	translations      map[string]string // map from input topic to output topic
	logStatistics     int32             //  How often to log statistics in seconds. 0 means no logging
}

// Log counters
var receivedEvents int64
var sentEvents int64
var totalReceived int64
var totalSent int64
var timeStart int64

var Logger = logging.Logger
var config TransferConfiguration

// BuildNumber is set at build time via -ldflags
var BuildNumber = "dev"

// Remove, update or read keys from the OS from the privateKeyGlob
// We want the keys to be in memory so we can decrypt messages. The keys
// in memory should be the same as the keys on disk. If a key is removed
// from disk, it should be removed from memory. If a key is added to disk,
// it should be added to memory. If a key is changed on disk, the new key
// should be loaded into memory.
func readPrivateKeys(fileGlob string) []KeyInfo {
	// Read the files
	var fileNames []string
	var err error
	fileNames, err = filepath.Glob(fileGlob)

	if err != nil {
		// Send message to Kafka
		message := fmt.Sprintf("Can't read private key files from file glob %s, %v", fileGlob, err)
		sendMessage(protocol.TYPE_ERROR, "", config.topic, "", []byte(message))
	}

	// Purge from memory if not on disk
	// First, iterate over the array and save an array of indicies that should be removed
	purgeIndices := []int{}
	for j := 0; j < len(config.keyInfos); j++ {
		found := false
		for _, fileName := range fileNames {
			if config.keyInfos[j].privateKeyFilename == fileName {
				found = true
			}
		}
		if !found {
			Logger.Printf("Removing private key from memory: %s", config.keyInfos[j].privateKeyFilename)
			purgeIndices = append(purgeIndices, j)
		}
	}
	// Here, we remove the indicies, start from the back of the array so we don't have to re-calculate indicies
	for i := len(purgeIndices) - 1; i >= 0; i-- {
		// Go splice format
		config.keyInfos = append(config.keyInfos[:purgeIndices[i]], config.keyInfos[purgeIndices[i]+1:]...)
	}

	// Now, iterate over all file names matching the glob and try to load as a key
	for _, fileName := range fileNames {
		file, err := os.Open(fileName)
		if err != nil {
			// File open error
			sendMessage(protocol.TYPE_ERROR, "", config.topic, "", fmt.Appendf(nil, "Can't read private key from file %s, %v", fileName, err))
		}
		defer file.Close()

		var builder strings.Builder

		// Read the key as a text file
		// First, get the base64 encoded data
		scanner := bufio.NewScanner(file)
		var inFile = false
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "-----") {
				inFile = !inFile
			} else if inFile {
				builder.WriteString(line)
			}
		}

		// Now add to a variable
		b64Data := builder.String()

		// Decode the base64 encoded variable
		derData, err := base64.StdEncoding.DecodeString(b64Data)
		if err != nil {
			sendMessage(protocol.TYPE_ERROR, "", config.topic, "", fmt.Appendf(nil, "Error decoding base64 data: %v", err))
		}
		// hash the key so we can see if it's loaded already
		derString := fmt.Sprintf("%x", sha256.Sum256(derData))

		// Check if we have the key loaded already
		addKey := true
		for j := 0; j < len(config.keyInfos); j++ {
			if config.keyInfos[j].privateKeyHash == derString {
				sendMessage(protocol.TYPE_ERROR, "", config.topic, "", fmt.Appendf(nil, "Key file %s already loaded", config.keyInfos[j].privateKeyFilename))
				addKey = false
			}
		}

		// Parse the DER encoded private key
		key, err := x509.ParsePKCS8PrivateKey(derData)
		if err != nil {
			sendMessage(protocol.TYPE_ERROR, "", config.topic, "", fmt.Appendf(nil, "Error parsing private key: %v", err))
		}

		privateKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			sendMessage(protocol.TYPE_ERROR, "", config.topic, "", []byte("Private key is not of type *rsa.PrivateKey"))
		}

		if addKey {
			keyInfo := KeyInfo{
				privateKey:         privateKey,
				privateKeyFilename: fileName,
				privateKeyHash:     derString,
			}
			config.keyInfos = append(config.keyInfos, keyInfo)
			sendMessage(protocol.TYPE_STATUS, "", config.topic, "", fmt.Appendf(nil, "Successfully loaded key file: %s", fileName))
		}
	}
	return config.keyInfos
}

func defaultConfiguration() TransferConfiguration {
	config := TransferConfiguration{}
	config.logLevel = "INFO"
	config.id = "default_upstream"
	config.target = "kafka"
	config.logFileName = ""
	config.mtu = 0 // default auto
	config.translations = make(map[string]string)
	config.logStatistics = 0 // default no logging
	return config
}

// Read the configuration file and return the configuration
func readParameters(fileName string, result TransferConfiguration) (TransferConfiguration, error) {
	Logger.Print("Reading configuration from file " + fileName)
	file, err := os.Open(fileName)
	if err != nil {
		// No file, but that's ok. Maybe the user only uses environment variables
		Logger.Printf("File: %s not found. Continuing with environment variables configuration...", fileName)
		return TransferConfiguration{}, nil
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "id":
			Logger.Printf("id: %s", value)
			result.id = value
		case "mtu":
			if value == "auto" {
				result.mtu = 0
			} else {
				tmp, err := strconv.Atoi(value)
				if err != nil {
					Logger.Fatalf("Error in config mtu. Ilegal value: %s. Legal values are 'auto' or a two byte integer", value)
				} else {
					result.mtu = uint16(tmp)
				}
			}
			Logger.Printf("mtu: %d", result.mtu)
		case "logFileName":
			result.logFileName = value
			Logger.Printf("logFileName: %s", value)
		case "nic":
			result.nic = value
			Logger.Printf("nic: %s", value)
		case "targetIP":
			result.targetIP = value
			Logger.Printf("targetIP: %s", value)
		case "targetPort":
			tmp, err := strconv.Atoi(value)
			if err != nil {
				Logger.Fatalf("Error in config targetPort. Ilegal value: %s. Legal values are 0-65535", value)
			} else {
				if tmp < 0 {
					Logger.Fatalf("Error in config targetPort. Ilegal value: %s. Legal values are 0-65535", value)
				} else if tmp > 65535 {
					Logger.Fatalf("Error in config targetPort. Ilegal value: %s. Legal values are 0-65535", value)
				} else {
					result.targetPort = tmp
				}
			}
			Logger.Printf("targetPort: %d", result.targetPort)
		case "bootstrapServers":
			result.bootstrapServers = value
			Logger.Printf("bootstrapServers: %s", value)
		case "clientId":
			result.clientID = value
			Logger.Printf("clientId: %s", value)
		case "topic":
			result.topic = value
			Logger.Printf("topic: %s", value)
		case "privateKeyFiles": // glob
			result.privateKeyGlob = value
			Logger.Printf("privateKeyGlob: %s", value)
		case "logLevel":
			result.logLevel = strings.ToUpper(value)
			Logger.Printf("logLevel: %s", result.logLevel)
		case "target": // optional
			if value == "kafka" || value == "cmd" {
				result.target = value
				Logger.Printf("target: %s", value)
			} else {
				Logger.Fatalf("Unknown target %s", value)
			}
		case "certFile":
			result.certFile = value
			Logger.Printf("certFile: %s", value)
		case "keyFile":
			result.keyFile = value
			Logger.Printf("keyFile: %s", value)
		case "caFile":
			result.caFile = value
			Logger.Printf("caFile: %s", value)
		case "topicTranslations":
			// Json like {"inputTopic1":"outputTopic1","inputTopic2":"outputTopic2"}
			result.topicTranslations = value
		case "logStatistics":
			tmp, err := strconv.Atoi(value)
			if err != nil {
				Logger.Fatalf("Error in config logStatistics. Illegal value: %s. Legal values are 0-60", value)
			} else if tmp < 0 || tmp > 60 {
				Logger.Fatalf("Error in config logStatistics. Illegal value: %s. Legal values are 0-60", value)
			} else {
				result.logStatistics = int32(tmp)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return TransferConfiguration{}, err
	}

	return result, nil
}

func overrideConfiguration(config TransferConfiguration) TransferConfiguration {
	Logger.Print("Checking configuration from environment variables...")

	prefix := "AIRGAP_DOWNSTREAM_"
	if id := os.Getenv(prefix + "ID"); id != "" {
		Logger.Print("Overriding id with environment variable: " + prefix + "ID" + " with value: " + id)
		config.id = id
	}

	if nic := os.Getenv(prefix + "NIC"); nic != "" {
		Logger.Print("Overriding nic with environment variable: " + prefix + "NIC" + " with value: " + nic)
		config.nic = nic
	}

	if targetIP := os.Getenv(prefix + "TARGET_IP"); targetIP != "" {
		Logger.Print("Overriding targetIP with environment variable: " + prefix + "TARGET_IP" + " with value: " + targetIP)
		config.targetIP = targetIP
	}

	if targetPort := os.Getenv(prefix + "TARGET_PORT"); targetPort != "" {
		if port, err := strconv.Atoi(targetPort); err == nil {
			Logger.Print("Overriding targetPort with environment variable: " + prefix + "TARGET_PORT" + " with value: " + targetPort)
			config.targetPort = port
		}
	}
	if bootstrapServers := os.Getenv(prefix + "BOOTSTRAP_SERVERS"); bootstrapServers != "" {
		Logger.Print("Overriding bootstrapServers with environment variable: " + prefix + "BOOTSTRAP_SERVERS" + " with value: " + bootstrapServers)
		config.bootstrapServers = bootstrapServers
	}
	if topic := os.Getenv(prefix + "TOPIC"); topic != "" {
		Logger.Print("Overriding topic with environment variable: " + prefix + "TOPIC" + " with value: " + topic)
		config.topic = topic
	}
	if mtu := os.Getenv(prefix + "MTU"); mtu != "" {
		Logger.Print("Overriding mtu with environment variable: " + prefix + "MTU" + " with value: " + mtu)
		if mtu == "auto" {
			config.mtu = 0
		} else if mtuInt, err := strconv.Atoi(mtu); err == nil {
			config.mtu = uint16(mtuInt)
		}
	}
	if privateKeyGlob := os.Getenv(prefix + "PRIVATE_KEY_GLOB"); privateKeyGlob != "" {
		Logger.Print("Overriding privateKeyGlob with environment variable: " + prefix + "PRIVATE_KEY_GLOB" + " with value: " + privateKeyGlob)
		config.privateKeyGlob = privateKeyGlob
	}
	if target := os.Getenv(prefix + "TARGET"); target != "" {
		Logger.Print("Overriding target with environment variable: " + prefix + "TARGET" + " with value: " + target)
		config.target = target
	}
	if logLevel := os.Getenv(prefix + "LOG_LEVEL"); logLevel != "" {
		Logger.Print("Overriding logLevel with environment variable: " + prefix + "LOG_LEVEL" + " with value: " + logLevel)
		config.logLevel = logLevel
	}
	if logFileName := os.Getenv(prefix + "LOG_FILE_NAME"); logFileName != "" {
		Logger.Print("Overriding logFileName with environment variable: " + prefix + "LOG_FILE_NAME" + " with value: " + logFileName)
		config.logFileName = logFileName
	}
	if certFile := os.Getenv(prefix + "CERT_FILE"); certFile != "" {
		Logger.Print("Overriding certFile with environment variable: " + prefix + "CERT_FILE" + " with value: " + certFile)
		config.certFile = certFile
	}
	if keyFile := os.Getenv(prefix + "KEY_FILE"); keyFile != "" {
		Logger.Print("Overriding keyFile with environment variable: " + prefix + "KEY_FILE" + " with value: " + keyFile)
		config.keyFile = keyFile
	}
	if caFile := os.Getenv(prefix + "CA_FILE"); caFile != "" {
		Logger.Print("Overriding caFile with environment variable: " + prefix + "CA_FILE" + " with value: " + caFile)
		config.caFile = caFile
	}
	if topicTranslations := os.Getenv(prefix + "TOPIC_TRANSLATIONS"); topicTranslations != "" {
		Logger.Print("Overriding topicTranslations with environment variable: " + prefix + "TOPIC_TRANSLATIONS" + " with value: " + topicTranslations)
		config.topicTranslations = topicTranslations
	}
	if logStatistics := os.Getenv(prefix + "LOG_STATISTICS"); logStatistics != "" {
		tmp, err := strconv.Atoi(logStatistics)
		if err != nil {
			Logger.Fatalf("Error in config logStatistics. Illegal value: %s. Legal values are 0-60", logStatistics)
		} else if tmp < 0 || tmp > 60 {
			Logger.Fatalf("Error in config logStatistics. Illegal value: %s. Legal values are 0-60", logStatistics)
		} else {
			config.logStatistics = int32(tmp)
		}
	}

	return config
}

// The current configuration
func logConfiguration(config TransferConfiguration) {
	Logger.Printf("Configuration for build number %s:", BuildNumber)
	Logger.Printf("  id: %s", config.id)
	Logger.Printf("  nic: %s", config.nic)
	Logger.Printf("  targetIP: %s", config.targetIP)
	Logger.Printf("  targetPort: %d", config.targetPort)
	Logger.Printf("  bootstrapServers: %s", config.bootstrapServers)
	Logger.Printf("  topic: %s", config.topic)
	Logger.Printf("  mtu: %d", config.mtu)
	Logger.Printf("  privateKeyGlob: %s", config.privateKeyGlob)
	Logger.Printf("  target: %s", config.target)
	Logger.Printf("  logLevel: %s", config.logLevel)
	Logger.Printf("  logFileName: %s", config.logFileName)
	Logger.Printf("  certFile: %s", config.certFile)
	Logger.Printf("  keyFile: %s", config.keyFile)
	Logger.Printf("  caFile: %s", config.caFile)
	Logger.Printf("  topicTranslations: %s", config.topicTranslations)
	Logger.Printf("  logStatistics: %d", config.logStatistics)
}

// Check the configuration. On fail, will terminate the application
func checkConfiguration(result TransferConfiguration) TransferConfiguration {
	Logger.Print("Validating the configuration...")

	// Must have an id
	if result.id == "" {
		Logger.Fatal("Missing required configuration: id")
	}
	if result.nic == "" {
		Logger.Fatal("Missing required configuration: nic")
	}
	if result.targetIP == "" {
		Logger.Fatal("Missing required configuration: targetIP")
	}
	if result.targetPort < 0 || result.targetPort > 65535 {
		Logger.Fatal("Invalid configuration: targetPort must be between 0 and 65535")
	}
	if result.target != "kafka" && result.target != "cmd" {
		Logger.Fatalf("Unknown target '%s'. Valid targets are: 'kafka', 'cmd'", result.target)
	}
	if result.target == "kafka" && result.bootstrapServers == "" {
		Logger.Fatal("Missing required configuration: bootstrapServers")
	}
	if result.target == "kafka" && result.topic == "" {
		Logger.Fatal("Missing required configuration: topic")
	}
	if result.logFileName != "" {
		// Check that the logFileName is a valid file name
		file, err := os.OpenFile(result.logFileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			Logger.Fatalf("Cannot open log file '%s' for writing: %v", result.logFileName, err)
		}
		defer file.Close()
		// Check that we can write to that file
		if _, err := file.WriteString(""); err != nil {
			Logger.Fatalf("Cannot write to log file '%s': %v", result.logFileName, err)
		}
	}
	if result.logLevel != "" {
		Logger.SetLogLevel(result.logLevel)
		var tmp = Logger.GetLogLevel()
		if (tmp == result.logLevel) == false {
			Logger.Fatalf("Error in config logLevel. Illegal value: %s. Legal values are DEBUG, INFO, WARN, ERROR, FATAL", result.logLevel)
		} else {
			Logger.Printf("logLevel: %s", tmp)
		}
	}

	// if one of certFile, keyFile or caFile is given, they all must be
	if result.certFile != "" || result.keyFile != "" || result.caFile != "" {
		if result.certFile == "" {
			Logger.Fatalf("Missing required configuration: certFile")
		}
		if result.keyFile == "" {
			Logger.Fatalf("Missing required configuration: keyFile")
		}
		if result.caFile == "" {
			Logger.Fatalf("Missing required configuration: caFile")
		}
	}
	if result.topicTranslations != "" {
		if err := json.Unmarshal([]byte(result.topicTranslations), &result.translations); err != nil {
			Logger.Fatalf("Error in config topicTranslations. Illegal value: %s. Legal values are JSON objects 'from': 'to', ...", result.topicTranslations)
		}
	}
	Logger.Print("Configuration OK")
	return result
}

func connectToKafka(configuration TransferConfiguration) {
	Logger.Printf("Connecting to %s\n", config.bootstrapServers)
	// Check if we have TLS to Kafka
	conf := sarama.NewConfig()
	// Tell Sarama to use manual partitioning - we provide the partition in the ProducerMessage
	conf.Producer.Partitioner = sarama.NewManualPartitioner
	// Check if we have TLS to Kafka
	if configuration.certFile != "" || configuration.keyFile != "" || configuration.caFile != "" {
		Logger.Print("Using TLS for Kafka")
		tlsConfig, err := kafka.SetTLSConfigParameters(configuration.certFile, configuration.keyFile, configuration.caFile)
		if err != nil {
			Logger.Panicf("Error setting TLS config parameters: %v", err)
		}
		conf.Net.TLS.Enable = true
		conf.Net.TLS.Config = tlsConfig
	}
	conf.Producer.RequiredAcks = sarama.WaitForLocal
	conf.Producer.Return.Successes = true
	conf.Producer.Return.Errors = true
	conf.Net.KeepAlive = 30 * time.Second
	conf.ClientID = config.clientID
	producer, err := sarama.NewAsyncProducer(strings.Split(config.bootstrapServers, ","), conf)
	if err != nil {
		Logger.Panicf("Error creating producer: %v\n", err)
	}
	go func() {
		for err := range producer.Errors() {
			Logger.Println("Failed to send message:", err)
		}
	}()
	go func() {
		for msg := range producer.Successes() {
			if Logger.CanLog(logging.DEBUG) {
				// Log at most 80 characters from msg. Note that msg can be shorter
				valueBytes, err := msg.Value.Encode()
				keyBytes, _ := msg.Key.Encode()
				if err != nil {
					Logger.Debugf("Error encoding message value: %v", err)
				} else {
					if len(valueBytes) > 80 {
						Logger.Debugf("Message sent: key=%s partition=%d %s ...", string(keyBytes), msg.Partition, string(valueBytes[:80]))
					} else {
						Logger.Debugf("Message sent: key=%s partition=%d %s", string(keyBytes), msg.Partition, string(valueBytes))
					}
				}
			}
		}
	}()
	Logger.Printf("Connected to Kafka. Creating startup message...\n")
	config.producer = producer
	kafka.SetProducer(producer)
	// Start a new goroutine for sending kafka messages
	kafka.StartBackgroundThread()
}

func main() {
	timeStart = time.Now().Unix()
	Logger.Printf("Downstream version: %s starting up...", version.GitVersion)
	Logger.Printf("Build number: %s", BuildNumber)

	var fileName string
	if len(os.Args) == 2 {
		fileName = os.Args[1]
	}

	hup := make(chan os.Signal, 1)
	sigterm := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	signal.Notify(sigterm, os.Interrupt, syscall.SIGTERM)

	var udpStopChan chan struct{}
	var udpDoneChan chan struct{}

	udpStarted := false
	reloadFunc := func() {
		Logger.Println("Reading configuration...")
		configuration := defaultConfiguration()
		configuration, err := readParameters(fileName, configuration)
		if err != nil {
			Logger.Fatalf("Error reading configuration file %s: %v\n", fileName, err)
		}
		// Pick up environment variable overrides after reading config file
		configuration = overrideConfiguration(configuration)
		config = checkConfiguration(configuration)
		if config.logFileName != "" {
			Logger.Println("Setting up log to file: " + config.logFileName)
			err := Logger.SetLogFile(config.logFileName)
			if err != nil {
				Logger.Fatal(err)
			}
			Logger.Println("Log to file started up")
		}
		logConfiguration(configuration)
		address := fmt.Sprintf("%s:%d", config.targetIP, config.targetPort)
		if config.mtu == 0 {
			mtuValue, err := mtu.GetMTU(config.nic, address)
			if err != nil {
				Logger.Fatal(err)
			}
			config.mtu = uint16(mtuValue)
		}
		Logger.Printf("MTU set to: %d\n", config.mtu)
		Logger.Printf("Loading private keys from files: %s\n", config.privateKeyGlob)
		readPrivateKeys(config.privateKeyGlob)
		// Stop previous UDP server if running
		if udpStopChan != nil {
			close(udpStopChan)
			<-udpDoneChan
		}

		// Start statistics logger if enabled
		if config.logStatistics > 0 {
			go func() {
				interval := time.Duration(config.logStatistics) * time.Second
				for {
					time.Sleep(interval)
					recv := atomic.SwapInt64(&receivedEvents, 0)
					sent := atomic.SwapInt64(&sentEvents, 0)
					stats := map[string]any{
						"id":             config.id,
						"time":           time.Now().Unix(),
						"time_start":     timeStart,
						"interval":       config.logStatistics,
						"received":       recv,
						"sent":           sent,
						"total_received": atomic.LoadInt64(&totalReceived),
						"total_sent":     atomic.LoadInt64(&totalSent),
					}
					if Logger.CanLog(logging.INFO) {
						b, _ := json.Marshal(stats)
						Logger.Info("STATISTICS: " + string(b))
					}
				}
			}()
		}

		if config.target == "kafka" {
			// Stop Kafka background thread before closing/recreating producer
			kafka.StopBackgroundThread()
			if config.producer != nil {
				config.producer.AsyncClose()
				config.producer = nil
			}
			Logger.Debugf("Target is Kafka, connecting to %s\n", config.bootstrapServers)
			connectToKafka(config)
			kafka.StartBackgroundThread()
		}
		udpStopChan = make(chan struct{})
		udpDoneChan = make(chan struct{})
		Logger.Printf("Checking UDP port availability on %s:%d...\n", config.targetIP, config.targetPort)
		maxRetries := 5
		for i := 0; i < maxRetries; i++ {
			addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", config.targetIP, config.targetPort))
			if err != nil {
				Logger.Errorf("Failed to resolve UDP address: %v", err)
				time.Sleep(200 * time.Millisecond)
				continue
			}
			testConn, err := net.ListenUDP("udp", addr)
			if err == nil {
				testConn.Close()
				udpStarted = true
				break
			}
			Logger.Errorf("UDP port %d not available (attempt %d/%d), retrying in 200ms...", config.targetPort, i+1, maxRetries)
			time.Sleep(200 * time.Millisecond)
			if i == maxRetries-1 {
				Logger.Errorf("UDP port %d still not available after retries, giving up.", config.targetPort)
				udpStarted = false
				return
			}
		}

		if udpStarted {
			Logger.Printf("Starting UDP Server on %s:%d\n", config.targetIP, config.targetPort)
			// // Connect to Kafka and start background thread
			// connectToKafka(config)
			// kafka.StartBackgroundThread()
			// Send to Kafka too:
			sendMessage(protocol.TYPE_STATUS, "", config.topic, "", fmt.Appendf(nil, "Downstream %s starting UDP server on port %d", config.id, config.targetPort))
			go func() {
				udp.ListenUDPWithStop(config.targetIP, config.targetPort, handleUdpMessage, config.mtu, udpStopChan)
				close(udpDoneChan)
			}()
		}
	}

	// Initial startup
	reloadFunc()

	running := true
	for running {
		select {
		case <-sigterm:
			Logger.Printf("%s: Received SIGINT/SIGTERM, exiting...", config.id)
			sendMessage(protocol.TYPE_STATUS, "", config.topic, "", []byte("terminating by signal"))
			// Stop receiving new UDP data only if UDP was started
			if udpStarted && udpStopChan != nil {
				close(udpStopChan)
				<-udpDoneChan // Wait for UDP goroutine to finish
				Logger.Printf("%s: UDP server stopped, waiting for Kafka background thread to flush...", config.id)
			}
			// Stop Kafka background thread and flush messages
			kafka.StopBackgroundThread()
			if config.producer != nil {
				config.producer.AsyncClose()
				config.producer = nil
			}
			Logger.Printf("%s: Downstream process exited.", config.id)
			running = false
		case <-hup:
			Logger.Printf("SIGHUP received: reopening log file for logrotate...")
			if config.logFileName != "" {
				err := Logger.SetLogFile(config.logFileName)
				if err != nil {
					Logger.Errorf("Error reopening log file: %v", err)
				} else {
					Logger.Printf("Log file reopened: %s", config.logFileName)
				}
			}
			// Do NOT reload config or restart UDP server
		}
	}
	Logger.Printf("%s Downstream exiting\n", config.id)
}

// To be able to assemble fragmented events
// The cache is used to store the fragments until it
// is assembled into a complete event
// Old events are removed from the cache by another thread
var cache = protocol.CreateMessageCache()

func getTopicTranslation(inputTopic string) string {
	if config.topicTranslations != "" {
		if outputTopic, ok := config.translations[inputTopic]; ok {
			Logger.Debugf("Translating topic '%s' to '%s'\n", inputTopic, outputTopic)
			return outputTopic
		} else {
			Logger.Debugf("No translation for topic '%s', using same topic\n", inputTopic)
		}
	}
	return inputTopic
}

// Send a message to Kafka or stdout
func sendMessage(messageType uint8, id string, topicName string, partitionStr string, message []byte) {
	// For extra printouts, change this:
	topic := getTopicTranslation(topicName)
	// If no id, just create one
	var messageKey string
	if id == "" {
		messageKey = uuid.New().String()
	} else {
		messageKey = id
	}
	if partitionStr == "" {
		partitionStr = "0"
	}
	partitionInt, err := strconv.Atoi(partitionStr)
	if err != nil {
		partitionInt = 0
	}
	partition := int32(partitionInt)
	if Logger.CanLog(logging.DEBUG) {
		// Print at most 40 characters of the message
		msgStr := string(message)
		if len(msgStr) > 40 {
			msgStr = msgStr[:40]
		}
		Logger.Debugf("%s Sending cleartext message to %s: %s to topic: %s on partition: %d for message key: %s", config.id, topic, msgStr, topic, partition, messageKey)
	}
	// If this is an error message, prepend a timestamp
	var toSend []byte = message
	if messageType == protocol.TYPE_ERROR || messageType == protocol.TYPE_STATUS {
		toSend = fmt.Appendf(nil, "%s %s %s", protocol.GetTimestamp(), config.id, string(message))
		Logger.Print(string(toSend))
	}

	// Send the data to Kafka
	if config.target == "kafka" {
		// Send the result to Kafka
		Logger.Debugf("Sending message with key %s to Kafka topic %s partition %d\n", messageKey, topic, partition)
		kafka.WriteToKafka(messageKey, topic, partition, toSend)
		atomic.AddInt64(&sentEvents, 1)
		atomic.AddInt64(&totalSent, 1)
	} else {
		// send to stdout
		os.Stdout.Write(toSend)
		os.Stdout.Write([]byte("\n"))
	}
}

// This is called for every message read from UDP
func handleUdpMessage(receivedBytes []byte) {
	atomic.AddInt64(&receivedEvents, 1)
	atomic.AddInt64(&totalReceived, 1)
	Logger.Debugf("Received %d bytes from UDP\n", len(receivedBytes))
	// Try our format
	var messageType uint8
	messageType, messageId, payload, ok := protocol.ParseMessage(receivedBytes, cache)

	// Get the partition
	topic, partition, offset, err := protocol.ParseMessageId(messageId)
	if err != nil {
		// May be a status message, not an actual message from the Upstream kafka
		topic = config.topic // default
		partition = "0"      // Don't care about the partition
	}

	Logger.Debugf("MessageType %d, messageId %s_%s_%s\n", messageType, topic, partition, offset)
	nrErrorMessages := 0
	errorMessageLastTime := time.Now()
	errorMessageEvery := 60 * time.Second
	if ok != nil {
		// Error
		Logger.Fatalf("Error parsing message %s, %v\n", receivedBytes, ok)
	} else {
		switch messageType {
		case protocol.TYPE_KEY_EXCHANGE:
			// Get the new key from the message
			keyFileNameUsed := readNewKey(payload)
			// and send a key-change log event to Kafka
			message := fmt.Appendf(nil, "Updating symmetric key with private key file: %s", keyFileNameUsed)
			Logger.Print(string(message))
			sendMessage(protocol.TYPE_STATUS, "", config.topic, "", message)
		case protocol.TYPE_CLEARTEXT:
			// Cleartext message
			sendMessage(protocol.TYPE_MESSAGE, messageId, topic, partition, payload)
		case protocol.TYPE_MESSAGE:
			// Encrypted message. Decrypt
			decrypted, err := protocol.Decrypt(payload, config.key)
			if err != nil {
				// Error decrypting message. Always send the error message to Kafka
				message := fmt.Appendf(nil, "ERROR decrypting message: %s", err)
				Logger.Print(string(message))
				sendMessage(protocol.TYPE_ERROR, messageId, config.topic, "", message)
			} else {
				// Decrypted message ok
				sendMessage(protocol.TYPE_MESSAGE, messageId, topic, partition, decrypted)
			}
		case protocol.TYPE_ERROR:
			if nrErrorMessages == 0 {
				// Always send first time an error occurs
				message := fmt.Appendf(nil, "ERROR message received: %s", payload)
				Logger.Print(string(message))
				sendMessage(protocol.TYPE_ERROR, messageId, config.topic, "", message)
				nrErrorMessages += 1
			} else {
				if time.Now().After(errorMessageLastTime.Add(errorMessageEvery)) {
					// Send error messages periodically
					message := fmt.Appendf(nil, "ERROR messages received: %d, last received: %s",
						nrErrorMessages,
						payload)
					Logger.Print(string(message))
					sendMessage(protocol.TYPE_ERROR, messageId, config.topic, "", message)
				}
			}
		case protocol.TYPE_MULTIPART:
			Logger.Debugf("Waiting for last fragment of multipart message. id: %s", messageId)
			// Do nothing. Wait for the last fragment
		default:
			// Just send the text to Kafka with an empty ID (will create a new ID)
			sendMessage(protocol.TYPE_MESSAGE, "", config.topic, "", payload)
		}
	}
}

// TODO: Use key id for both symmetric and assymetric keys

// Side effect. Update the config.key parameter
func readNewKey(message []byte) string {
	// We have our private key in memory. This message
	// contains the new symmetric key to use, enrypted with
	// our public key
	// The message can look like this:
	// KEY_UPDATE#(here comes binary data)
	payload := message[11:]

	// Just to be sure, we read the private keys again
	// since they can have changed on disk
	readPrivateKeys(config.privateKeyGlob)

	// Decrypt the symmetric key with the private keys until we get the message:
	// KEY_UPDATE# as the first 11 bytes
	Logger.Debug("Checking " + fmt.Sprint(len(config.keyInfos)) + " keys for new symmetric key")
	for i := range config.keyInfos {
		Logger.Debug("Checking key " + fmt.Sprint(i))
		privateKey := config.keyInfos[i].privateKey
		decrypted, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, privateKey, payload, nil)
		if err == nil {
			if string(decrypted[0:11]) == "KEY_UPDATE#" {
				// We got the correct key
				sendMessage(protocol.TYPE_STATUS, "", config.topic, "", fmt.Appendf(nil, "Got new symmetric key: %s", config.keyInfos[i].privateKeyFilename))
				config.key = decrypted[11:]
				return config.keyInfos[i].privateKeyFilename
			}
		}
	}
	sendMessage(protocol.TYPE_ERROR, "", config.topic, "", fmt.Appendf(nil, "Can't decrypt the new symmetric key. Tried all %v private keys\n", len(config.keyInfos)))
	return "ERROR: No key found that can decrypt the new symmetric key"
}
