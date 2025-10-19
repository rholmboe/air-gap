package kafka

import (
	"time"
)

type KafkaHandler func(id string, key []byte, t time.Time, received []byte) bool
