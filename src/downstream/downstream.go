package main

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/IBM/sarama"
	"github.com/google/uuid"
	"sitia.nu/airgap/src/kafka"
	"sitia.nu/airgap/src/mtu"
	"sitia.nu/airgap/src/protocol"
	"sitia.nu/airgap/src/udp"
    "sitia.nu/airgap/src/logfile"
)

// A private key, the filename and the hash of the file
// If a file is removed in the OS, it will be removed from
// the list of keys. If it is changed, the new key will be
// loaded and if it's added in the OS, it will be added to
// the list of keys.
type KeyInfo struct {
    privateKey          *rsa.PrivateKey
    privateKeyFilename  string
    privateKeyHash      string
}

// Mimics the configuration property file
type TransferConfiguration struct {
    id                  string  // unique id for this downstrem
	nic                 string
    targetIP            string
    targetPort          int
    bootstrapServers    string
    topic               string
    clientID            string
    mtu                 uint16
    from                string
    key                 []byte // symmetric
    keyInfos            []KeyInfo // array of private keys
    privateKeyGlob      string
    producer            sarama.AsyncProducer // kafka
    target              string
    verbose             bool
    logFileName         string
}
var config TransferConfiguration
var Logger = log.New(os.Stdout, "", log.LstdFlags)

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
        sendMessage(protocol.TYPE_ERROR, "", config.topic, []byte(message))
    }

    // Purge from memory if not on disk
    // First, iterate over the array and save an array of indicies that should be removed
    purgeIndices := []int{}
    for j := 0; j < len(config.keyInfos); j++ {
        found := false
        for _, fileName := range fileNames {
            if (config.keyInfos[j].privateKeyFilename == fileName) {
                found = true
            }
        }
        if (!found) {
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
            sendMessage(protocol.TYPE_ERROR, "", config.topic, []byte(fmt.Sprintf("Can't read private key from file %s, %v", fileName, err)))
        }
        defer file.Close()
    
        var builder strings.Builder

        // Read the key as a text file
        // First, get the base64 encoded data
        scanner := bufio.NewScanner(file)
        var inFile = false
        for scanner.Scan() {
            line := scanner.Text()
            if (strings.HasPrefix(line, "-----")) {
                inFile = !inFile
            } else if (inFile) {
                builder.WriteString(line)
            }
        }
    
        // Now add to a variable
        b64Data := builder.String()
    
        // Decode the base64 encoded variable
        derData, err := base64.StdEncoding.DecodeString(b64Data)
        if err != nil {
            sendMessage(protocol.TYPE_ERROR, "", config.topic, []byte(fmt.Sprintf("Error decoding base64 data: %v", err)))
        }
        // hash the key so we can see if it's loaded already
        derString := fmt.Sprintf("%x", sha256.Sum256(derData))

        // Check if we have the key loaded already
        addKey := true
        for j := 0; j < len(config.keyInfos); j++ {
            if (config.keyInfos[j].privateKeyHash == derString) {
                sendMessage(protocol.TYPE_ERROR, "", config.topic, []byte(fmt.Sprintf("Key file %s already loaded", config.keyInfos[j].privateKeyFilename)))
                addKey = false
            }
        }

        // Parse the DER encoded private key
        key, err := x509.ParsePKCS8PrivateKey(derData)
        if err != nil {
            sendMessage(protocol.TYPE_ERROR, "", config.topic, []byte(fmt.Sprintf("Error parsing private key: %v", err)))
        }
    
        privateKey, ok := key.(*rsa.PrivateKey)
        if !ok {
            sendMessage(protocol.TYPE_ERROR, "", config.topic, []byte("Private key is not of type *rsa.PrivateKey"))
        }
    
        if (addKey) {
            keyInfo := KeyInfo{
                privateKey:          privateKey,
                privateKeyFilename:  fileName,
                privateKeyHash:      derString,
            }
            config.keyInfos = append(config.keyInfos, keyInfo)
            sendMessage(protocol.TYPE_STATUS, "", config.topic, []byte(fmt.Sprintf("Successfully loaded key file: %s", fileName)))
        }
    }
    return config.keyInfos;
}

// Read the configuration file and return the configuration
func readParameters(fileName string) (TransferConfiguration, error) {
    file, err := os.Open(fileName)
    if err != nil {
        return TransferConfiguration{}, err
    }
    defer file.Close()

    result := TransferConfiguration{}
    scanner := bufio.NewScanner(file)
    for scanner.Scan() {
        line := scanner.Text()
        parts := strings.SplitN(line, "=", 2)
        if len(parts) != 2 {
            continue
        }
        key := strings.TrimSpace(parts[0])
        value := strings.TrimSpace(parts[1])
        result.target = "kafka"
        result.verbose = false

        switch key {
        case "id": 
            Logger.Printf("id: %s", value)
            result.id = value
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
            if (err != nil) {
                Logger.Fatalf("Error in config targetPort. Ilegal value: %s. Legal values are 0-65535", value)
            } else {
                if (tmp < 0) {
                    Logger.Fatalf("Error in config targetPort. Ilegal value: %s. Legal values are 0-65535", value)
                } else if (tmp > 65535) {
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
        case "verbose":
            tmp, err := strconv.ParseBool(value)
            if err != nil {
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
        case "target": // optional
            if ("kafka" == value || "cmd" == value) {
                result.target = value
                Logger.Printf("target: %s", value)
            } else {
                Logger.Fatalf("Unknown target %s", value)
            }
        }
    }

    if err := scanner.Err(); err != nil {
        return TransferConfiguration{}, err
    }

    return result, nil
}

// To be able to assemble fragmented events
// The cache is used to store the fragments until it
// is assembled into a complete event
// Old events are removed from the cache by another thread
var cache = protocol.CreateMessageCache()

// Send a message to Kafka or stdout
func sendMessage(messageType uint8, id string, topic string, message []byte) {
    // For extra printouts, change this:
    verbose := true
    // If no id, just create one
    var messageKey string
    if (id == "") {
        messageKey = uuid.New().String()
    } else {
        messageKey = id
    }
    if verbose {
        Logger.Printf("id %s", id)
        Logger.Printf("%s Sending cleartext message to %s: %s ", config.id, config.target, string(message))        
    }
    // If this is an error message, prepend a timestamp
    var toSend []byte = message
    if (messageType == protocol.TYPE_ERROR || messageType == protocol.TYPE_STATUS) {
        toSend = []byte(fmt.Sprintf("%s %s %s", protocol.GetTimestamp(), config.id, string(message)))
        Logger.Printf(string(toSend))
    }

    // Send the data to Kafka
    if ("kafka" == config.target) {
        // Send the result to Kafka
        kafka.WriteToKafka(messageKey, config.topic, toSend)
    } else {
        // send to stdout
        os.Stdout.Write(toSend)
        os.Stdout.Write([]byte("\n"))
    }
}

// This is called for every message read from UDP
func handleUdpMessage(receivedBytes []byte) {
    // Try our format
    var messageType uint8
    messageType, messageId, payload, ok := protocol.ParseMessage(receivedBytes, cache)
    fmt.Printf("MessageType %d, messageId %s\n", messageType, messageId)
    nrErrorMessages := 0
    errorMessageLastTime := time.Now()
    errorMessageEvery := 60 * time.Second
    if ok != nil {
        // Error
        Logger.Fatalf("Error parsing message %s, %v\n", receivedBytes, ok)
    } else {
        if (messageType == protocol.TYPE_KEY_EXCHANGE) {
            // Get the new key from the message
            keyFileNameUsed := readNewKey(payload)
            // and send a key-change log event to Kafka
            message := []byte(fmt.Sprintf("Updating symmetric key with private key file: %s", keyFileNameUsed))
            Logger.Printf(string(message))
            sendMessage(protocol.TYPE_STATUS, "", config.topic, message)
        } else if (messageType == protocol.TYPE_MESSAGE) {
            // Decrypt the message
            decrypted, err := protocol.Decrypt(payload, config.key)
            if (err != nil) {
                // Error decrypting message. Always send the error message to Kafka
                message := []byte(fmt.Sprintf("ERROR decrypting message: %s", err))
                sendMessage(protocol.TYPE_ERROR, messageId, config.topic, message)
            } else {
                // Decrypted message ok
                sendMessage(protocol.TYPE_MESSAGE, messageId, config.topic, decrypted)
            }
        } else if (messageType == protocol.TYPE_ERROR) {
            if (nrErrorMessages == 0) {
                // Always send first time an error occurs
                message := []byte(fmt.Sprintf("ERROR message received: %s", payload))
                sendMessage(protocol.TYPE_ERROR, messageId, config.topic, message)
                nrErrorMessages += 1
            } else {
                if (time.Now().After(errorMessageLastTime.Add(errorMessageEvery))) {
                    // Send error messages periodically
                    message := []byte(fmt.Sprintf("ERROR messages received: %d, last received: %s", 
                            nrErrorMessages,
                            payload))
                    sendMessage(protocol.TYPE_ERROR, messageId, config.topic, message)
                    errorMessageLastTime = time.Now()
                    nrErrorMessages = 0 // reset the message counter
                } else {
                    nrErrorMessages += 1
                }
            }
        } else if (messageType == protocol.TYPE_MULTIPART) {
            // Do nothing. Wait for the last fragment
        } else {
            // Just send the text to Kafka with an empty ID (will create a new ID)
            sendMessage(protocol.TYPE_MESSAGE, "", config.topic, payload)
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
    payload := message [11:]

    // Just to be sure, we read the private keys again
    // since they can have changed on disk
    readPrivateKeys(config.privateKeyGlob)

    // Decrypt the symmetric key with the private keys until we get the message: 
    // KEY_UPDATE# as the first 11 bytes
    Logger.Print("Checking " + fmt.Sprint(len(config.keyInfos)) + " keys for new symmetric key")
    for i := range config.keyInfos {
        Logger.Print("Checking key " + fmt.Sprint(i))
        privateKey := config.keyInfos[i].privateKey
        decrypted, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, privateKey, payload, nil)
        if err == nil {
            if string(decrypted[0:11]) == "KEY_UPDATE#" {
                // We got the correct key
                sendMessage(protocol.TYPE_STATUS, "", config.topic, []byte(fmt.Sprintf("Got new symmetric key: %s", config.keyInfos[i].privateKeyFilename)))
                config.key = decrypted[11:]
                return config.keyInfos[i].privateKeyFilename
            }
        }
    }
    sendMessage(protocol.TYPE_ERROR, "", config.topic, []byte(fmt.Sprintf("Can't decrypt the new symmetric key. Tried all %v private keys\n", len(config.keyInfos))))
    return "ERROR: No key found that can decrypt the new symmetric key"
}

// main is the entry point of the program.
// It reads a configuration file from the command line parameter,
// initializes the necessary variables, and starts the upstream process.
func main() {
    if len(os.Args) < 2 {
        Logger.Fatal("Missing command line parameter (configuration file)\n")
    }
    fileName := os.Args[1]
    
    configuration, err := readParameters(fileName)
    if err != nil {
        Logger.Fatalf("Error reading configuration file %s: %v\n", fileName, err)
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
	// and be able to read the MTU for that connection.

    if config.mtu == 0 {
        mtuValue, err := mtu.GetMTU(config.nic, address)
        if err != nil {
            Logger.Fatal(err)
        }    
        config.mtu = uint16(mtuValue)
    }
    Logger.Printf("MTU set to: %d\n", config.mtu)

    // Load our private key
    Logger.Printf("Loading private keys from files: %s\n", config.privateKeyGlob)
    readPrivateKeys(config.privateKeyGlob)
    
    // Create a new async producer
    if ("kafka" == config.target) {
        Logger.Printf("Connecting to %s\n", config.bootstrapServers)
        conf := sarama.NewConfig()
        conf.Producer.RequiredAcks = sarama.WaitForLocal
        conf.Producer.Return.Successes = true
        conf.ClientID = config.clientID
        producer, err := sarama.NewAsyncProducer(strings.Split(config.bootstrapServers, ","), conf)
        if err != nil {
            Logger.Panicf("Error creating producer: %v\n", err)
        }
        defer func() {
            if err := producer.Close(); err != nil {
                Logger.Panicf("Error closing async producer: %v", err)
            }
        }()
        go func() {
            for err := range producer.Errors() {
                Logger.Println("Failed to send message:", err)
            }
        }()
        go func() {
            for msg := range producer.Successes() {
                if config.verbose {
                    Logger.Println("Message sent:", msg)
                }
            }
        }()
        Logger.Printf("Connected to Kafka. Creating startup message...\n")
        config.producer = producer
        kafka.SetProducer(producer)
        // Start a new goroutine for sending kafka messages
        kafka.StartBackgroundThread()
    }
    
    // Send startup message with unique key
    sendMessage(protocol.TYPE_STATUS, "", config.topic, []byte("downstream starting up"))

    // Signal termination
    c := make(chan os.Signal)
    signal.Notify(c, os.Interrupt, syscall.SIGTERM)
    go func() {
        <-c
        // Send shutdown message to Kafka
        Logger.Printf("%s Sending shutdown message to Kafka\n", config.id)
        sendMessage(protocol.TYPE_STATUS, "", config.topic, []byte("terminating by signal"))
        time.Sleep(500 * time.Millisecond) // Give the Kafka message some time to be processed before terminating
        os.Exit(1)
    }()

    Logger.Printf("Starting UDP Server on %s:%d\n", config.targetIP, config.targetPort)
    // Now, read from UDP and call our handler for each message:
    udp.ListenUDP(config.targetIP, config.targetPort, handleUdpMessage, config.mtu);

}

