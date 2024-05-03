package protocol

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
)

func Decrypt(ciphertext []byte, key []byte) ([]byte, error) {
    // Create a new cipher block
    block, err := aes.NewCipher(key)
    if err != nil {
        return nil, err
    }

    // Create a new GCM
    gcm, err := cipher.NewGCM(block)
    if err != nil {
        return nil, err
    }

    // Split the nonce and the ciphertext
    nonceSize := gcm.NonceSize()
    if len(ciphertext) < nonceSize {
        return nil, errors.New("ciphertext too short")
    }
    nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]

    // Decrypt the data
    plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
    if err != nil {
        return nil, err
    }

    return plaintext, nil
}