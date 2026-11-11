// Package password hasht und prüft Passwörter mit Argon2id.
//
// Gespeichert wird das übliche PHC-Format, das auch andere Programme lesen:
//
//	$argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>
//
// Die Parameter stehen im Hash selbst. Werden sie später erhöht, bleiben alte
// Hashes prüfbar, und NeedsRehash meldet, dass ein Hash neu berechnet werden sollte.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Parameter nach RFC 9106 / OWASP: 64 MiB Speicher, 3 Durchläufe, 2 Threads.
// Dauert auf einem CPX-Server grob 50–100 ms pro Anmeldung – für Nutzer
// unmerklich, für Angreifer, die Millionen Passwörter durchprobieren, teuer.
const (
	memoryKiB  = 64 * 1024
	iterations = 3
	threads    = 2
	saltLen    = 16
	keyLen     = 32
)

var ErrInvalidHash = errors.New("ungültiger Passwort-Hash")

var b64 = base64.RawStdEncoding

// Hash erzeugt einen neuen Hash mit zufälligem Salt.
func Hash(password string) string {
	salt := make([]byte, saltLen)
	rand.Read(salt) // crypto/rand schlägt seit Go 1.24 nie fehl
	key := argon2.IDKey([]byte(password), salt, iterations, memoryKiB, threads, keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memoryKiB, iterations, threads, b64.EncodeToString(salt), b64.EncodeToString(key))
}

type params struct {
	memory  uint32
	time    uint32
	threads uint8
	salt    []byte
	key     []byte
}

func parse(encoded string) (params, error) {
	// "", "argon2id", "v=19", "m=…,t=…,p=…", salt, hash
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return params{}, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return params{}, ErrInvalidHash
	}
	var p params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return params{}, ErrInvalidHash
	}
	var err error
	if p.salt, err = b64.DecodeString(parts[4]); err != nil {
		return params{}, ErrInvalidHash
	}
	if p.key, err = b64.DecodeString(parts[5]); err != nil || len(p.key) == 0 {
		return params{}, ErrInvalidHash
	}
	return p, nil
}

// Verify prüft das Passwort gegen den gespeicherten Hash.
func Verify(password, encoded string) bool {
	p, err := parse(encoded)
	if err != nil {
		return false
	}
	key := argon2.IDKey([]byte(password), p.salt, p.time, p.memory, p.threads, uint32(len(p.key)))
	// Vergleich in konstanter Zeit, damit die Antwortzeit nichts verrät.
	return subtle.ConstantTimeCompare(key, p.key) == 1
}

// NeedsRehash meldet, ob der Hash mit älteren Parametern erzeugt wurde.
func NeedsRehash(encoded string) bool {
	p, err := parse(encoded)
	if err != nil {
		return true
	}
	return p.memory != memoryKiB || p.time != iterations || p.threads != threads || len(p.key) != keyLen
}
