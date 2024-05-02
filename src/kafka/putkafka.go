package kafka

import (
	"sync"
	"time"

	"github.com/IBM/sarama"
)


type MessageCacheEntry struct {
	end time.Time
	key string
	val []byte
	topic string
}

type MessageCache struct {
	mu	    sync.RWMutex
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
)
func SetProducer(p sarama.AsyncProducer) {
	producer = p
}

func StartBackgroundThread() {
    // Start a background thread
	ticker := time.NewTicker(10 * time.Millisecond)
    go func() {
		for range ticker.C {
            // Iterate over the cache entries
			// This may be updated by another thread
			// but this thread is the only one that
			// consumes (from the beginning)
			length := len(cache.entries)
			for i := 0; i < len(cache.entries); i++ {
				entry := cache.entries[i]
				// Send the message to Kafka
				DoWriteToKafka(entry.key, entry.topic, entry.val)
			}
			// Lock the cache. Maybe move outside for loop? Test?
			cache.mu.Lock()
			// Remove the entry from the cache
			cache.entries = cache.entries[length:]
			// Unlock the cache
			cache.mu.Unlock()
		}
	}()
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
			end: time.Now().Add(time.Millisecond + TTL),
			key: messageKey,
			val: message,
			topic: topics,
		})
	cache.mu.Unlock()
}


// Background thread that sends messages to Kafka.
func DoWriteToKafka(messageKey string, topics string, message []byte) {
    // Create a new message
    msg := &sarama.ProducerMessage{
        Topic: topics,
        Key:   sarama.StringEncoder(messageKey),
        Value: sarama.StringEncoder(message),
    }
	
    // Send the message asynchronously to Kafka
	producer.Input() <- msg
}