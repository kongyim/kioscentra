package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
)

type Protector interface {
	Seal([]byte) ([]byte, error)
	Open([]byte) ([]byte, error)
}

// DevelopmentProtector requires an external key; the key is never written to SQLite.
func DevelopmentProtector(key string) (Protector, error) {
	b, e := base64.StdEncoding.DecodeString(key)
	if e != nil || len(b) != 32 {
		return nil, errors.New("KIOSENTRA_DEVELOPMENT_KEY must be base64 of 32 random bytes")
	}
	block, e := aes.NewCipher(b)
	if e != nil {
		return nil, e
	}
	a, e := cipher.NewGCM(block)
	return aesProtector{a}, e
}

type aesProtector struct{ a cipher.AEAD }

func (p aesProtector) Seal(b []byte) ([]byte, error) {
	n := make([]byte, p.a.NonceSize())
	if _, e := rand.Read(n); e != nil {
		return nil, e
	}
	return p.a.Seal(n, n, b, []byte("kiosentra-state-v1")), nil
}
func (p aesProtector) Open(b []byte) ([]byte, error) {
	n := p.a.NonceSize()
	if len(b) < n {
		return nil, errors.New("invalid sealed data")
	}
	return p.a.Open(nil, b[:n], b[n:], []byte("kiosentra-state-v1"))
}
