package test

import (
	"fmt"
	"reflect"
	"testing"

	"sitia.nu/airgap/src/protocol"
	"sitia.nu/airgap/src/udp"
)

func TestSendMessage(t *testing.T) {
    tests := []struct {
        name     string
        mtu      uint16
        id       string
        message  string
        expected int
    }{
        {
            name:     "Test 1",
            mtu:      100,
            id:       "testID",
            message:  "This is a test message",
            expected: 1,
        },
        {
            name:     "Test 2",
            mtu:      25,
            id:       "testID",
            message:  "This is a longer test message that should be split into multiple parts. abcdefghijklmnopqrstuvwxyzåäö",
            expected: 18,
        },
        // add more test cases as needed
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            actual := protocol.FormatMessage(protocol.TYPE_MESSAGE, tt.id, []byte(tt.message), uint16(tt.mtu))
            // check the result
            if !reflect.DeepEqual(len(actual), tt.expected) {
                t.Errorf("FormatMessage. Expected %d messages, got %d", tt.expected, len(actual))
            }
        })
    }
}

// package public
var cache = protocol.CreateMessageCache()



func handleMessage(received []byte) {
    fmt.Printf("*Received %s\n", received)
    _, _, message, ok := protocol.ParseMessage(received, cache)
    if ok == nil {
        fmt.Printf("Message: %s\n", message)
    }
}

// Will set up a udp receiver on port 1234 and print everything that is received on that port. No automatic test case
func SetupReceiverMessage(t *testing.T) {
    address := "127.0.0.1"
    port := 1234
    mtuLength := uint16(1500)

    udp.ListenUDP(address, port, handleMessage, mtuLength)
}


func TestSendMessageEncrypted(t *testing.T) {
    tests := []struct {
        name     string
        mtu      uint16
        id       string
        message  string
        expected uint16
    }{
        {
            name:     "Test 1",
            mtu:      100,
            id:       "testID",
            message:  "This is a test message",
            expected: 1,
        },
        {
            name:     "Test 2",
            mtu:      25,
            id:       "testID",
            message:  "This is a longer test message that should be split into multiple parts. abcdefghijklmnopqrstuvwxyzåäö",
            expected: 25,
        },
        // add more test cases as needed
    }
    // func FormatMessage(id string, message string, mtu int) []string {
    cache := protocol.CreateMessageCache()
    key := []byte("your-32-byte-key-here!abcdefghij") // replace with your 32-byte key
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            ciphertext, err := protocol.Encrypt([]byte(tt.message), key)
            if err != nil {
                fmt.Println(err)
                t.Fail()
            }
            actual := protocol.FormatMessage(protocol.TYPE_MESSAGE, tt.id, ciphertext, tt.mtu)
            for _, str := range actual {
                messageType, _, encrypted, _ := protocol.ParseMessage(str, cache) // Pass the dereferenced value of cache
                if messageType == protocol.TYPE_MESSAGE {
                    // Complete message
                    decrypted, _ := protocol.Decrypt(encrypted, key)
                    fmt.Printf("decrypted: '%s'\n", string(decrypted))
                    fmt.Printf("expected:  '%s'\n", string(tt.message));
                    // check the result
                    if !reflect.DeepEqual(string(decrypted), tt.message) {
                        t.Errorf("FormatMessage(%d, %s, %s) = %v; expected %v", tt.mtu, tt.id, tt.message, actual, tt.expected)
                    }
                } // else the message is empty (part of a larger message)
            }
        })
    }
}