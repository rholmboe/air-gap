package test

import (
	"context"
	"testing"
	"time"
)

// KafkaMessage is a helper for mock messages
type KafkaMessage struct {
	ID        string
	Key       []byte
	Timestamp time.Time
	Value     []byte
}

// MockKafkaReader implements KafkaReader for testing
type MockKafkaReader struct {
	Messages []KafkaMessage
}

func (m *MockKafkaReader) ReadToEnd(ctx context.Context, brokers, topic, group, timestamp string,
	callback func(id string, key []byte, ts time.Time, value []byte) bool, partition int) error {
	for _, msg := range m.Messages {
		callback(msg.ID, msg.Key, msg.Timestamp, msg.Value)
	}
	return nil
}

// MockUDPSender implements UDPSender for testing
type MockUDPSender struct {
	Sent [][]byte
}

func (m *MockUDPSender) Send(addr string, data []byte) error {
	m.Sent = append(m.Sent, data)
	return nil
}

func TestResend_MockAdapters(t *testing.T) {
	mockKafka := &MockKafkaReader{
		Messages: []KafkaMessage{
			{ID: "topic_0_1", Key: []byte("key1"), Timestamp: time.Now(), Value: []byte("value1")},
			{ID: "topic_0_2", Key: []byte("key2"), Timestamp: time.Now(), Value: []byte("value2")},
		},
	}
	mockUDP := &MockUDPSender{}

	ctx := context.Background()
	called := 0
	callback := func(id string, key []byte, ts time.Time, value []byte) bool {
		called++
		// Simulate sending over UDP
		if err := mockUDP.Send("127.0.0.1:9999", value); err != nil {
			t.Errorf("UDP send failed: %v", err)
		}
		return true
	}

	err := mockKafka.ReadToEnd(ctx, "broker", "topic", "group", "", callback, 0)
	if err != nil {
		t.Fatalf("Kafka ReadToEnd failed: %v", err)
	}

	if called != 2 {
		t.Errorf("Expected 2 messages, got %d", called)
	}
	if len(mockUDP.Sent) != 2 {
		t.Errorf("Expected 2 UDP sends, got %d", len(mockUDP.Sent))
	}
	if string(mockUDP.Sent[0]) != "value1" || string(mockUDP.Sent[1]) != "value2" {
		t.Errorf("UDP sent values mismatch: %v", mockUDP.Sent)
	}
}
