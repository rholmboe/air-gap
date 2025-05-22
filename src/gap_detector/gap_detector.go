package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"sitia.nu/airgap/src/gap_util"
	"sitia.nu/airgap/src/kafka"
	"sitia.nu/airgap/src/logfile"
	"sitia.nu/airgap/src/protocol"
	"sitia.nu/airgap/src/timestamp_util"
)

// Mimics the configuration property file
type TransferConfiguration struct {
	id                   string               // unique id when logging events
	bootstrapServers     string               // Kafka servers
	topic                string               // topic to read from
	topicOut             string               // topic to write to
	clientID             string               // Id used in kafka for both read and write
	producer             sarama.AsyncProducer // kafka
	verbose              bool                 // print debug messages
	sendGapsEverySeconds int                  // interval for sending gap messages
	gapMessageTemplate   string               // template for gap messages
	gapSaveFile          string               // file to save gap state to
	from                 string               // timestamp to start reading from
	logFileName          string               // log file name
}

var config TransferConfiguration
var keepRunning bool = true
var Logger = log.New(os.Stdout, "", log.LstdFlags)
var lock sync.RWMutex

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
		result.verbose = false
		result.gapMessageTemplate = "$time Total gaps: $total. Gaps: $gaps"

		switch key {
		case "id":
			Logger.Printf("id: %s", value)
			result.id = value
		case "bootstrapServers":
			result.bootstrapServers = value
			Logger.Printf("bootstrapServers: %s", value)
		case "from":
			result.from = value
			Logger.Printf("from: %s", value)
		case "topic":
			result.topic = value
			Logger.Printf("topic: %s", value)
		case "topicOut":
			result.topicOut = value
			Logger.Printf("topicOut: %s", value)
		case "logFileName":
			result.logFileName = value
			Logger.Printf("logFileName: %s", value)
		case "gapSaveFile":
			result.gapSaveFile = value
			Logger.Printf("gapSaveFile: %s", value)
		case "clientId":
			result.clientID = value
			Logger.Printf("clientId: %s", value)
		case "gapMessageTemplate":
			result.gapMessageTemplate = value
		case "sendGapsEverySeconds":
			tmp, err := strconv.Atoi(value)
			if err != nil {
				Logger.Fatalf("Error in config sendGapsEverySeconds. Ilegal value: %s. Legal values are numbers", value)
			} else {
				if tmp < 0 {
					Logger.Fatalf("Error in config sendGapsEverySeconds. Ilegal value: %s. Legal values are positive numbers", value)
				} else {
					result.sendGapsEverySeconds = tmp
				}
			}
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
		}

		if err := scanner.Err(); err != nil {
			return TransferConfiguration{}, err
		}
	}
	return result, nil
}

func sendMessage(id string, topic string, message []byte) {
	if config.verbose {
		Logger.Printf("id %s", id)
		Logger.Printf("%s Sending cleartext message: %s\n", config.id, string(message))
	}

	// Send the result to the Kafka queue
	//    var item = gap_util.Item{ // Use the fully qualified name of the Item struct
	//		ID:      id,
	//		Topic:   topic,
	//		Message: message,
	//	}

	kafka.WriteToKafka(id, topic, message)
}

// This is called for every message read from Kafka
// Return true except when we are shutting down
// False will force the reader to stop reading from Kafka
// The timestamp is the Kafka time of the read event
// Use that to be able to reposition the read if the application
// crashes. So, save the value in a variable and every time the
// gap state is stored to file, update the configuration from parameter
// The id is the kafka topic name _ partition id _ position
// The key is the value of the event key
// In GapDetector, we need the key, not the id
func handleKafkaMessage(unused string, key []byte, timeStamp time.Time, receivedBytes []byte) bool {
	lock.Lock()
	defer lock.Unlock()
	id := string(key)
	fmt.Printf("%s\n", key)
	if !keepRunning {
		Logger.Printf("keepRunning is false, handleKafkaMessage...")
	}
	if config.verbose {
		Logger.Printf("Message received %s %s", id, string(receivedBytes))
	}
	duplicate := false
	// Split the id into parts
	parts := strings.SplitN(id, "_", 3)
	if len(parts) != 3 {
		if config.verbose {
			Logger.Printf("Can't parse the id %s", id)
		}
	} else {
		// Dedup/Gap detection
		key := parts[0] + "_" + parts[1]
		number, err := strconv.ParseInt(parts[2], 10, 64)
		if err == nil {
			// parse ok, check
			duplicate = gap_util.CheckNextNumber(key, number)
		}
	}
	// write every message to topicOut, except duplicates
	if !duplicate {
		sendMessage(string(key), config.topicOut, receivedBytes)
	}
	// debug:
	//    time.Sleep(1 * time.Second)
	if !keepRunning {
		Logger.Printf("keepRunning is false, shutting down...")
	}
	gap_util.SetTimestamp(timeStamp)
	return keepRunning
}

func EmitGaps(fileName string, configFileName string) error {
	error := gap_util.Save(fileName, configFileName)
	if error != nil {
		message := fmt.Sprintf("%s Error saving state to file: %s", protocol.GetTimestamp(), error)
		sendMessage("",
			config.topicOut,
			[]byte(message))
		Logger.Print(message)
		return error
	}
	// Iterate over the gap entries and get the
	// smallest number for each key
	// array of topic_partition_row
	total, lowest := gap_util.GetFirstGaps()
	var builder strings.Builder
	delimiter := " "
	for i := range lowest {
		builder.WriteString(lowest[i])
		builder.WriteString(delimiter)
	}
	timestring := protocol.GetTimestamp()
	gapstring := builder.String()
	toSend := strings.Replace(
		config.gapMessageTemplate,
		"$time",
		timestring, 1)
	toSend = strings.Replace(
		toSend,
		"$gaps",
		gapstring,
		1)
	toSend = strings.Replace(
		toSend,
		"$total",
		fmt.Sprintf("%d", total),
		1)

	// Send gap messages with unique key
	sendMessage("",
		config.topicOut,
		[]byte(toSend))

	return nil
}

// Will emit log messages to Kafka an also
// save the gap state to fileName
func StartBackgroundThread(fileName string, configFileName string) {
	// Start a background thread
	ticker := time.NewTicker(time.Duration(config.sendGapsEverySeconds) * time.Second)
	go func() {
		for range ticker.C {
			EmitGaps(fileName, configFileName)
		}
	}()
}

func LoadGaps(fileName string) error {
	error := gap_util.Load(fileName)
	if error != nil {
		Logger.Printf("Error loading gaps from file %s", error)
		return error
	}
	return nil
}

// main is the entry point of the program.
// It reads a configuration file from the command line parameter,
// initializes the necessary variables, and starts the gap detection process.
// Gap detection is done by reading from a Kafka topic and checking the
// sequence of numbers in the messages. If a number is missing, a gap is detected.
// Output is formatted for the set-timestamp utility to consume upstream
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
	if config.logFileName != "" {
		err := logfile.SetLogFile(config.logFileName, Logger)
		if err != nil {
			Logger.Fatal(err)
		}
		Logger.Println("Log to file started up")
		gap_util.SetLogger(Logger)
	}

	// Create a new async producer
	Logger.Printf("Connecting to %s\n", config.bootstrapServers)
	conf := sarama.NewConfig()
	conf.Producer.RequiredAcks = sarama.WaitForLocal
	conf.Producer.Return.Successes = true
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

	Logger.Printf("Loading gap file: %s", config.gapSaveFile)
	error := gap_util.Load(config.gapSaveFile)
	if error != nil {
		Logger.Fatalf("FATAL: Cannot load gaps from file %s %s", config.gapSaveFile, error)
	}

	// Send startup message with unique key
	sendMessage("", config.topicOut, []byte(fmt.Sprintf("%s INFO: %s starting up reading from topic: %s",
		protocol.GetTimestamp(), config.id, config.topic)))

	// Initialize the background thread that sends messages
	// about the first not received event for each
	// topic_partitionId (if any).
	// The interval is configurable.
	StartBackgroundThread(config.gapSaveFile, fileName)

	Logger.Printf("Starting GapDetector Handler on: %s, topic: %s\n", config.bootstrapServers, config.topic)

	// Restore from crash
	if config.from != "" {
		msg := fmt.Sprintf("%s ERROR: %s Restoring from crashed state. There may be duplicates from: %s to: %s",
			protocol.GetTimestamp(),
			config.id,
			config.from, protocol.GetTimestamp())

		Logger.Printf(msg)
		sendMessage("", config.topicOut, []byte(msg))

	}

	// Reset the from parameter so that it's not used on the next start
	// Get an updated config
	err2 := timestamp_util.SaveTimestampInConfig(fileName, "")
	if err2 != nil {
		Logger.Fatalf("Error updating the config with the new from value '', err: %s", err)
	}

	// Now, read from Kafka and call our handler for each message:
	kafka.ReadFromKafka(config.bootstrapServers, config.topic, config.clientID, config.from, handleKafkaMessage)
	// The above line will keep processing messages until a signal is received, when we will proceed

	Logger.Printf("Shutting down gracefully. Please wait...")
	// Give the Kafka writer some time to catch up
	time.Sleep(1 * time.Second)
	// Send shutdown message to Kafka
	Logger.Printf("%s Sending shutdown message to Kafka\n", config.id)
	sendMessage("", config.topicOut, []byte(fmt.Sprintf("%s INFO: %s terminating by signal",
		protocol.GetTimestamp(),
		config.id)))

	// Send gap information to Kafka
	Logger.Printf("Sending Gap information to Kafka\n")
	EmitGaps(config.gapSaveFile, fileName)

	// Let the producer shut down gracefully
	time.Sleep(1 * time.Second)

	// Clean shutdown. We don't need to save the timestamp here
	Logger.Printf("Removing saved timestamp since we managed a clean shutdown")
	timestamp_util.SaveTimestampInConfig(fileName, "")

	Logger.Printf("Closing application")
}
