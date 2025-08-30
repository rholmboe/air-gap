package kafka

import (
	"sync"
	"time"

	"github.com/IBM/sarama"
)

type MessageCacheEntry struct {
	end   time.Time
	key   string
	val   []byte
	topic string
}

type MessageCache struct {
	mu sync.RWMutex
	// key is the id of the message
	entries []MessageCacheEntry
}

// Time to live for items in the message cache
const TTL = 30000

var (
	cache = MessageCache{
		entries: make([]MessageCacheEntry, 0),
	}
	producer sarama.AsyncProducer
	doneChan chan struct{}
	wg       sync.WaitGroup
)

func SetProducer(p sarama.AsyncProducer) {
	// Stop background thread before replacing producer
	StopBackgroundThread()
	producer = p
	// Clear cache before starting new thread
	cache.mu.Lock()
	cache.entries = nil
	cache.mu.Unlock()
}

func StartBackgroundThread() {
	if producer == nil || doneChan != nil {
		return
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	doneChan = make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ticker.C:
				length := len(cache.entries)
				for i := 0; i < len(cache.entries); i++ {
					entry := cache.entries[i]
					DoWriteToKafka(entry.key, entry.topic, entry.val)
				}
				cache.mu.Lock()
				cache.entries = cache.entries[length:]
				cache.mu.Unlock()
			case <-doneChan:
				ticker.Stop()
				return
			}
		}
	}()
}

// Stop the Kafka background thread safely
func StopBackgroundThread() {
	if doneChan != nil {
		close(doneChan)
		wg.Wait()
		doneChan = nil
		// Clear cache to avoid sending old messages after restart
		cache.mu.Lock()
		cache.entries = nil
		cache.mu.Unlock()
	}
}

// WriteToKafka stores a message in the cache with a specified key and topic.
// The message is stored with a TTL (Time To Live), after which it will be removed from the cache. (not yet implemented)
// The function locks the cache while adding the entry to prevent race conditions.
//
// Parameters:
// messageKey: A unique identifier for the message. This key is used to retrieve the message from the cache.
// topics: The Kafka topic where the message will be published.
// message: The message to be published to the Kafka topic.
func WriteToKafka(messageKey string, topics string, message []byte) {
	cache.mu.Lock()
	cache.entries = append(cache.entries, MessageCacheEntry{
		end:   time.Now().Add(time.Millisecond + TTL),
		key:   messageKey,
		val:   message,
		topic: topics,
	})
	cache.mu.Unlock()
}

// Background thread that sends messages to Kafka.
func DoWriteToKafka(messageKey string, topics string, message []byte) {
	defer func() {
		if r := recover(); r != nil {
			Logger.Printf("Recovered from panic in DoWriteToKafka: %v", r)
		}
	}()
	if producer == nil {
		return
	}
	// Create a new message
	msg := &sarama.ProducerMessage{
		Topic: topics,
		Key:   sarama.StringEncoder(messageKey),
		Value: sarama.StringEncoder(message),
	}
	// Send the message asynchronously to Kafka
	producer.Input() <- msg
}
