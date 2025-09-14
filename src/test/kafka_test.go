package test

import (
	"fmt"
	"reflect"
	"testing"

	"sitia.nu/airgap/src/protocol"
)

// package public
var maxlength uint16 = 1000

// func handleKafkaMessage(id string, key []byte, _ time.Time, received []byte) bool {
// 	fmt.Printf("%s\n", received)

// 	message := protocol.FormatMessage(protocol.TYPE_MESSAGE, id, received, maxlength)
// 	log.Println(message)
// 	return true
// }

// Todo: add Kafka sending
func TestSendKafkaMessageEncrypted(t *testing.T) {
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
				if ok != nil {
					// Message is incomplete or part of a larger message, skip
					return
				}
				// Complete message
				decrypted, _ := protocol.Decrypt(encrypted, key)
				fmt.Println("Received: " + string(decrypted))
				// check the result
				if !reflect.DeepEqual(decrypted, tt.message) {
					t.Errorf("FormatMessage(%d, %s, %s) = %v; expected %v", tt.mtu, tt.id, tt.message, actual, tt.expected)
				}
			}
		})
	}
}
