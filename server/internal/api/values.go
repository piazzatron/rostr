package api

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// member is one roster member, as stored in the members JSON column.
type member struct {
	Name  string `json:"name"`
	Emoji string `json:"emoji"`
}

// cast is a roster's emoji mode and members, as sent by create, edit, replace cast, and import.
type cast struct {
	EmojiMode string   `json:"emojiMode"`
	Members   []member `json:"members"`
}

const (
	maxNameRunes     = 64
	maxEmojiBytes    = 64
	maxMembers       = 200
	maxSessionBytes  = 256
	minCiphertextLen = 12 + 16 // nonce + GCM tag
	maxCiphertextLen = 16 << 10
)

// nameKey is how names are compared: case- and accent-insensitive, so "eowyn" matches "Éowyn".
func nameKey(name string) string {
	stripAccents := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	stripped, _, err := transform.String(stripAccents, strings.TrimSpace(name))
	if err != nil {
		stripped = name
	}
	return strings.ToLower(stripped)
}

// displayName is a drafted member's name with its copy number: "Gandalf", then "Gandalf 2".
func displayName(memberName string, copy int) string {
	if copy == 1 {
		return memberName
	}
	return memberName + " " + strconv.Itoa(copy)
}

// validateName trims a roster or member name and checks its length.
func validateName(field, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameRunes {
		return "", invalidRequest(fmt.Sprintf("%s must be 1 to %d characters.", field, maxNameRunes))
	}
	return name, nil
}

// validateCast returns the cast with trimmed names and the default emoji mode, or an error if it isn't valid.
func validateCast(c cast) (cast, error) {
	if c.EmojiMode == "" {
		c.EmojiMode = "on"
	}
	if c.EmojiMode != "on" && c.EmojiMode != "off" && c.EmojiMode != "only" {
		return cast{}, invalidRequest(`emojiMode must be "on", "off", or "only".`)
	}
	if len(c.Members) == 0 {
		return cast{}, invalidRequest("members needs at least one member.")
	}
	if len(c.Members) > maxMembers {
		return cast{}, invalidRequest(fmt.Sprintf("A roster can have at most %d members.", maxMembers))
	}

	seen := map[string]bool{}
	members := make([]member, len(c.Members))
	for i, m := range c.Members {
		name, err := validateName("Member name", m.Name)
		if err != nil {
			return cast{}, err
		}
		if m.Emoji == "" || len(m.Emoji) > maxEmojiBytes {
			return cast{}, invalidRequest(fmt.Sprintf("Member %q needs an emoji.", name))
		}
		if seen[nameKey(name)] {
			return cast{}, invalidRequest(fmt.Sprintf("Two members match %q (names ignore case and accents).", name))
		}
		seen[nameKey(name)] = true
		members[i] = member{Name: name, Emoji: m.Emoji}
	}
	return cast{EmojiMode: c.EmojiMode, Members: members}, nil
}

func validateSession(session string) error {
	if session == "" || len(session) > maxSessionBytes {
		return invalidRequest(fmt.Sprintf("session must be 1 to %d bytes.", maxSessionBytes))
	}
	return nil
}

var repoHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validateRepoHash(repoHash string) error {
	if !repoHashPattern.MatchString(repoHash) {
		return invalidRequest("repoHash must be 64 lowercase hex characters (HMAC-SHA256).")
	}
	return nil
}

// decodeCiphertext decodes an optional base64 AES-256-GCM value (nonce, then ciphertext and tag). nil stays nil.
func decodeCiphertext(field string, value *string) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(*value)
	if err != nil || len(decoded) < minCiphertextLen || len(decoded) > maxCiphertextLen {
		return nil, invalidRequest(fmt.Sprintf("%s must be base64 AES-256-GCM ciphertext (nonce, ciphertext, tag).", field))
	}
	return decoded, nil
}

func encodeCiphertext(value []byte) *string {
	if value == nil {
		return nil
	}
	encoded := base64.StdEncoding.EncodeToString(value)
	return &encoded
}

// repoInput validates an optional repoHash and repo ciphertext, which must come together.
func repoInput(repoHash, repo *string) (*string, []byte, error) {
	if (repoHash == nil) != (repo == nil) {
		return nil, nil, invalidRequest("repoHash and repo must be sent together.")
	}
	if repoHash == nil {
		return nil, nil, nil
	}
	if err := validateRepoHash(*repoHash); err != nil {
		return nil, nil, err
	}
	ciphertext, err := decodeCiphertext("repo", repo)
	return repoHash, ciphertext, err
}

var durationPattern = regexp.MustCompile(`^([1-9][0-9]{0,4})([hdw])$`)

var hoursPerDurationUnit = map[string]int{"h": 1, "d": 24, "w": 24 * 7}

const maxExpiryHours = 52 * 24 * 7

// parseExpiryHours parses a draft expiry like "48h", "7d", or "2w" into hours, between 1 hour and 52 weeks.
func parseExpiryHours(value string) (int, error) {
	match := durationPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, invalidRequest(`draftExpiry must be a number of hours, days, or weeks, like "48h", "7d", or "2w".`)
	}
	n, _ := strconv.Atoi(match[1])
	hours := n * hoursPerDurationUnit[match[2]]
	if hours > maxExpiryHours {
		return 0, invalidRequest("draftExpiry can be at most 52 weeks.")
	}
	return hours, nil
}

// formatExpiryHours formats hours as whole days when it can: 168 is "7d", 336 is "14d", 36 is "36h".
func formatExpiryHours(hours int) string {
	if hours%24 == 0 {
		return strconv.Itoa(hours/24) + "d"
	}
	return strconv.Itoa(hours) + "h"
}
