package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"sitia.nu/airgap/src/kafka"
	"sitia.nu/airgap/src/logfile"
	"sitia.nu/airgap/src/mtu"
	"sitia.nu/airgap/src/protocol"
	"sitia.nu/airgap/src/udp"
	"sitia.nu/airgap/src/version"
)

type TransferConfiguration struct {
	id                           string           // unique id for this upstream
	nic                          string           // network interface card
	targetIP                     string           // target IP address
	targetPort                   int              // target port
	bootstrapServers             string           // Kafka bootstrap servers
	topic                        string           // Kafka topic
	groupID                      string           // Kafka group ID
	mtu                          uint16           // Maximum Transmission Unit
	from                         string           // Start time for reading from Kafka
	encryption                   bool             // encryption on/off
	key                          []byte           // symmetric key in use
	newkey                       []byte           // a new symmetric key, when successfully sent, copy the value to key
	publicKey                    *rsa.PublicKey   // public key for encrypting the symmetric key
	publicKeyFile                string           // file with the public key
	source                       string           // source of the messages, kafka or random
	generateNewSymmetricKeyEvery int              // seconds between key generation
	verbose                      bool             // verbose output
	logFileName                  string           // log file name, redirect logging to a file from the console
	sendingThreads               []map[string]int // array of objects with thread names and offsets
	certFile                     string           // Certificate to use to communicate with Kafka with TLS
	keyFile                      string           // Key file to use for TLS
	caFile                       string           // CA file to use for TLS
}

var config TransferConfiguration
var nextKeyGeneration time.Time
var keepRunning bool = true

// Start by logging to the console. If a log file is specified in the configuration file, use that
// instead. The log file will be created if it doesn't exist, and appended to if it does.
// Any errors creating the log file will be reported to the console.
var Logger = log.New(os.Stdout, "", log.LstdFlags)

// Create a new symmetric key for the encryption.
func createNewKey() []byte {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	if err != nil {
		Logger.Panicf("Error generating key: %v", err)
	}
	return key
}

// Read the public key from a file and return it.
func readPublicKey(fileName string) *rsa.PublicKey {
	file, err := os.Open(fileName)
	if err != nil {
		Logger.Fatalf("Can't read public key from file %s, %v", fileName, err)
	}
	defer file.Close()

	var builder strings.Builder

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		builder.WriteString(line)
		builder.WriteString("\n")
	}

	block, _ := pem.Decode([]byte(builder.String()))
	if block == nil {
		Logger.Fatal("failed to parse PEM block containing the public key")
	}
	Logger.Printf("block type %s", block.Type)

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		Logger.Fatalf("failed to parse DER encoded certificate: " + err.Error())
	}

	pubKey, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		Logger.Fatal("public key is not of type *rsa.PublicKey")
	}
	return pubKey
}

func defaultConfiguration() TransferConfiguration {
	config := TransferConfiguration{}
	config.verbose = false
	config.encryption = false
	config.id = "default_upstream"
	config.logFileName = ""
	config.mtu = 0 // default auto
	config.sendingThreads = []map[string]int{
		{"now": 0},
	}
	return config
}

// Parse the configuration file and return a TransferConfiguration struct.
func readParameters(fileName string, result TransferConfiguration) (TransferConfiguration, error) {
	if fileName == "" {
		return result, nil
	}
	file, err := os.Open(fileName)
	if err != nil {
		return result, err
	}
	defer file.Close()

	Logger.Print("Reading configuration from file " + fileName)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "id":
			result.id = value
			Logger.Printf("id: %s", value)
		case "mtu":
			if value == "auto" {
				result.mtu = 0
			} else {
				tmp, err := strconv.Atoi(value)
				if err != nil {
					Logger.Fatalf("Error in config mtu. Illegal value: %s. Legal values are 'auto' or a two byte integer", value)
				} else {
					result.mtu = uint16(tmp)
				}
			}
			Logger.Printf("mtu: %d", result.mtu)
		case "nic":
			result.nic = value
			Logger.Printf("nic: %s", value)
		case "logFileName":
			result.logFileName = value
			Logger.Printf("logFileName: %s", value)
		case "targetIP":
			result.targetIP = value
			Logger.Printf("targetIP: %s", value)
		case "targetPort":
			tmp, err := strconv.Atoi(value)
			if err != nil {
				Logger.Fatalf("Error in config targetPort. Illegal value: %s. Legal values are 0-65535", value)
			} else {
				if tmp < 0 || tmp > 65535 {
					Logger.Fatalf("Error in config targetPort. Illegal value: %s. Legal values are 0-65535", value)
				} else {
					result.targetPort = tmp
				}
			}
			Logger.Printf("targetPort: %d", result.targetPort)
		case "bootstrapServers":
			result.bootstrapServers = value
			Logger.Printf("bootstrapServers: %s", value)
		case "topic":
			result.topic = value
			Logger.Printf("topic: %s", value)
		case "groupID":
			result.groupID = value
		case "from":
			result.from = value
			Logger.Printf("from: %s", value)
		case "publicKeyFile":
			result.publicKeyFile = value
			Logger.Printf("publicKeyFile: %s", value)
		case "source":
			if value == "kafka" || value == "random" {
				result.source = value
			} else {
				Logger.Fatalf("Unknown source %s. Legal values are 'kafka' or 'random'.", value)
			}
			Logger.Printf("source: %s", value)
		case "verbose":
			tmp, err := strconv.ParseBool(value)
			if err != nil {
				Logger.Fatalf("Error in config verbose. Illegal value: %s. Legal values are true or false", value)
			} else {
				result.verbose = tmp
			}
			var verboseStr string
			if result.verbose {
				verboseStr = "true"
			} else {
				verboseStr = "false"
			}
			Logger.Printf("verbose: %s", verboseStr)
		case "encryption":
			tmp, err := strconv.ParseBool(value)
			if err != nil {
				Logger.Fatalf("Error in config encryption. Illegal value: %s. Legal values are true or false", value)
			} else {
				result.encryption = tmp
			}
			var encryptionStr string
			if result.encryption {
				encryptionStr = "true"
			} else {
				encryptionStr = "false"
			}
			Logger.Printf("encryption: %s", encryptionStr)
		case "generateNewSymmetricKeyEvery": // second
			tmp, err := strconv.Atoi(value)
			if err != nil {
				Logger.Fatalf("Error in config generateNewSymmetricKeyEvery. Illegal value: %s. Legal values are a four byte integer", value)
			} else {
				if tmp < 2 {
					Logger.Fatalf("Error in config generateNewSymmetricKeyEvery. Illegal value: %s. Legal values are integer >= 2", value)
				} else {
					result.generateNewSymmetricKeyEvery = tmp
					Logger.Printf("generateNewSymmetricKeyEvery: %d", tmp)
				}
			}
		case "sendingThreads":
			// Remove the old value for result.sendingThreads
			result.sendingThreads = nil
			// Read the new ones
			if err := json.Unmarshal([]byte(value), &result.sendingThreads); err != nil {
				Logger.Fatalf("Error in config sendingThreads. Illegal value: %s. Legal values are an array of objects", value)
			}
			Logger.Printf("sendingThreads: %v", result.sendingThreads)

		case "certFile":
			result.certFile = value
			Logger.Printf("certFile: %s", value)
		case "keyFile":
			result.keyFile = value
			Logger.Printf("keyFile: %s", value)
		case "caFile":
			result.caFile = value
			Logger.Printf("caFile: %s", value)
		}
	}

	if result.source == "kafka" {
		// Check the sending threads
		if len(result.sendingThreads) == 0 {
			Logger.Print("No sendingThreads found. Adding default: sendingThreads: [{'now': 0}]")
			result.sendingThreads = []map[string]int{
				{"now": 0},
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return result, err
	}

	return result, nil
}

func overrideConfiguration(config TransferConfiguration) TransferConfiguration {
	Logger.Print("Checking configuration from environment variables...")

	prefix := "AIRGAP_UPSTREAM_"
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
	if groupID := os.Getenv(prefix + "GROUP_ID"); groupID != "" {
		Logger.Print("Overriding groupID with environment variable: " + prefix + "GROUP_ID" + " with value: " + groupID)
		config.groupID = groupID
	}
	if mtu := os.Getenv(prefix + "MTU"); mtu != "" {
		Logger.Print("Overriding mtu with environment variable: " + prefix + "MTU" + " with value: " + mtu)
		if mtu == "auto" {
			config.mtu = 0
		} else if mtuInt, err := strconv.Atoi(mtu); err == nil {
			config.mtu = uint16(mtuInt)
		}
	}
	if from := os.Getenv(prefix + "FROM"); from != "" {
		Logger.Print("Overriding from with environment variable: " + prefix + "FROM" + " with value: " + from)
		config.from = from
	}
	if encryption := os.Getenv(prefix + "ENCRYPTION"); encryption != "" {
		Logger.Print("Overriding encryption with environment variable: " + prefix + "ENCRYPTION" + " with value: " + encryption)
		config.encryption = encryption == "true"
	}
	if publicKeyFile := os.Getenv(prefix + "PUBLIC_KEY_FILE"); publicKeyFile != "" {
		Logger.Print("Overriding publicKeyFile with environment variable: " + prefix + "PUBLIC_KEY_FILE" + " with value: " + publicKeyFile)
		config.publicKeyFile = publicKeyFile
	}
	if generateNewSymmetricKeyEvery := os.Getenv(prefix + "GENERATE_NEW_SYMMETRIC_KEY_EVERY"); generateNewSymmetricKeyEvery != "" {
		if generateNewSymmetricKeyEveryInt, err := strconv.Atoi(generateNewSymmetricKeyEvery); err == nil {
			Logger.Print("Overriding generateNewSymmetricKeyEvery with environment variable: " + prefix + "GENERATE_NEW_SYMMETRIC_KEY_EVERY" + " with value: " + generateNewSymmetricKeyEvery)
			config.generateNewSymmetricKeyEvery = generateNewSymmetricKeyEveryInt
		}
	}
	if verbose := os.Getenv(prefix + "VERBOSE"); verbose != "" {
		Logger.Print("Overriding verbose with environment variable: " + prefix + "VERBOSE" + " with value: " + verbose)
		config.verbose = verbose == "true"
	}
	if logFileName := os.Getenv(prefix + "LOG_FILE_NAME"); logFileName != "" {
		Logger.Print("Overriding logFileName with environment variable: " + prefix + "LOG_FILE_NAME" + " with value: " + logFileName)
		config.logFileName = logFileName
	}
	if sendingThreads := os.Getenv(prefix + "SENDING_THREADS"); sendingThreads != "" {
		Logger.Print("Overriding sendingThreads with environment variable: " + prefix + "SENDING_THREADS" + " with value: " + sendingThreads)
		if err := json.Unmarshal([]byte(sendingThreads), &config.sendingThreads); err != nil {
			Logger.Fatalln("Error parsing SENDING_THREADS:", err)
		}
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
	if source := os.Getenv(prefix + "SOURCE"); source != "" {
		Logger.Print("Overriding source with environment variable: " + prefix + "SOURCE" + " with value: " + source)
		config.source = source
	}

	return config
}

// Check the configuration. On fail, will terminate the application
func checkConfiguration(result TransferConfiguration) {
	Logger.Print("Validating the configuration...")

	// Must have an id
	if result.id == "" {
		Logger.Fatal("Missing required configuration: id")
	}
	if result.nic == "" {
		Logger.Fatal("Missing required configuration: nic")
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
	if result.targetIP == "" {
		Logger.Fatal("Missing required configuration: targetIP")
	}
	if result.targetPort < 0 || result.targetPort > 65535 {
		Logger.Fatal("Invalid configuration: targetPort must be between 0 and 65535")
	}
	if result.bootstrapServers == "" {
		Logger.Fatal("Missing required configuration: bootstrapServers")

	}
	if result.topic == "" {
		Logger.Fatal("Missing required configuration: topic")
	}
	if result.groupID == "" {
		Logger.Fatal("Missing required configuration: groupID")
	}
	if result.publicKeyFile != "" {
		// Check that the name is a valid file name and that we can open and read from that file
		file, err := os.OpenFile(result.publicKeyFile, os.O_RDONLY, 0644)
		if err != nil {
			Logger.Fatalf("Cannot open public key file '%s' for reading: %v", result.publicKeyFile, err)
		}
		defer file.Close()
		// Check that we can read from that file
		if _, err := file.Stat(); err != nil {
			Logger.Fatalf("Cannot read from public key file '%s': %v", result.publicKeyFile, err)
		}
	}
	if result.source != "kafka" && result.source != "random" {
		Logger.Fatalf("Unknown source %s. Legal values are 'kafka' or 'random'.", result.source)
	}
	if result.verbose {
		Logger.Printf("verbose: %t", result.verbose)
	}
	if result.encryption {
		Logger.Printf("encryption: %t", result.encryption)
		if result.publicKeyFile == "" {
			Logger.Fatalf("Missing required configuration: publicKeyFile")
		}
	}
	if result.generateNewSymmetricKeyEvery < 2 {
		Logger.Fatalf("Error in config generateNewSymmetricKeyEvery. Illegal value: %d. Legal values are integer >= 2", result.generateNewSymmetricKeyEvery)
	}
	if len(result.sendingThreads) == 0 {
		Logger.Fatalf("Error in config sendingThreads. Illegal value: %v. Legal values are an array of objects", result.sendingThreads)
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
}

func logConfiguration(config TransferConfiguration) {
	Logger.Printf("Configuration:")
	Logger.Printf("  id: %s", config.id)
	Logger.Printf("  nic: %s", config.nic)
	Logger.Printf("  targetIP: %s", config.targetIP)
	Logger.Printf("  targetPort: %d", config.targetPort)
	Logger.Printf("  bootstrapServers: %s", config.bootstrapServers)
	Logger.Printf("  topic: %s", config.topic)
	Logger.Printf("  groupID: %s", config.groupID)
	Logger.Printf("  mtu: %d", config.mtu)
	Logger.Printf("  from: %s", config.from)
	Logger.Printf("  encryption: %t", config.encryption)
	if config.publicKeyFile != "" {
		Logger.Printf("  publicKeyFile: %s", config.publicKeyFile)
	}
	Logger.Printf("  source: %s", config.source)
	Logger.Printf("  generateNewSymmetricKeyEvery: %d seconds", config.generateNewSymmetricKeyEvery)
	if len(config.sendingThreads) > 0 {
		for i, thread := range config.sendingThreads {
			for name, offset := range thread {
				Logger.Printf("  sendingThread[%d]: name=%s, offset=%d seconds", i, name, offset)
			}
		}
	}
	Logger.Printf("  certFile: %s", config.certFile)
	Logger.Printf("  keyFile: %s", config.keyFile)
	Logger.Printf("  caFile: %s", config.caFile)
}

// Generate a new symmetric key and encrypt the key with the
// public key from the certificate.
// Return the encrypted symmetric key
func generateNewKey() []byte {
	// Create a symmetric key
	config.newkey = createNewKey()

	// Get the public key for downstream
	config.publicKey = readPublicKey(config.publicKeyFile)

	// We would like to encrypt an [] byte that contains
	// - A Guard: KEY_UPDATE#
	// - The new key

	// Create a Guard
	// In downstream.go we decrypt key exchange messages with
	// every private key we have until we find a decrypted message
	// that starts with this string.
	guard := "KEY_UPDATE#"
	toEncrypt := []byte(guard)
	toEncrypt = append(toEncrypt, config.newkey...)

	// Encrypt the initial key with the downstream public key
	encryptedKey, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, config.publicKey, toEncrypt, nil)

	if err != nil {
		Logger.Panicf("Error encrypting key: %v", err)
	}

	// encryptedKey now contains the encrypted key
	return encryptedKey
}

// Create a message with message type KEY_EXCHANGE
// and message id KEY_UPDATE# (the guard)
// Set the next time for key re-generation
func sendNewKey(conn *udp.UDPConn) {
	byteKey := generateNewKey()
	guard := "KEY_UPDATE#"
	toSend := []byte(guard)
	toSend = append(toSend, byteKey...)

	Logger.Printf("Sending key exchange message...")
	messages := protocol.FormatMessage(protocol.TYPE_KEY_EXCHANGE, "KEY_UPDATE#", toSend, config.mtu)
	if conn != nil {
		udpErr := conn.SendMessages(messages)
		if udpErr != nil {
			Logger.Fatalf("Error sending encryption key: %v", udpErr)
		}
	}
	// Now, we should be using the new key too
	config.key = config.newkey

	// Set the time for the next key generation
	if config.generateNewSymmetricKeyEvery > 0 {
		nextKeyGeneration = time.Now().Add(time.Duration(config.generateNewSymmetricKeyEvery) * time.Second)
	}

}

// main is the entry point of the program.
// It reads a configuration file from the command line parameter,
// initializes the necessary variables, and starts the upstream process.
func main() {
	var conn *udp.UDPConn
	Logger.Printf("Upstream version: %s starting up...", version.GitVersion)
	fileName := ""
	if len(os.Args) > 2 {
		Logger.Fatal("Too many command line parameters. Only one parameter is allowed: the configuration file.")
	}
	if len(os.Args) == 2 {
		fileName = os.Args[1]
	}

	hup := make(chan os.Signal, 1)
	sigterm := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	signal.Notify(sigterm, syscall.SIGINT, syscall.SIGTERM)

	var cancel context.CancelFunc
	var ctx context.Context

	reload := func() {
		// Start with the default parameters
		configuration := defaultConfiguration()
		// Read configuration from file, if added
		var err error
		configuration, err = readParameters(fileName, configuration)
		// May override with environment variables
		configuration = overrideConfiguration(configuration)
		// Make sure we have everything we need in the config
		checkConfiguration(configuration)
		if err != nil {
			Logger.Fatalf("Error reading configuration file %s: %v", fileName, err)
		}
		// Add to package variable
		config = configuration

		// Set the log file name
		if config.logFileName != "" {
			Logger.Println("Configuring log to: " + config.logFileName)
			err := logfile.SetLogFile(config.logFileName, Logger)
			if err != nil {
				Logger.Fatal(err)
			}
			Logger.Printf("Upstream version: %s", version.GitVersion)
			Logger.Println("Log to file started up")
			kafka.SetLogger(Logger)
		}

		// Now log the complete configuration to stdout or file
		logConfiguration(config)

		// ip:port to send to
		address := fmt.Sprintf("%s:%d", config.targetIP, config.targetPort)

		// The GetMTU will open the connection with UDP on the specified NIC
		// and be able to read the MTU for that connection. This is vital
		// so we know how to fragment our data
		if config.mtu == 0 {
			mtuValue, err := mtu.GetMTU(config.nic, address)
			if err != nil {
				Logger.Fatal(err)
			}
			config.mtu = uint16(mtuValue)
		}
		Logger.Printf("MTU from NIC: %d\n", config.mtu)

		// Create the UDP sender once
		conn, err = udp.NewUDPConn(address)
		if err != nil {
			Logger.Fatalf("Error creating UDP connection: %v", err)
			return
		}

		messages := protocol.FormatMessage(protocol.TYPE_STATUS, "STATUS",
			[]byte(fmt.Sprintf("%s %s starting up", protocol.GetTimestamp(), config.id)), config.mtu)
		if len(messages) == 1 {
			udpErr := conn.SendMessage(messages[0])
			if udpErr != nil {
				if strings.Contains(udpErr.Error(), "udp-connection-refused") || strings.Contains(udpErr.Error(), "udp-connection-closed") {
					Logger.Printf("UDP connection error detected, attempting to recreate connection: %v", udpErr)
					conn.Close()
					conn, err = udp.NewUDPConn(address)
					if err != nil {
						Logger.Fatalf("Error recreating UDP connection: %v", err)
					} else {
						Logger.Printf("UDP connection recreated successfully.")
						// Try sending again
						udpErr = conn.SendMessage(messages[0])
						if udpErr != nil {
							Logger.Fatalf("Error after UDP reconnect: %v", udpErr)
						}
					}
				} else {
					Logger.Fatalf("Error: %v", udpErr)
				}
			}
		}

		// Create initial key if the config contains a publicKeyFile
		if config.encryption {
			Logger.Printf("Creating initial key with public key file: %s", config.publicKeyFile)
			sendNewKey(conn)
		} else {
			Logger.Printf("No encryption will be used between upstream and downstream.")
		}

		// Cancel previous threads if any
		if cancel != nil {
			cancel()
			// Give threads a moment to shut down
			time.Sleep(200 * time.Millisecond)
		}
		ctx, cancel = context.WithCancel(context.Background())

		// Closure for Kafka message handler, captures conn
		kafkaHandler := func(id string, key []byte, t time.Time, received []byte) bool {
			var messages [][]byte
			// Logger.Println("handleKafkaMessage called with message byte array length: ", len(received))
			var err error
			if config.encryption {
				var ciphertext []byte
				ciphertext, err = protocol.Encrypt(received, config.key)
				messages = protocol.FormatMessage(protocol.TYPE_MESSAGE, id, ciphertext, config.mtu)
			} else {
				messages = protocol.FormatMessage(protocol.TYPE_CLEARTEXT, id, received, config.mtu)
			}
			if config.verbose {
				Logger.Println(id + " - " + string(received))
			}
			if err != nil {
				Logger.Println(err)
			}
			udpErr := conn.SendMessages(messages)
			if udpErr != nil {
				if strings.Contains(udpErr.Error(), "udp-connection-refused") || strings.Contains(udpErr.Error(), "udp-connection-closed") {
					Logger.Printf("UDP connection error detected in Kafka handler, attempting to recreate connection: %v", udpErr)
					conn.Close()
					conn, err = udp.NewUDPConn(fmt.Sprintf("%s:%d", config.targetIP, config.targetPort))
					if err != nil {
						Logger.Printf("Error recreating UDP connection: %v", err)
					} else {
						Logger.Printf("UDP connection recreated successfully in Kafka handler.")
						// Try sending again
						udpErr = conn.SendMessages(messages)
						if udpErr != nil {
							Logger.Printf("Error after UDP reconnect in Kafka handler: %v", udpErr)
						}
					}
				} else {
					Logger.Printf("Error sending UDP messages: %v", udpErr)
				}
			}
			if config.encryption && config.generateNewSymmetricKeyEvery > 0 {
				if time.Now().After(nextKeyGeneration) {
					if config.publicKeyFile != "" {
						sendNewKey(conn)
					}
				}
			}
			return keepRunning
		}

		// Now, read from Kafka and call our handler for each message, or generate random messages for test purposes
		if config.source == "kafka" {
			Logger.Printf("Reading from Kafka %s", config.bootstrapServers)
			kafka.SetVerbose(config.verbose)
			// Check if we have TLS to Kafka
			if configuration.certFile != "" || configuration.keyFile != "" || configuration.caFile != "" {
				Logger.Print("Using TLS for Kafka")
				kafka.SetTLSConfigParameters(configuration.certFile, configuration.keyFile, configuration.caFile)
			}

			// Spawn one thread for each entry in config.sendingThreads
			for _, thread := range config.sendingThreads {
				go func(thread map[string]int) {
					for name, offset := range thread {
						Logger.Printf("Starting sending thread: %s (offset: %d)", name, offset)
						group := fmt.Sprintf("%s-%s", config.groupID, name)
						kafka.ReadFromKafkaWithContext(ctx, name, offset, config.bootstrapServers, config.topic, group, config.from, kafkaHandler)
					}
				}(thread)
			}
		}
	}

	reload() // initial start

	for running := true; running; {
		select {
		case <-sigterm:
			// SIGTERM received, send a message to the downstream that we are shutting down
			messages := protocol.FormatMessage(protocol.TYPE_STATUS,
				"STATUS",
				fmt.Appendf(nil, "Downstream id: %s terminating by signal", config.id), config.mtu)
			if conn != nil {
				conn.SendMessages(messages)
			}
			time.Sleep(100 * time.Millisecond)
			if cancel != nil {
				cancel()
			}
			running = false
		case <-hup:
			Logger.Printf("SIGHUP received: reopening log file for logrotate...")
			if config.logFileName != "" {
				err := logfile.SetLogFile(config.logFileName, Logger)
				if err != nil {
					Logger.Printf("Error reopening log file: %v", err)
				} else {
					Logger.Printf("Log file reopened: %s", config.logFileName)
				}
			}
			// Do NOT reload config or restart threads
		}
	}
	if conn != nil {
		conn.Close()
	}
}
