// SPDX-License-Identifier: Apache-2.0
package payment

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
)

func parsePrivateKey(s string) (*rsa.PrivateKey, error) {
	b := []byte(s)
	if block, _ := pem.Decode(b); block != nil {
		b = block.Bytes
	} else {
		var err error
		b, err = base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, ErrProviderUnavailable
		}
	}
	if k, err := x509.ParsePKCS1PrivateKey(b); err == nil {
		if k.N.BitLen() < 2048 {
			return nil, ErrProviderUnavailable
		}
		return k, nil
	}
	v, err := x509.ParsePKCS8PrivateKey(b)
	if err != nil {
		return nil, ErrProviderUnavailable
	}
	k, ok := v.(*rsa.PrivateKey)
	if !ok || k.N.BitLen() < 2048 {
		return nil, ErrProviderUnavailable
	}
	return k, nil
}
func normalizedPublicKey(s string) (string, error) {
	b := []byte(s)
	if block, _ := pem.Decode(b); block != nil {
		b = block.Bytes
	} else {
		var err error
		b, err = base64.StdEncoding.DecodeString(s)
		if err != nil {
			return "", ErrProviderUnavailable
		}
	}
	v, err := x509.ParsePKIXPublicKey(b)
	if err != nil {
		return "", ErrProviderUnavailable
	}
	k, ok := v.(*rsa.PublicKey)
	if !ok || k.N.BitLen() < 2048 {
		return "", ErrProviderUnavailable
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
