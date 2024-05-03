package udp

import (
	"net"
)

// Send a UDP message
func SendMessage(message []byte, address string) error {
	conn, err := net.Dial("udp", address)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.Write(message)
	if err != nil {
		return err
	}
	return nil
}

