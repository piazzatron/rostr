// Package crypto implements the rostr key and what's derived from it: the auth token sent to the API, the key that
// encrypts tasks and repo names, and the key that fingerprints repos so the server can match project links without
// seeing their names.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// KeyPrefix starts every rostr key.
const KeyPrefix = "rostr_sk_"

const nonceSize = 12

// Key is a rostr key's 256 random bits.
type Key [32]byte

// DerivedKeys are the three keys HKDF-SHA256 derives from a rostr key (no salt; info "auth", "encrypt",
// "fingerprint").
type DerivedKeys struct {
	AuthKey        [32]byte
	EncryptKey     [32]byte
	FingerprintKey [32]byte
}

// GenerateKey makes a new random rostr key.
func GenerateKey() (Key, error) {
	var k Key
	_, err := rand.Read(k[:])
	return k, err
}

// String formats the key as "rostr_sk_" plus its bytes in unpadded base64url.
func (k Key) String() string {
	return KeyPrefix + base64.RawURLEncoding.EncodeToString(k[:])
}

// ParseKey parses a key formatted by Key.String, ignoring surrounding whitespace.
func ParseKey(s string) (Key, error) {
	var k Key
	encoded, found := strings.CutPrefix(strings.TrimSpace(s), KeyPrefix)
	if !found {
		return k, fmt.Errorf("a rostr key starts with %q", KeyPrefix)
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) != len(k) {
		return k, errors.New("that isn't a valid rostr key")
	}
	copy(k[:], raw)
	return k, nil
}

// Derive computes the auth, encryption, and fingerprint keys.
func (k Key) Derive() (DerivedKeys, error) {
	var d DerivedKeys
	for _, target := range []struct {
		info string
		out  *[32]byte
	}{{"auth", &d.AuthKey}, {"encrypt", &d.EncryptKey}, {"fingerprint", &d.FingerprintKey}} {
		derived, err := hkdf.Key(sha256.New, k[:], nil, target.info, 32)
		if err != nil {
			return d, err
		}
		copy(target.out[:], derived)
	}
	return d, nil
}

// AuthToken is the bearer token for the API: the derived auth key in unpadded base64url. The server stores only its
// SHA-256 hash.
func (d DerivedKeys) AuthToken() string {
	return base64.RawURLEncoding.EncodeToString(d.AuthKey[:])
}

// Encrypt seals a task or repo name with AES-256-GCM under a random 96-bit nonce, returning standard base64 of the
// nonce followed by the ciphertext and tag.
func (d DerivedKeys) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return d.encryptWithNonce(plaintext, nonce)
}

// Decrypt opens a value made by Encrypt.
func (d DerivedKeys) Decrypt(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decoding encrypted value: %w", err)
	}
	if len(raw) < nonceSize {
		return "", errors.New("encrypted value is too short")
	}
	gcm, err := d.gcm()
	if err != nil {
		return "", err
	}
	plaintext, err := gcm.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", errors.New("can't decrypt: wrong key or corrupted value")
	}
	return string(plaintext), nil
}

// Fingerprint is the lowercase hex HMAC-SHA256 of the lowercased "owner/repo", which the server matches project
// links on.
func (d DerivedKeys) Fingerprint(repo string) string {
	mac := hmac.New(sha256.New, d.FingerprintKey[:])
	mac.Write([]byte(strings.ToLower(repo)))
	return hex.EncodeToString(mac.Sum(nil))
}

func (d DerivedKeys) encryptWithNonce(plaintext string, nonce []byte) (string, error) {
	gcm, err := d.gcm()
	if err != nil {
		return "", err
	}
	sealed := gcm.Seal(append([]byte{}, nonce...), nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (d DerivedKeys) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(d.EncryptKey[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
