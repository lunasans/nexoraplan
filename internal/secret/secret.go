// Package secret verschlüsselt kleine Geheimnisse (das CalDAV-Passwort) für
// die Datenbank mit AES-256-GCM. Ein Abzug der Datenbank allein verrät sie
// nicht; der Schlüssel steht nur in der Env-Datei (root-eigen).
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

const prefix = "v1:"

// Box ver- und entschlüsselt mit einem festen Schlüssel.
type Box struct {
	aead cipher.AEAD
}

// GenerateKey liefert einen neuen Schlüssel (32 Byte, Base64).
func GenerateKey() string {
	key := make([]byte, 32)
	rand.Read(key)
	return base64.StdEncoding.EncodeToString(key)
}

// New prüft den Schlüssel (Base64, 32 Byte).
func New(encoded string) (*Box, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("Schlüssel muss 32 Byte lang und Base64-kodiert sein (erzeugen mit: nexoraplan genkey)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal verschlüsselt plain; label bindet den Wert an seinen Zweck (eine
// Einstellung lässt sich nicht in eine andere umkopieren).
func (b *Box) Seal(plain, label string) string {
	nonce := make([]byte, b.aead.NonceSize())
	rand.Read(nonce)
	out := b.aead.Seal(nonce, nonce, []byte(plain), []byte(label))
	return prefix + base64.StdEncoding.EncodeToString(out)
}

// Open entschlüsselt einen Wert von Seal.
func (b *Box) Open(sealed, label string) (string, error) {
	if len(sealed) < len(prefix) || sealed[:len(prefix)] != prefix {
		return "", errors.New("unbekanntes Format")
	}
	raw, err := base64.StdEncoding.DecodeString(sealed[len(prefix):])
	if err != nil || len(raw) < b.aead.NonceSize() {
		return "", errors.New("beschädigter Wert")
	}
	n := b.aead.NonceSize()
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], []byte(label))
	if err != nil {
		return "", fmt.Errorf("entschlüsseln fehlgeschlagen – anderer Schlüssel? %w", err)
	}
	return string(plain), nil
}
