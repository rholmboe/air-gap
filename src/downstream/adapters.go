package downstream

import (
	"fmt"
	"sync/atomic"
	"time"

	"sitia.nu/airgap/src/kafka"
	"sitia.nu/airgap/src/udp"
)

// UDPAdapter is a thin wrapper around the optimized UDP receiver.
type UDPAdapter struct {
	inner *udp.UDPAdapterLinux
}

// NewUDPAdapter creates a new instance with parameters from TransferConfiguration.
func NewUDPAdapter(config TransferConfiguration) *UDPAdapter {
	addr := fmt.Sprintf("%s:%d", config.targetIP, config.targetPort)
	a := &UDPAdapter{inner: udp.NewUDPAdapterLinux(addr)}

	a.inner.Setup(
		config.mtu,
		config.numReceivers,
		config.channelBufferSize,
		config.readBufferMultiplier,
	)

	return a
}

// Setup allows overriding tuning parameters at runtime (e.g., from CLI or config reload).
func (a *UDPAdapter) Setup(
	mtu uint16,
	numReceivers int,
	channelBufferSize int,
	readBufferMultiplier uint16,
) {
	a.inner.Setup(mtu, numReceivers, channelBufferSize, readBufferMultiplier)
}

// Listen delegates to UDPAdapterLinux.Listen, converting stopChan to a proper channel.
func (a *UDPAdapter) Listen(
	address string,
	port int,
	rcvBufSize int,
	callback func([]byte),
	payloadSize uint16,
	stopChan <-chan struct{},
	_ int, // numReceivers unused (already configured in Setup)
) {
	realStop := make(chan struct{})
	if stopChan != nil {
		go func() {
			<-stopChan
			close(realStop)
		}()
	}
	a.inner.Listen(address, port, rcvBufSize, callback, realStop)
}

// Close gracefully shuts down the UDP listener.
func (a *UDPAdapter) Close() error {
	return a.inner.Close()
}

//
// ────────────────────────────── Kafka Adapter ───────────────────────────────
//

type KafkaAdapter struct{}

func NewKafkaAdapter() *KafkaAdapter { return &KafkaAdapter{} }

func (a *KafkaAdapter) Write(key string, topic string, partition int32, message []byte) error {
	kafka.WriteToKafka(key, topic, partition, message)
	return nil
}

func (a *KafkaAdapter) Flush() {
	kafka.StopBackgroundThread()
}

func (a *KafkaAdapter) Close() error {
	kafka.StopBackgroundThread()
	kafka.CloseProducer()
	return nil
}

//
// ────────────────────────────── Cmd Adapter ───────────────────────────────
//

// CmdAdapter writes to stdout for debugging or CLI testing.
type CmdAdapter struct{}

func NewCmdAdapter() *CmdAdapter { return &CmdAdapter{} }

func (a *CmdAdapter) Write(_ string, _ string, _ int32, message []byte) error {
	println(string(message))
	return nil
}

func (a *CmdAdapter) Flush()       {}
func (a *CmdAdapter) Close() error { return nil }

//
// ────────────────────────────── Null Adapter ───────────────────────────────
//

// NullAdapter is used for performance benchmarking and profiling.
type NullAdapter struct {
	numMessages int64
	startTime   time.Time
}

func NewNullAdapter() *NullAdapter { return &NullAdapter{} }

func (a *NullAdapter) Write(_ string, _ string, _ int32, _ []byte) error {
	atomic.AddInt64(&a.numMessages, 1)
	if a.startTime.IsZero() {
		a.startTime = time.Now()
	}
	return nil
}

func (a *NullAdapter) Flush() {
	total := atomic.LoadInt64(&a.numMessages)
	Logger.Printf("NullAdapter flush: %d messages total", total)

	if !a.startTime.IsZero() {
		elapsed := time.Since(a.startTime)
		if elapsed > 0 {
			eps := float64(total) / elapsed.Seconds()
			Logger.Printf("Processed %d messages in %s (%.2f EPS)",
				total, elapsed.Truncate(time.Millisecond), eps)
		}
	}
}

func (a *NullAdapter) Close() error { return nil }
