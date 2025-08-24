package main

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
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
)

type TransferConfiguration struct {
    id                  string          // unique id for this upstream
	nic                 string          // network interface card
    targetIP            string          // target IP address
    targetPort          int             // target port
    bootstrapServers    string          // Kafka bootstrap servers
    topic               string          // Kafka topic
    groupID             string          // Kafka group ID
    mtu                 uint16          // Maximum Transmission Unit
    from                string          // Start time for reading from Kafka
    encryption          bool            // encryption on/off
    key                 []byte          // symmetric key in use
    newkey              []byte          // a new symmetric key, when successfully sent, copy the value to key
    publicKey           *rsa.PublicKey  // public key for encrypting the symmetric key
    publicKeyFile       string          // file with the public key
    source              string          // source of the messages, kafka or random
    generateNewSymmetricKeyEvery int    // seconds between key generation
    verbose             bool            // verbose output
    logFileName         string          // log file name, redirect logging to a file from the console
    sendingThreads      []map[string]int // array of objects with thread names and offsets
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
    if (fileName == "") {
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
            if (value == "auto") {
                result.mtu = 0
            } else {
                tmp, err := strconv.Atoi(value)
                if (err != nil) {
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
            if (err != nil) {
                Logger.Fatalf("Error in config targetPort. Illegal value: %s. Legal values are 0-65535", value)
            } else {
                if (tmp < 0 || tmp > 65535) {
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
            if (value == "kafka" || value == "random") {
                result.source = value
            } else {
                Logger.Fatalf("Unknown source %s. Legal values are 'kafka' or 'random'.", value)
            }
            Logger.Printf("source: %s", value)
        case "verbose":
            tmp, err := strconv.ParseBool(value)
            if (err != nil) {
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
            if (err != nil) {
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
            if (err != nil) {
                Logger.Fatalf("Error in config generateNewSymmetricKeyEvery. Illegal value: %s. Legal values are a four byte integer", value)
            } else {
                if (tmp < 2) {
                    Logger.Fatalf("Error in config generateNewSymmetricKeyEvery. Illegal value: %s. Legal values are integer >= 2", value)
                } else {
                    result.generateNewSymmetricKeyEvery = tmp
                    Logger.Printf("generateNewSymmetricKeyEvery: %d", tmp)                
                }
            }
        case "sendingThreads":
            if err := json.Unmarshal([]byte(value), &result.sendingThreads); err != nil {
                Logger.Fatalf("Error in config sendingThreads. Illegal value: %s. Legal values are an array of objects", value)
            }
            Logger.Printf("sendingThreads: %v", result.sendingThreads)
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
    Logger.Print("Overriding configuration with environment variables...")

    prefix := "AIRGAP_UPSTREAM_";
    if id := os.Getenv(prefix + "ID"); id != "" {
        config.id = id
    }

    if nic := os.Getenv(prefix + "NIC"); nic != "" {
        config.nic = nic
    }

    if targetIP := os.Getenv(prefix + "TARGET_IP"); targetIP != "" {
        config.targetIP = targetIP
    }

    if targetPort := os.Getenv(prefix + "TARGET_PORT"); targetPort != "" {
        if port, err := strconv.Atoi(targetPort); err == nil {
            config.targetPort = port
        }
    }
    if bootstrapServers := os.Getenv(prefix + "BOOTSTRAP_SERVERS"); bootstrapServers != "" {
        config.bootstrapServers = bootstrapServers
    }
    if topic := os.Getenv(prefix + "TOPIC"); topic != "" {
        config.topic = topic
    }
    if groupID := os.Getenv(prefix + "GROUP_ID"); groupID != "" {
        config.groupID = groupID
    }
    if mtu := os.Getenv(prefix + "MTU"); mtu != "" {
        if mtu == "auto" {
            config.mtu = 0
        } else if mtuInt, err := strconv.Atoi(mtu); err == nil {
            config.mtu = uint16(mtuInt)
        }
    }
    if from := os.Getenv(prefix + "FROM"); from != "" {
        config.from = from
    }
    if encryption := os.Getenv(prefix + "ENCRYPTION"); encryption != "" {
        config.encryption = encryption == "true"
    }
    if publicKeyFile := os.Getenv(prefix + "PUBLIC_KEY_FILE"); publicKeyFile != "" {
        config.publicKeyFile = publicKeyFile
    }
    if generateNewSymmetricKeyEvery := os.Getenv(prefix + "GENERATE_NEW_SYMMETRIC_KEY_EVERY"); generateNewSymmetricKeyEvery != "" {
        if generateNewSymmetricKeyEveryInt, err := strconv.Atoi(generateNewSymmetricKeyEvery); err == nil {
            config.generateNewSymmetricKeyEvery = generateNewSymmetricKeyEveryInt
        }
    }
    if verbose := os.Getenv(prefix + "VERBOSE"); verbose != "" {
        config.verbose = verbose == "true"
    }
    if logFileName := os.Getenv(prefix + "LOG_FILE_NAME"); logFileName != "" {
        config.logFileName = logFileName
    }
    if sendingThreads := os.Getenv(prefix + "SENDING_THREADS"); sendingThreads != "" {
        if err := json.Unmarshal([]byte(sendingThreads), &config.sendingThreads); err != nil {
            Logger.Println("Error parsing SENDING_THREADS:", err)
        }
    }
    return config
}

// Check the configuration. On fail, will terminate the application
func checkConfiguration (result TransferConfiguration) {
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
    if (result.targetIP == "") {
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
    }
    if result.generateNewSymmetricKeyEvery < 2 {
        Logger.Fatalf("Error in config generateNewSymmetricKeyEvery. Illegal value: %d. Legal values are integer >= 2", result.generateNewSymmetricKeyEvery)
    }
    if len(result.sendingThreads) == 0 {
        Logger.Fatalf("Error in config sendingThreads. Illegal value: %v. Legal values are an array of objects", result.sendingThreads)
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
}

// This is called for every message read from Kafka, except
// if the from parameter (start time for sending) is set. Then 
// only events that entered Kafka after the from parameter generates a call
// The id is the kafka topic name _ partition id _ position
// The key is the value of the event key
// In upstream, we need the id, not the key
func handleKafkaMessage(id string, key []byte, _ time.Time, received []byte) bool {
    // ip:port
    address := fmt.Sprintf("%s:%d", config.targetIP, config.targetPort)
    var message [][]byte
    var err error
    if config.encryption {
        // Encrypt and send
        var ciphertext []byte
        ciphertext, err = protocol.Encrypt(received, config.key)
        message = protocol.FormatMessage(protocol.TYPE_MESSAGE, id, ciphertext, config.mtu)
    } else {
        // Send in clear text
        message = protocol.FormatMessage(protocol.TYPE_CLEARTEXT, id, received, config.mtu)
    }

    if(config.verbose) {
        Logger.Println(id + " - " + string(received))
    }

    if err != nil {
        Logger.Println(err)
    }
    for _, bytes := range message {
        udp.SendMessage(bytes, address)
    }
    // Maybe generate a new key?
    if config.generateNewSymmetricKeyEvery > 0 {
        if (time.Now().After(nextKeyGeneration)) {
            if (config.publicKeyFile != "") {
                sendNewKey(address)
            }
        }
    }
    return keepRunning
}

// Test usage only
// Send random messages instead of reading from Kafka. Use the 
// handleKafkaMessage function to send the messages as if they were
// read from Kafka. The id is the kafka topic name _ partition id _ position
// The key is the value of the event key
// Set source=random in the property file to run this code.
// Adjust max and mayby time.Sleep below to fit your needs
func generateRandom(_ string, topic string, groupID string, _ string) {
    max := 3
    startTime := time.Now() // Get the current time at the start of the function

    for i := 2; i <= max; i++ {
        id := topic + "_" + groupID + "_" + strconv.Itoa(i)
        // Create a random string of printable characters
        random := ""
        for j := 0; j < 500; j++ {
            n, _ := rand.Int(rand.Reader, big.NewInt(26))
            random += string('a' + rune(n.Int64()))
        }
        random += " " + id
        handleKafkaMessage(id, nil, time.Now(), []byte(random))
        time.Sleep(1 * time.Second)
    }
    id := topic + "_" + groupID + "_" + fmt.Sprint(max + 1)
    shutdownMessage := fmt.Sprintf("%s Upstream %s shutting down...", protocol.GetTimestamp(), config.id)
    handleKafkaMessage(id, nil, time.Now(), []byte(shutdownMessage))
    elapsedTime := time.Since(startTime) // Calculate the elapsed time
    fmt.Printf("generateRandom took %s\n", elapsedTime)

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
func sendNewKey(address string) {
    byteKey := generateNewKey()
    guard := "KEY_UPDATE#"
    toSend := []byte(guard)
    toSend = append(toSend, byteKey...)
    
    Logger.Printf("Sending key exchange message...")
    messages := protocol.FormatMessage(protocol.TYPE_KEY_EXCHANGE, "KEY_UPDATE#", toSend, config.mtu);
    for i := range messages {
        message := messages[i]
        udpErr := udp.SendMessage(message, address)
        if udpErr != nil {
            // Should we send error message or just die here?
            // TODO://check this later
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
    fileName := ""
    if len(os.Args) > 2 {
        Logger.Fatal("Too many command line parameters. Only one parameter is allowed: the configuration file.")
    }
    if len(os.Args) == 2 {
        fileName = os.Args[1]
    }
    
    // Start with the default parameters
    configuration := defaultConfiguration()
    // Read configuration from file, if added
    configuration, err := readParameters(fileName, configuration)
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
    if (config.logFileName != "") {
        Logger.Println("Configuring log to: " + config.logFileName)
        err := logfile.SetLogFile(config.logFileName, Logger)
        if err != nil {
            Logger.Fatal(err)
        }
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

    messages := protocol.FormatMessage(protocol.TYPE_STATUS, "STATUS", 
        []byte(fmt.Sprintf("%s %s starting up", protocol.GetTimestamp(), config.id)), config.mtu);
    if (len(messages) == 1) {
        udpErr := udp.SendMessage(messages[0], address)
        if udpErr != nil {
            Logger.Fatalf("Error: %v", udpErr)
        }
    }

    // Create initial key if the config contains a publicKeyFile
    if config.publicKeyFile != "" {
        Logger.Printf("Creating initial key with public key file: %s", config.publicKeyFile)
        sendNewKey(address);
    } else {
        Logger.Printf("No public key file specified, encryption will not be used.")
    }

    // Now, read from Kafka and call our handler for each message, or generate random messages for test purposes
    if (config.source == "kafka") {
        Logger.Printf("Reading from Kafka %s", config.bootstrapServers)

        // Spawn one thread for each entry in config.sendingThreads
        for _, thread := range config.sendingThreads {
            go func(thread map[string]int) {
                // Get the thread name and offset
                for name, offset := range thread {
                    // use name and offset here
                    // Start the thread
                    Logger.Printf("Starting sending thread: %s (offset: %d)", name, offset)
                    group := fmt.Sprintf("%s-%s", config.groupID, name)
                    kafka.ReadFromKafka(name, offset, config.bootstrapServers, config.topic, group, config.from, handleKafkaMessage)
                }
            }(thread)
        }

    } else {
        Logger.Printf("Reading from random source")
        // Now, read from Kafka and call our handler for each message:
        generateRandom(config.bootstrapServers, config.topic, config.groupID, config.from)

    }

    // Wait for SIGTERM or SIGINT, then send shutdown message
    sigterm := make(chan os.Signal, 1)
    signal.Notify(sigterm, syscall.SIGINT, syscall.SIGTERM)
    <-sigterm

    // SIGTERM received, send a message to the downstream that we are shutting down
    messages = protocol.FormatMessage(protocol.TYPE_STATUS, 
        "STATUS", 
        []byte(fmt.Sprintf("%s %s terminating by signal", protocol.GetTimestamp(), config.id)), config.mtu);
    udp.SendMessage(messages[0], address)
    // Wait a while for the messages to be sent
    time.Sleep(100 * time.Millisecond)
}

