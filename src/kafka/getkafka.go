package kafka

// SIGUSR1 toggle the pause/resume consumption
import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/IBM/sarama"
	"sitia.nu/airgap/src/protocol"
)

// Sarama configuration options
var (
	version  = sarama.DefaultVersion.String()
	assignor = "roundrobin"
	oldest   = true
	verbose  = false
	from     = time.Date(1970, 1, 1, 1, 0, 0, 0, time.Local)  // Set to the beginning of time
	callback func(string, []byte, time.Time, []byte) bool // Assign a nil value to the callback variable
)

func ReadFromKafka(brokers string, topics string, group string, timestamp string, callbackFunction func(string, []byte, time.Time, []byte) bool) {
	callback = callbackFunction
	keepRunning := true
	Logger.Println("Starting a new Sarama consumer")

	if (timestamp != "") {
		// Don't change the time string, it's a standard format for time parsing! Check the go documentation
		fromTime, err := time.Parse("2006-01-02T15:04:05-07:00", timestamp)
		if err != nil {
			Logger.Panicf("Error parsing from time: %v", err)
		}
		// Now, copy to the package variable. Used in ConsumeClaim
		from = fromTime
		// Also, force zookeeper to start from the beginning
		// Use a new Id (do not use the from timestamp since we then can't use that twice)
		// The kafka console consumer also uses this trick
		group = group + "-" + protocol.GetTimestamp()
		Logger.Println("Using new group id: " + group)
	}
	// set sarama to log to our log file
	sarama.Logger = Logger

	version, err := sarama.ParseKafkaVersion(version)
	if err != nil {
		Logger.Panicf("Error parsing Kafka version: %v", err)
	}

	/**
	 * Construct a new Sarama configuration.
	 * The Kafka cluster version has to be defined before the consumer/producer is initialized.
	 */
	config := sarama.NewConfig()
	config.Version = version
	config.ClientID = "upstream"

	switch assignor {
	case "sticky":
		config.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategySticky()}
	case "roundrobin":
		config.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategyRoundRobin()}
	case "range":
		config.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{sarama.NewBalanceStrategyRange()}
	default:
		Logger.Panicf("Unrecognized consumer group partition assignor: %s", assignor)
	}

	if oldest {
		config.Consumer.Offsets.Initial = sarama.OffsetOldest
	}

	/**
	 * Setup a new Sarama consumer group
	 */
	consumer := Consumer{
		ready: make(chan bool),
	}

	ctx, cancel := context.WithCancel(context.Background())
	client, err := sarama.NewConsumerGroup(strings.Split(brokers, ","), group, config)
	if err != nil {
		Logger.Panicf("Error creating consumer group client: %v", err)
	}

	consumptionIsPaused := false
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			// `Consume` should be called inside an infinite loop, when a
			// server-side rebalance happens, the consumer session will need to be
			// recreated to get the new claims
			if err := client.Consume(ctx, strings.Split(topics, ","), &consumer); err != nil {
				if errors.Is(err, sarama.ErrClosedConsumerGroup) {
					return
				}
				Logger.Panicf("Error from consumer: %v", err)
			}
			// check if context was cancelled, signaling that the consumer should stop
			if ctx.Err() != nil {
				return
			}
			consumer.ready = make(chan bool)
		}
	}()

	<-consumer.ready // Await till the consumer has been set up
	Logger.Println("Sarama consumer up and running!...")

	sigusr1 := make(chan os.Signal, 1)
	signal.Notify(sigusr1, syscall.SIGUSR1)

	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGINT, syscall.SIGTERM)

	for keepRunning {
		select {
		case <-ctx.Done():
			Logger.Println("terminating: context cancelled")
			keepRunning = false
		case <-sigterm:
			Logger.Println("terminating: via signal")
			keepRunning = false
		case <-sigusr1:
			toggleConsumptionFlow(client, &consumptionIsPaused)
		}
	}
	cancel()
	wg.Wait()
	if err = client.Close(); err != nil {
		Logger.Panicf("Error closing client: %v", err)
	}
}

func toggleConsumptionFlow(client sarama.ConsumerGroup, isPaused *bool) {
	if *isPaused {
		client.ResumeAll()
		Logger.Println("Resuming consumption")
	} else {
		client.PauseAll()
		Logger.Println("Pausing consumption")
	}

	*isPaused = !*isPaused
}

// Consumer represents a Sarama consumer group consumer
type Consumer struct {
	ready chan bool
}

// Setup is run at the beginning of a new session, before ConsumeClaim
func (consumer *Consumer) Setup(sarama.ConsumerGroupSession) error {
	// Mark the consumer as ready
	close(consumer.ready)
	return nil
}

// Cleanup is run at the end of a session, once all ConsumeClaim goroutines have exited
func (consumer *Consumer) Cleanup(sarama.ConsumerGroupSession) error {
	return nil
}

// ConsumeClaim must start a consumer loop of ConsumerGroupClaim's Messages().
// Once the Messages() channel is closed, the Handler must finish its processing
// loop and exit.
func (consumer *Consumer) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	// NOTE:
	// Do not move the code below to a goroutine.
	// The `ConsumeClaim` itself is called within a goroutine, see:
	// https://github.com/IBM/sarama/blob/main/consumer_group.go#L27-L29
	delimiter := "_"
	keepRunning := true
	for keepRunning {
		select {
		case message, ok := <-claim.Messages():
			if !ok {				
				Logger.Printf("message channel was closed")
				return nil
			}
			if from.Before(message.Timestamp) {
				if (verbose) {
					Logger.Printf("Received message from topic %s: key = %s, value = %s, partition = %d, offset = %d\n", message.Topic, string(message.Key), string(message.Value), message.Partition, message.Offset)
				}
				id := message.Topic + delimiter +
						fmt.Sprint(message.Partition) + delimiter +
						fmt.Sprint(message.Offset)
				
				// Emit the message (mostly with udp)
				keepRunning = callback(id, message.Key, message.Timestamp, message.Value)
				session.MarkMessage(message, "")
			}
		// Should return when `session.Context()` is done.
		// If not, will raise `ErrRebalanceInProgress` or `read tcp <ip>:<port>: i/o timeout` when kafka rebalance. see:
		// https://github.com/IBM/sarama/issues/1192
		case <-session.Context().Done():
			return nil
		}
	}
	// shutting down
	return nil
}

