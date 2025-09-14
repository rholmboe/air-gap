package udp

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// Start a UDP listener on the assigned port and address, stoppable via stopChan
func ListenUDPWithStop(address string, port int, callback func(arg []byte), mtu uint16, stopChan <-chan struct{}) {
	addr := net.UDPAddr{
		Port: port,
		IP:   net.ParseIP(address),
	}

	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer conn.Close()

	Logger.Printf("Listening on %s (stoppable)\n", addr.String())

	buf := make([]byte, mtu)
	done := false
	for !done {
		select {
		case <-stopChan:
			Logger.Printf("UDP server on %s stopping due to signal\n", addr.String())
			conn.Close() // force ReadFromUDP to return
			done = true
		default:
			conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue // check stopChan again
				}
				// If conn is closed, exit loop
				if opErr, ok := err.(*net.OpError); ok && opErr.Err.Error() == "use of closed network connection" {
					done = true
					continue
				}
				fmt.Println(err)
				continue
			}
			// Copy the data before passing it to the callback
			callback(append([]byte(nil), buf[:n]...))
		}
	}
}

// Handle a connection
// Just call the callback function with the data for every received packet
func handleConnection(conn *net.UDPConn, _ *net.UDPAddr, wg *sync.WaitGroup, callback func(arg []byte), mtu uint16) {
	defer wg.Done()

	buf := make([]byte, mtu)
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		fmt.Println(err)
		return
	}
	callback(buf[:n])
}

// Start a UDP listener on the assigned port and address
func ListenUDP(address string, port int, callback func(arg []byte), mtu uint16) {
	addr := net.UDPAddr{
		Port: port,
		IP:   net.ParseIP(address),
	}

	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer conn.Close()

	Logger.Printf("Listening on %s\n", addr.String())

	var wg sync.WaitGroup

	for {
		wg.Add(1)
		go func() {
			handleConnection(conn, &addr, &wg, callback, mtu)
		}()
		wg.Wait()
	}
}
