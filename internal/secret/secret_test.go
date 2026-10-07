// SPDX-License-Identifier: Apache-2.0
package secret

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestAuthenticatedEncryption(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	c, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	first, err := c.Encrypt("synthetic upstream secret", "channel:1")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := c.Encrypt("synthetic upstream secret", "channel:1")
	if first == second {
		t.Fatal("nonce reused")
	}
	plain, err := c.Decrypt(first, "channel:1")
	if err != nil || plain != "synthetic upstream secret" {
		t.Fatal("round trip failed")
	}
	if _, err = c.Decrypt(first, "channel:2"); !errors.Is(err, ErrDecrypt) {
		t.Fatal("purpose substitution accepted")
	}
	other, _ := New(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32))))
	if _, err = other.Decrypt(first, "channel:1"); !errors.Is(err, ErrDecrypt) {
		t.Fatal("wrong key accepted")
	}
	for _, value := range []string{"", "invalid", "v1:not-base64!", "v1:YWJj"} {
		if _, err = c.Decrypt(value, "channel:1"); !errors.Is(err, ErrDecrypt) {
			t.Fatal("malformed ciphertext accepted")
		}
	}
	for _, master := range []string{"", "password", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err = New(master); !errors.Is(err, ErrInvalidMasterKey) {
			t.Fatal("invalid master accepted")
		}
	}
}

func TestTokenHash(t *testing.T) {
	a, err := Token("nmg_")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Token("nmg_")
	if a == b || len(a) < 40 || !strings.HasPrefix(a, "nmg_") {
		t.Fatal("invalid randomness")
	}
	if Hash(a) == a || len(Hash(a)) != 64 {
		t.Fatal("hash invalid")
	}
}
