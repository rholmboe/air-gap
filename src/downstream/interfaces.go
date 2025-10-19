package downstream

// Interfaces for dependency injection / mocking
type UDPReceiver interface {
	Listen(ip string, port int, rcvBufSize int, handler func([]byte), mtu uint16, stopChan <-chan struct{}, numReceivers int)
	Close() error
	Setup(mtu uint16, numReceivers int, channelBufferSize int, readBufferMultiplier uint16)
}

type KafkaWriter interface {
	Write(key string, topic string, partition int32, message []byte) error
	Flush()
	Close() error
}
