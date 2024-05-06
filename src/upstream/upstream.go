package main

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"sitia.nu/airgap/src/kafka"
	"sitia.nu/airgap/src/logfile"
	"sitia.nu/airgap/src/mtu"
	"sitia.nu/airgap/src/protocol"
	"sitia.nu/airgap/src/timestamp_util"
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
    key                 []byte          // symmetric key in use
    newkey              []byte          // a new symmetric key, when successfully sent, copy the value to key
    publicKey           *rsa.PublicKey  // public key for encrypting the symmetric key
    publicKeyFile       string          // file with the public key
    source              string          // source of the messages, kafka or random
    generateNewSymmetricKeyEvery int    // seconds between key generation
    verbose             bool            // verbose output
    logFileName         string          // log file name, redirect logging to a file from the console
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

// Parse the configuration file and return a TransferConfiguration struct.
func readParameters(fileName string) (TransferConfiguration, error) {
    file, err := os.Open(fileName)
    if err != nil {
        return TransferConfiguration{}, err
    }
    defer file.Close()

    result := TransferConfiguration{}
    scanner := bufio.NewScanner(file)
    config.verbose = false
    config.logFileName = ""
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
            if ("auto" == value) {
                result.mtu = 0
            } else {
                tmp, err := strconv.Atoi(value)
                if (err != nil) {
                    Logger.Fatalf("Error in config mtu. Ilegal value: %s. Legal values are 'auto' or a two byte integer", value)
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
                Logger.Fatalf("Error in config targetPort. Ilegal value: %s. Legal values are 0-65535", value)
            } else {
                if (tmp < 0 || tmp > 65535) {
                    Logger.Fatalf("Error in config targetPort. Ilegal value: %s. Legal values are 0-65535", value)
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
            if ("kafka" == value || "random" == value) {
                result.source = value
            } else {
                Logger.Fatalf("Unknown source %s. Legal values are 'kafka' or 'random'.", value)
            }
            Logger.Printf("source: %s", value)
        case "verbose":
            tmp, err := strconv.ParseBool(value)
            if (err != nil) {
                Logger.Fatalf("Error in config verbose. Ilegal value: %s. Legal values are true or false", value)
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
        case "generateNewSymmetricKeyEvery": // second
            tmp, err := strconv.Atoi(value)
            if (err != nil) {
                Logger.Fatalf("Error in config generateNewSymmetricKeyEvery. Ilegal value: %s. Legal values are a four byte integer", value)
            } else {
                if (tmp < 2) {
                    Logger.Fatalf("Error in config generateNewSymmetricKeyEvery. Ilegal value: %s. Legal values are integer >= 2", value)
                } else {
                    result.generateNewSymmetricKeyEvery = tmp
                    Logger.Printf("generateNewSymmetricKeyEvery: %d", tmp)                
                }
            }
        }
    }

    if err := scanner.Err(); err != nil {
        return TransferConfiguration{}, err
    }

    return result, nil
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
    ciphertext, err := protocol.Encrypt(received, config.key)

    message := protocol.FormatMessage(protocol.TYPE_MESSAGE, id, ciphertext, config.mtu)
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
            sendNewKey(address)
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
func generateRandom(bootstrapServers string, topic string, groupID string, from string) {
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
    if len(os.Args) < 2 {
        Logger.Fatal("Missing command line parameter (configuration file)")
    }
    fileName := os.Args[1]
    
    configuration, err := readParameters(fileName)
    if err != nil {
        Logger.Fatalf("Error reading configuration file %s: %v", fileName, err)
    }
    // Add to package variable
    config = configuration

    // Set the log file name
    if (config.logFileName != "") {
        err := logfile.SetLogFile(config.logFileName, Logger)
        if err != nil {
            Logger.Fatal(err)
        }
        Logger.Println("Log to file started up")
        kafka.SetLogger(Logger)
    } 

    // ip:port
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

    // Create initial key
    sendNewKey(address);

    // Reset the from parameter so that it's not used on the next start
    // Get an updated config
    Logger.Printf("Using from=%s", config.from)
    newConfig, err := timestamp_util.UpdateTimeParameter(fileName, "")
    if (err == nil) {
        err2 := timestamp_util.WriteResultToFile(fileName, newConfig)
        if (err2 == nil) {
            Logger.Printf("Successfully updated %s with the value from=", fileName)
        } else {
            Logger.Fatalf("Error updating the file %s, err: %s", fileName, err2)
        }
    } else {
        Logger.Fatalf("Error updating the config with the new from value '', err: %s", err)
    }


    // Now, read from Kafka and call our handler for each message, or generate random messages for test purposes
    if (config.source == "kafka") {
        Logger.Printf("Reading from Kafka %s", config.bootstrapServers)
        // Now, read from Kafka and call our handler for each message:
        kafka.ReadFromKafka(config.bootstrapServers, config.topic, config.groupID, config.from, handleKafkaMessage)
    } else {
        Logger.Printf("Reading from random source")
        // Now, read from Kafka and call our handler for each message:
        generateRandom(config.bootstrapServers, config.topic, config.groupID, config.from)

    }

    // SIGTERM received, send a message to the downstream that we are shutting down
    messages = protocol.FormatMessage(protocol.TYPE_STATUS, 
        "STATUS", 
        []byte(fmt.Sprintf("%s %s terminating by signal", protocol.GetTimestamp(), config.id)), config.mtu);
    udp.SendMessage(messages[0], address)
    // Wait a while for the messages to be sent
    time.Sleep(100 * time.Millisecond)
}

