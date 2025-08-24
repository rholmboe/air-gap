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
)

// Sarama configuration options
var (
	version  = sarama.DefaultVersion.String()
	assignor = "roundrobin"
	oldest   = true
	verbose  = true
)

// This is called for each sending thread
func ReadFromKafka(name string, offsetSeconds int, brokers string, topics string, group string, timestamp string, callbackFunction func(string, []byte, time.Time, []byte) bool) {
	keepRunning := true
	Logger.Println("Starting a new Sarama consumer: " + name + " with offset: " + fmt.Sprintf("%d", offsetSeconds))

	// set sarama to log to our log file
	sarama.Logger = Logger

	version, err := sarama.ParseKafkaVersion(version)
	if err != nil {
		Logger.Panicf("Error parsing Kafka version: %v", err)
	}

	config := sarama.NewConfig()
	config.Version = version
	config.ClientID = name

	// Set initial offset to timestamp
	var from time.Time
	if timestamp != "" {
		t, err := time.Parse("2006-01-02T15:04:05-07:00", timestamp)
		if err != nil {
			Logger.Panicf("Error parsing from time: %v", err)
		}
		from = t
	} else {
		from = time.Now().Add(time.Duration(offsetSeconds) * time.Second)
	}
	config.Consumer.Offsets.Initial = sarama.OffsetOldest

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

	consumer := Consumer{
		name: name,
		ready: make(chan bool),
		from:  from,
		delay: time.Duration(offsetSeconds) * time.Second,
		callback: callbackFunction,
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
	Logger.Println("Sarama consumer " + name + " up and running!...")

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
			toggleConsumptionFlow(client, &consumptionIsPaused, &consumer)
		}
	}
	cancel()
	wg.Wait()
	if err = client.Close(); err != nil {
		Logger.Panicf("Error closing client: %v", err)
	}
}

func toggleConsumptionFlow(_ sarama.ConsumerGroup, isPaused *bool, _ *Consumer) {
	// Optionally use consumer.from for seeking if needed
	*isPaused = !*isPaused
}

// Consumer represents a Sarama consumer group consumer
type Consumer struct {
	name     string
	ready    chan bool
	from     time.Time
	delay    time.Duration
	callback func(string, []byte, time.Time, []byte) bool
}

// Setup is run at the beginning of a new session, before ConsumeClaim
func (consumer *Consumer) Setup(sarama.ConsumerGroupSession) error {
	// Mark the consumer as ready
	close(consumer.ready)
	return nil
}

func (consumer *Consumer) Cleanup(sarama.ConsumerGroupSession) error {
	return nil
}

func (consumer *Consumer) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
    delimiter := "_"
    keepRunning := true
    for keepRunning {
        select {
        case message, ok := <-claim.Messages():
            if !ok {
                Logger.Printf("message channel was closed")
                return nil
            }
			sleepTime := time.Until(message.Timestamp.Add(-consumer.delay))
			if sleepTime > 0 {
				Logger.Print("Consumer.delay: " + consumer.delay.String())
				Logger.Print("Delaying message delivery on thread: " + consumer.name + " for " + sleepTime.String())
				time.Sleep(sleepTime)
			}
			if verbose {
				Logger.Printf("Delivering message in thread %s from topic %s: key = %s, value = %s, partition = %d, offset = %d, msgTime = %s\n", consumer.name, message.Topic, string(message.Key), string(message.Value), message.Partition, message.Offset, message.Timestamp)
			}
			id := message.Topic + delimiter +
				fmt.Sprint(message.Partition) + delimiter +
				fmt.Sprint(message.Offset)
			keepRunning = consumer.callback(id, message.Key, message.Timestamp, message.Value)
			session.MarkMessage(message, "")
        case <-session.Context().Done():
            return nil
        default:
            // Sleep briefly to avoid busy loop
            time.Sleep(10 * time.Millisecond)
        }
    }
    // shutting down
    return nil
}

