package secret

import "testing"

func TestSealOpen(t *testing.T) {
	b, err := New(GenerateKey())
	if err != nil {
		t.Fatal(err)
	}
	sealed := b.Seal("geheim", "caldav")
	if got, err := b.Open(sealed, "caldav"); err != nil || got != "geheim" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	if _, err := b.Open(sealed, "anderes"); err == nil {
		t.Fatal("anderes Label darf nicht entschlüsseln")
	}
	other, _ := New(GenerateKey())
	if _, err := other.Open(sealed, "caldav"); err == nil {
		t.Fatal("anderer Schlüssel darf nicht entschlüsseln")
	}
}

func TestNewRejectsBadKey(t *testing.T) {
	for _, k := range []string{"", "kurz", "AAAA"} {
		if _, err := New(k); err == nil {
			t.Errorf("New(%q) ohne Fehler", k)
		}
	}
}
