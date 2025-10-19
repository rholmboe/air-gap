// src/downstream/adapters.go
package create

import (
	"context"
	"time"

	"sitia.nu/airgap/src/kafka"
)

// KafkaAdapter implements the KafkaReader interface
type KafkaAdapter struct{}

func NewKafkaReader() *KafkaAdapter {
	return &KafkaAdapter{}
}

func (r *KafkaAdapter) ReadToEnd(ctx context.Context, brokers string, topic string, group string, callbackFunction func(string, []byte, time.Time, []byte) bool) error {
	return kafka.ReadToEnd(ctx, brokers, topic, group, callbackFunction)
}
