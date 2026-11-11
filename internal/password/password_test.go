package password

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	hash := Hash("richtiges-passwort")

	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("unerwartetes Format: %s", hash)
	}
	if !Verify("richtiges-passwort", hash) {
		t.Error("richtiges Passwort wurde abgelehnt")
	}
	if Verify("falsches-passwort", hash) {
		t.Error("falsches Passwort wurde akzeptiert")
	}
	if NeedsRehash(hash) {
		t.Error("frischer Hash sollte kein Rehash brauchen")
	}
}

func TestSaltIsRandom(t *testing.T) {
	if Hash("gleich") == Hash("gleich") {
		t.Error("zwei Hashes desselben Passworts sind identisch – Salt fehlt?")
	}
}

func TestInvalidHashes(t *testing.T) {
	for _, h := range []string{
		"",
		"klartext",
		"$2a$10$bcrypthashbcrypthashbcrypthashbcrypthashbcrypthash",
		"$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$a2V5",
		"$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$",
	} {
		if Verify("egal", h) {
			t.Errorf("ungültiger Hash %q wurde akzeptiert", h)
		}
		if !NeedsRehash(h) {
			t.Errorf("ungültiger Hash %q sollte Rehash brauchen", h)
		}
	}
}

func TestOldParametersNeedRehash(t *testing.T) {
	old := "$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"
	if !NeedsRehash(old) {
		t.Error("Hash mit alten Parametern sollte Rehash brauchen")
	}
}
