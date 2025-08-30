package udp

import (
	"log"
	"net"
	// Send a UDP message
)

// Create and manage a reusable UDP connection
type UDPConn struct {
	conn net.Conn
}

// Create a new UDP connection
func NewUDPConn(address string) (*UDPConn, error) {
	conn, err := net.Dial("udp", address)
	if err != nil {
		return nil, err
	}
	return &UDPConn{conn: conn}, nil
}

// Close the UDP connection
func (u *UDPConn) Close() error {
	if u.conn != nil {
		return u.conn.Close()
	}
	return nil
}

// Send a UDP message using a reusable connection
func (u *UDPConn) SendMessage(message []byte) error {
	if u.conn == nil {
		return net.ErrClosed
	}
	// log.Printf("Sending %d bytes to %s\n", len(message), u.conn.RemoteAddr().String())
	_, err := u.conn.Write(message)
	return err
}

// Send multiple UDP messages using a reusable connection
func (u *UDPConn) SendMessages(messages [][]byte) error {
	if u.conn == nil {
		return net.ErrClosed
	}
	// log.Printf("Sending %d message parts to %s using a single UDP connection\n", len(messages), u.conn.RemoteAddr().String())
	for i, message := range messages {
		// log.Printf("Sending message part %d/%d, length %d bytes: %s", i+1, len(messages), len(message), message)
		_, err := u.conn.Write(message)
		if err != nil {
			log.Printf("Error sending message part %d: %v\n", i+1, err)
			return err
		}
	}
	return nil
}
