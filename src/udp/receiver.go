package udp

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"sitia.nu/airgap/src/logging"
)

type UDPAdapterLinux struct {
	addr                 string
	conns                []*net.UDPConn
	closed               atomic.Bool
	closeOnce            sync.Once
	wg                   sync.WaitGroup
	numReceivers         int
	channelBufferSize    int
	mtu                  uint16
	readBufferMultiplier uint16
}

var Logger = logging.Logger

func (u *UDPAdapterLinux) Setup(
	mtu uint16,
	numReceivers int,
	channelBufferSize int,
	readBufferMultiplier uint16,
) {
	u.mtu = mtu
	u.numReceivers = numReceivers
	u.channelBufferSize = channelBufferSize
	u.readBufferMultiplier = readBufferMultiplier
}

func NewUDPAdapterLinux(addr string) *UDPAdapterLinux {
	return &UDPAdapterLinux{
		addr:                 addr,
		numReceivers:         8,
		channelBufferSize:    8192,
		mtu:                  1500,
		readBufferMultiplier: 16,
	}
}

// Listen starts multiple sockets for SO_REUSEPORT
func (u *UDPAdapterLinux) Listen(ip string, port int, rcvBufSize int, handler func([]byte), stopChan <-chan struct{}) {
	addrStr := net.JoinHostPort(ip, fmt.Sprintf("%d", port))
	Logger.Printf("UDP listener starting on %s with %d workers (MTU=%d)", addrStr, u.numReceivers, u.mtu)

	packetChan := make(chan []byte, u.channelBufferSize)

	// --- start worker goroutines ---
	for i := 0; i < u.numReceivers; i++ {
		u.wg.Add(1)
		go func(id int) {
			defer u.wg.Done()
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()

			for {
				select {
				case pkt := <-packetChan:
					if pkt == nil {
						return
					}
					handler(pkt)
				case <-stopChan:
					return
				}
			}
		}(i)
	}

	// --- create one socket per worker using SO_REUSEPORT ---
	for i := 0; i < u.numReceivers; i++ {
		lc := net.ListenConfig{
			Control: func(network, address string, c syscall.RawConn) error {
				return c.Control(func(fd uintptr) {
					_ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
					_ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
					_ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF, rcvBufSize)
				})
			},
		}

		conn, err := lc.ListenPacket(context.Background(), "udp", addrStr)
		if err != nil {
			Logger.Fatalf("Failed to listen on %s: %v", addrStr, err)
		}
		udpConn := conn.(*net.UDPConn)
		u.conns = append(u.conns, udpConn)

		u.wg.Add(1)
		// Worker thread for reading from this socket
		go func(c *net.UDPConn) {
			defer u.wg.Done()
			readBuf := make([]byte, int(u.mtu)*int(u.readBufferMultiplier))

			for {
				select {
				case <-stopChan:
					return
				default:
					c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
					n, _, err := c.ReadFromUDP(readBuf)
					if err != nil {
						if ne, ok := err.(net.Error); ok && ne.Timeout() {
							continue
						}
						if u.closed.Load() {
							return
						}
						Logger.Errorf("UDP read error: %v", err)
						continue
					}
					buf := make([]byte, n)
					copy(buf, readBuf[:n])

					select {
					case packetChan <- buf:
					case <-stopChan:
						return
					}
				}
			}
		}(udpConn)
	}

	// Wait for stop signal
	<-stopChan

	// --- shutdown ---
	u.Close()
	close(packetChan)

	// Give workers a small timeout to finish processing remaining messages
	timeout := time.After(2 * time.Second)
	done := make(chan struct{})
	go func() {
		u.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-timeout:
		Logger.Warnf("UDPAdapter shutdown timeout reached, some messages may be lost")
	}
}

// Close closes all sockets
func (u *UDPAdapterLinux) Close() error {
	u.closeOnce.Do(func() {
		u.closed.Store(true)
		for _, c := range u.conns {
			c.Close()
		}
	})
	return nil
}
