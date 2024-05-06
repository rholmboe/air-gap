package main

import (
	"bufio"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"sitia.nu/airgap/src/timestamp_util"
)

// Mimics the configuration property file
type TransferConfiguration struct {
    bootstrapServers    string
    producer            sarama.AsyncProducer // kafka
    verbose             bool
}
var config TransferConfiguration


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

        switch key {
        case "bootstrapServers":
            result.bootstrapServers = value
            log.Printf("bootstrapServers: %s", value)
        case "verbose":
            tmp, err := strconv.ParseBool(value)
            if err != nil {
                log.Fatalf("Error in config verbose. Ilegal value: %s. Legal values are true or false", value)
            } else {
                result.verbose = tmp
            }
            var verboseStr string
            if result.verbose {
                verboseStr = "true"
            } else {
                verboseStr = "false"
            }
            log.Printf("verbose: %s", verboseStr)
        }
        if err := scanner.Err(); err != nil {
            return TransferConfiguration{}, err
        }
    }
    return result, nil
}

// Given a topic name, a partition id and a row number, get the timestamp in Kafka when that
// event was persisted
func getTimestampFromKafka(topicName string, partitionId int32, offset int64) (time.Time, error) {
    brokers := strings.Split(config.bootstrapServers, ",")
    log.Printf("Topic: %s, partitionId: %d, offset %d", topicName, partitionId, offset)

    config := sarama.NewConfig()
    config.Consumer.Return.Errors = true

    client, err := sarama.NewClient(brokers, config)
    if err != nil {
        log.Println("Failed to start client:", err)
        return time.Time{}, err
    }
    log.Println("Client started...")
    consumer, err := sarama.NewConsumerFromClient(client)
    if err != nil {
        log.Println("Failed to start consumer:", err)
        return time.Time{}, err
    }
    log.Println("Consumer created...")

    defer func() {
        if err := consumer.Close(); err != nil {
            log.Fatalln(err)
        }
    }()

    partitionConsumer, err := consumer.ConsumePartition(topicName, partitionId, offset)
    if err != nil {
        log.Fatalf("Failed to start partition consumer: %s", err)
    }
    log.Println("PartitionConsumer created...")

    defer func() {
        if err := partitionConsumer.Close(); err != nil {
            log.Fatalln(err)
        }
    }()
    
    // Trap SIGINT to trigger a shutdown.
    signals := make(chan os.Signal, 1)
    signal.Notify(signals, os.Interrupt)
    
    ConsumerLoop:
    for {
        select {
        case msg := <-partitionConsumer.Messages():
            log.Printf("Consumed message offset %d\n", msg.Offset)
            log.Printf("Timestamp %s", msg.Timestamp)
            return msg.Timestamp, nil
        case <-signals:
            break ConsumerLoop
        }
    }
    return time.Time{}, err
}

// Given a list of ids, return the earliest timestamp of the events
// ID:s are formatted as {topic_name}_{partition_id}_{row_number}
// So first extract the topic_name, partition_id and row_number
func getMinTimestamp(ids []string) string {
    timestamps := []time.Time{}
    for i := range ids {
        id := ids[i]
        parts := strings.Split(id, "_")
        if (len(parts) != 3) {
            log.Fatalf("Illegal number of parts in id: %s. Got %d parts (split by _) but expected 3",
            id, len(parts))
        }
        topicName := parts[0]
        // 32 byte int
        tmp, err := strconv.ParseInt(parts[1], 10, 64)
        if err != nil {
            log.Fatalf("Can't convert %s to a number", parts[1])
        }
        partitionId := int32(tmp)
        // 64 byte int
        tmp, err = strconv.ParseInt(parts[2], 10, 64)
        if err != nil {
            log.Fatalf("Can't convert %s to a number", parts[2])
        }
        rowNumber := tmp
        log.Printf("%s_%d_%d", topicName, partitionId, rowNumber)
        timestamp, err := getTimestampFromKafka(topicName, partitionId, rowNumber)
        if (err != nil) {
            log.Fatalf("Error getting timestamp for id: %s. Error: %s", id, err)
        } else {
            timestamps = append(timestamps, timestamp)
        }
    }
    // Select the earliest timestamp from the timestamps array:
    if len(timestamps) == 0 {
        log.Fatalf("No timestamps to returned")
    }
    
    minTimestamp := timestamps[0]
    for _, timestamp := range timestamps {
        if timestamp.Before(minTimestamp) {
            minTimestamp = timestamp
        }
    }
    
    return minTimestamp.Format(time.RFC3339)
}

// main is the entry point of the program.
// It reads a configuration file from the command line parameter,
// and updates the from line to the earliest time calculated
// The times are obtained by using all the remaining parameters.
// Each parameter after the first (fileName) should be formatted as
// {topic_name}_{partition_id}_{row_number}, e.g., test_1_344443
// For each of those parameters, split them into topic_name, partition_id and 
// row_number. Query the kafka (from the configuration) for the timestamp
// of the event with those parameters and sort our the earliest of those
// events. Format that timestamp as a ISO 8601 timestamp and update the
// from parameter in the configuration file. Save the file.
func main() {
    if len(os.Args) < 3 {
        log.Fatal("Parameters: (configuration file) and one or more (topic_partition_row) ")
    }
    fileName := os.Args[1]
    
    configuration, err := readParameters(fileName)
    if err != nil {
        log.Fatalf("Error reading configuration file %s: %v", fileName, err)
    }
    // Add to package variable
    config = configuration

    minTimestamp := getMinTimestamp(os.Args[2:])

    // Get an updated config
    newConfig, err := timestamp_util.UpdateTimeParameter(fileName, minTimestamp)
    if (err == nil) {
        err2 := timestamp_util.WriteResultToFile(fileName, newConfig)
        if (err2 == nil) {
            log.Printf("Successfully updated %s with the value from=%s", fileName, minTimestamp)
        } else {
            log.Fatalf("Error updating the file %s, err: %s", fileName, err2)
        }
    } else {
        log.Fatalf("Error updating the config with the new from value '%s', err: %s", minTimestamp, err)
    }

}

