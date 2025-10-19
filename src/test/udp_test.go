package test

import (
	"testing"

	"sitia.nu/airgap/src/protocol"
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
			if len(actual) != tt.expected {
				t.Errorf("FormatMessage. Expected %d messages, got %d", tt.expected, len(actual))
			}
		})
	}
}

// package public
var cache = protocol.CreateMessageCache()

func TestSendMessageEncrypted(t *testing.T) {
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
			expected: 22, // match the number of parts as in the unencrypted test
		},
		// add more test cases as needed
	}
	cache := protocol.CreateMessageCache()
	key := []byte("your-32-byte-key-here!abcdefghij") // replace with your 32-byte key

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ciphertext, err := protocol.Encrypt([]byte(tt.message), key)
			if err != nil {
				t.Fatalf("Encrypt error: %v", err)
			}
			actual := protocol.FormatMessage(protocol.TYPE_MESSAGE, tt.id, ciphertext, tt.mtu)
			if len(actual) != tt.expected {
				t.Errorf("FormatMessage (encrypted). Expected %d messages, got %d", tt.expected, len(actual))
			}
			for _, str := range actual {
				messageType, _, encrypted, _ := protocol.ParseMessage(str, cache)
				if protocol.IsMessageType(messageType, protocol.TYPE_MESSAGE) {
					decrypted, _ := protocol.Decrypt(encrypted, key)
					if string(decrypted) != tt.message {
						t.Errorf("Decrypted message mismatch: got '%s', want '%s'", string(decrypted), tt.message)
					}
				}
			}
		})
	}
}
