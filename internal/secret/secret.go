// SPDX-License-Identifier: Apache-2.0
// Package secret handles credentials without exposing them through diagnostics.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
)

var ErrInvalidMasterKey = errors.New("master key must be base64 encoding of exactly 32 random bytes")
var ErrDecrypt = errors.New("encrypted credential cannot be opened with this master key")

type Cipher struct{ aead cipher.AEAD }

func New(masterKey string) (*Cipher, error) {
	b, err := base64.StdEncoding.DecodeString(masterKey)
	if err != nil || len(b) != 32 {
		return nil, ErrInvalidMasterKey
	}
	block, err := aes.NewCipher(b)
	if err != nil {
		return nil, ErrInvalidMasterKey
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Encrypt(plain, purpose string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", errors.New("credential randomness unavailable")
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plain), []byte(purpose))
	return "v1:" + base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (c *Cipher) Decrypt(value, purpose string) (string, error) {
	if len(value) < 3 || value[:3] != "v1:" {
		return "", ErrDecrypt
	}
	b, err := base64.RawStdEncoding.DecodeString(value[3:])
	if err != nil || len(b) < c.aead.NonceSize()+c.aead.Overhead() {
		return "", ErrDecrypt
	}
	n := c.aead.NonceSize()
	plain, err := c.aead.Open(nil, b[:n], b[n:], []byte(purpose))
	if err != nil {
		return "", ErrDecrypt
	}
	return string(plain), nil
}

func Token(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", errors.New("credential randomness unavailable")
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func Hash(value string) string { h := sha256.Sum256([]byte(value)); return hex.EncodeToString(h[:]) }
