package crypto

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

// Vectors computed independently with Node's crypto (hkdfSync, createHmac, aes-256-gcm) for the key whose bytes are
// 0x00 through 0x1f.
const (
	vectorKey            = "rostr_sk_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"
	vectorAuthKey        = "d6e2faeb0c03ee07b49c896889f38eb9443a78e8eed161be1a3588526f95cbd8"
	vectorAuthToken      = "1uL66wwD7ge0nIloifOOuUQ6eOju0WG-GjWIUm-Vy9g"
	vectorEncryptKey     = "91fc23e445c984d4fbac111eb714261017103b8f6d16a859c8e6633ad043c766"
	vectorFingerprintKey = "dd630a267d5152021908e838369b9771ba2c8dcba4e88eafd6dcbb052823e50e"
	vectorFingerprint    = "88833500097898a8c8f313fc0b5fa56f2ff11b79256154975c090c0a90a2e5bf" // "piazzatron/rostr"
	vectorNonce          = "000102030405060708090a0b"
	vectorPlaintext      = "Fix auth redirect loop"
	vectorCiphertext     = "AAECAwQFBgcICQoLtuTGah4xnxe72wn8sELq7SLH7hJAsJsfLF/H4Qt+kuW7K5tbqeg="
)

func vectorDerivedKeys(t *testing.T) DerivedKeys {
	t.Helper()
	key, err := ParseKey(vectorKey)
	if err != nil {
		t.Fatal(err)
	}
	derived, err := key.Derive()
	if err != nil {
		t.Fatal(err)
	}
	return derived
}

func TestKeyFormat(t *testing.T) {
	var key Key
	for i := range key {
		key[i] = byte(i)
	}
	if key.String() != vectorKey {
		t.Fatalf("got %s", key.String())
	}
	parsed, err := ParseKey("  " + vectorKey + "\n")
	if err != nil || parsed != key {
		t.Fatalf("round trip: %v %v", parsed, err)
	}

	for _, bad := range []string{"", "AAECAwQF", "rostr_sk_", "rostr_sk_AAEC", "rostr_sk_" + strings.Repeat("!", 43), "rostr_pk_" + vectorKey[9:]} {
		if _, err := ParseKey(bad); err == nil {
			t.Errorf("ParseKey(%q) accepted", bad)
		}
	}
}

func TestGenerateKey(t *testing.T) {
	a, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := GenerateKey()
	if a == b || a == (Key{}) {
		t.Fatal("keys aren't random")
	}
	if s := a.String(); !strings.HasPrefix(s, KeyPrefix) || len(s) != len(KeyPrefix)+43 {
		t.Fatalf("bad format %q", s)
	}
}

func TestDerivedKeysMatchVectors(t *testing.T) {
	d := vectorDerivedKeys(t)
	for name, pair := range map[string][2]string{
		"auth":        {hex.EncodeToString(d.AuthKey[:]), vectorAuthKey},
		"encrypt":     {hex.EncodeToString(d.EncryptKey[:]), vectorEncryptKey},
		"fingerprint": {hex.EncodeToString(d.FingerprintKey[:]), vectorFingerprintKey},
		"auth token":  {d.AuthToken(), vectorAuthToken},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s: got %s, want %s", name, pair[0], pair[1])
		}
	}
}

func TestFingerprintLowercasesRepo(t *testing.T) {
	d := vectorDerivedKeys(t)
	if got := d.Fingerprint("Piazzatron/Rostr"); got != vectorFingerprint {
		t.Fatalf("got %s, want %s", got, vectorFingerprint)
	}
	if d.Fingerprint("piazzatron/other") == vectorFingerprint {
		t.Fatal("different repos share a fingerprint")
	}
}

func TestEncryptMatchesVector(t *testing.T) {
	d := vectorDerivedKeys(t)
	nonce, _ := hex.DecodeString(vectorNonce)
	got, err := d.encryptWithNonce(vectorPlaintext, nonce)
	if err != nil || got != vectorCiphertext {
		t.Fatalf("got %s, %v; want %s", got, err, vectorCiphertext)
	}
	plaintext, err := d.Decrypt(vectorCiphertext)
	if err != nil || plaintext != vectorPlaintext {
		t.Fatalf("decrypt: %q, %v", plaintext, err)
	}
}

// TestAESGCMKnownAnswer is test case 14 of the GCM spec (McGrew and Viega): a zero key, nonce, and block.
func TestAESGCMKnownAnswer(t *testing.T) {
	var d DerivedKeys
	got, err := d.encryptWithNonce(string(make([]byte, 16)), make([]byte, 12))
	if err != nil {
		t.Fatal(err)
	}
	want := "000000000000000000000000" + "cea7403d4d606b6e074ec5d3baf39d18" + "d0d1c8a799996bf0265b98b5d48ab919"
	raw, _ := hex.DecodeString(want)
	if got != base64.StdEncoding.EncodeToString(raw) {
		t.Fatalf("got %s", got)
	}
}

func TestEncryptRoundTripsWithFreshNonces(t *testing.T) {
	d := vectorDerivedKeys(t)
	for _, plaintext := range []string{"", "piazzatron/rostr", "🧙 Gandalf · Fix auth redirect loop", strings.Repeat("x", 5000)} {
		a, err := d.Encrypt(plaintext)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := d.Encrypt(plaintext)
		if a == b {
			t.Error("same ciphertext twice: nonce reused")
		}
		if got, err := d.Decrypt(a); err != nil || got != plaintext {
			t.Errorf("round trip %q: %q, %v", plaintext, got, err)
		}
	}
}

func TestDecryptRejectsTamperingAndWrongKeys(t *testing.T) {
	d := vectorDerivedKeys(t)
	other, _ := GenerateKey()
	otherDerived, _ := other.Derive()

	if _, err := otherDerived.Decrypt(vectorCiphertext); err == nil {
		t.Error("decrypted with the wrong key")
	}
	tampered := []byte(vectorCiphertext)
	tampered[20] ^= 1
	for _, bad := range []string{string(tampered), "not base64!", "AAAA"} {
		if _, err := d.Decrypt(bad); err == nil {
			t.Errorf("Decrypt(%q) accepted", bad)
		}
	}
}
