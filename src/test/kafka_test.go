package test

import (
	"fmt"
	"log"
	"reflect"
	"testing"
	"time"

	"sitia.nu/airgap/src/kafka"
	"sitia.nu/airgap/src/mtu"
	"sitia.nu/airgap/src/protocol"
	"sitia.nu/airgap/src/udp"
)

func notestKafkaSendMessage(t *testing.T) {
    tests := []struct {
        name     string
        mtu      int
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
            expected: 33,
        },
        // add more test cases as needed
    }
    // func FormatMessage(id string, message string, mtu int) []string {
    receiver := "127.0.0.1:1234"

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            actual := protocol.FormatMessage(protocol.TYPE_MESSAGE, tt.id, []byte(tt.message), uint16(tt.mtu))
            for _, bytes := range actual {
                if len(bytes) > 0 {
                    fmt.Println("ToSend: " + string(bytes))
                    // Send UDP
                    udp.SendMessage(bytes, receiver)
                    // check the result
                    if !reflect.DeepEqual(string(bytes), tt.message) {
                        t.Errorf("FormatMessage(%d, %s, %s) = %v; expected %v", tt.mtu, tt.id, tt.message, actual, tt.expected)
                    }
                }
            }
        })
    }
}

// package public
var kafkaCache = protocol.CreateMessageCache()
var maxlength uint16 = 1000

func handleKafkaMessage(id string, key []byte, _ time.Time, received []byte) bool {
    fmt.Printf("%s\n", received)

    message := protocol.FormatMessage(protocol.TYPE_MESSAGE, id, received, maxlength)
    log.Println(message)
    return true
}

func TestKafkaReceiverMessage(t *testing.T) {
    address := "192.168.153.138:9092"
    topic := "transfer"
    group := "transfer"
    from := "2024-01-03T11:34:03+01:00"
    toIP := "127.0.0.1"
    toNic := "en0"
    fmt.Println("Calling ReadFromKafka...")

    mtulength, err := mtu.GetMTU(toNic, toIP + ":514")
    if err == nil {
        maxlength = uint16(mtulength)
    } else {
        log.Panicf("Error from getting MTU for %s %s: %v", toNic, toIP, err)
    }

    kafka.ReadFromKafka(address, topic, group, from, handleKafkaMessage)
}


func notestSendKafkaMessageEncrypted(t *testing.T) {
    tests := []struct {
        name     string
        mtu      int
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
            expected: 33,
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
            actual := protocol.FormatMessage(protocol.TYPE_MESSAGE, tt.id, ciphertext, uint16(tt.mtu))
            for _, str := range actual {
                _, _, encrypted, ok := protocol.ParseMessage(str, cache) // Pass the dereferenced value of cache
                if (ok == nil) {
                    // Complete message
                    decrypted, _ := protocol.Decrypt(encrypted, key)
                    fmt.Println("Received: " + string(decrypted))
                    if ok == nil {
                        // check the result
                        if !reflect.DeepEqual(decrypted, tt.message) {
                            t.Errorf("FormatMessage(%d, %s, %s) = %v; expected %v", tt.mtu, tt.id, tt.message, actual, tt.expected)
                        }
                    }
                } // else the message is empty (part of a larger message)
            }
        })
    }
}