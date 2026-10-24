// Package roster holds the roster domain rules shared by every storage backend: members, loose name matching,
// emoji parsing and assignment, display per emoji mode, and cast validation.
package roster

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Member is one name on a roster.
type Member struct {
	Name  string `json:"name"`
	Emoji string `json:"emoji"`
}

// EmojiMode controls how a roster's agents are displayed.
type EmojiMode string

const (
	EmojiOn   EmojiMode = "on"
	EmojiOff  EmojiMode = "off"
	EmojiOnly EmojiMode = "only"
)

// ParseEmojiMode validates an emoji mode typed by the user.
func ParseEmojiMode(s string) (EmojiMode, error) {
	switch mode := EmojiMode(strings.ToLower(strings.TrimSpace(s))); mode {
	case EmojiOn, EmojiOff, EmojiOnly:
		return mode, nil
	}
	return "", fmt.Errorf("emoji mode must be on, off, or only (got %q)", s)
}

// NormalizeName folds case, accents, and spacing so "Éowyn", "eowyn", and " EOWYN " all match.
func NormalizeName(s string) string {
	stripAccents := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	folded, _, err := transform.String(stripAccents, s)
	if err != nil {
		folded = s
	}
	return strings.ToLower(strings.Join(strings.Fields(folded), " "))
}

// NamesMatch reports whether two names match loosely.
func NamesMatch(a, b string) bool {
	return NormalizeName(a) == NormalizeName(b)
}

// AgentName is a drafted member's name, with the copy number for copies past the first ("Gandalf 2").
func AgentName(member string, copy int) string {
	if copy <= 1 {
		return member
	}
	return member + " " + strconv.Itoa(copy)
}

// Display is how an agent shows up under the roster's emoji mode.
func Display(emoji, member string, copy int, mode EmojiMode) string {
	switch mode {
	case EmojiOff:
		return AgentName(member, copy)
	case EmojiOnly:
		if copy <= 1 {
			return emoji
		}
		return emoji + " " + strconv.Itoa(copy)
	default:
		return emoji + " " + AgentName(member, copy)
	}
}

// ParseMember parses "🧙 Gandalf" or "Gandalf". The emoji is empty when the text doesn't start with one.
func ParseMember(s string) Member {
	s = strings.TrimSpace(s)
	first, rest, found := strings.Cut(s, " ")
	if found && isEmoji(first) {
		return Member{Name: strings.TrimSpace(rest), Emoji: first}
	}
	return Member{Name: s}
}

// ParseNames parses a comma-separated --names list, giving every name without an emoji a random one that no other
// member uses.
func ParseNames(list string) ([]Member, error) {
	var members []Member
	for _, part := range strings.Split(list, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		members = append(members, ParseMember(part))
	}
	if len(members) == 0 {
		return nil, errors.New("--names needs at least one name")
	}
	return AssignMissingEmoji(members)
}

// AssignMissingEmoji returns a copy of members where every member without an emoji gets a random one, different from
// every other member's.
func AssignMissingEmoji(members []Member) ([]Member, error) {
	used := map[string]bool{}
	for _, m := range members {
		if m.Emoji != "" {
			used[emojiKey(m.Emoji)] = true
		}
	}

	var available []string
	for _, e := range emojiPool {
		if !used[emojiKey(e)] {
			available = append(available, e)
		}
	}
	rand.Shuffle(len(available), func(i, j int) { available[i], available[j] = available[j], available[i] })

	out := make([]Member, len(members))
	for i, m := range members {
		if m.Emoji == "" {
			if len(available) == 0 {
				return nil, errors.New("too many names without an emoji; give some of them one, like \"🧮 Ada\"")
			}
			m.Emoji, available = available[0], available[1:]
		}
		out[i] = m
	}
	return out, nil
}

// ValidateCast checks a roster's members: at least one, every name and emoji present, no two names that match
// loosely, and, in "only" mode, no two members with the same emoji.
func ValidateCast(members []Member, mode EmojiMode) error {
	if len(members) == 0 {
		return errors.New("a roster needs at least one member")
	}
	if _, err := ParseEmojiMode(string(mode)); err != nil {
		return err
	}

	names := map[string]string{}
	emoji := map[string]string{}
	for _, m := range members {
		if strings.TrimSpace(m.Name) == "" {
			return errors.New("every member needs a name")
		}
		if strings.TrimSpace(m.Emoji) == "" {
			return fmt.Errorf("%s needs an emoji", m.Name)
		}
		if other, ok := names[NormalizeName(m.Name)]; ok {
			return fmt.Errorf("%q and %q are the same name", other, m.Name)
		}
		names[NormalizeName(m.Name)] = m.Name

		if other, ok := emoji[emojiKey(m.Emoji)]; ok && mode == EmojiOnly {
			return fmt.Errorf("emoji mode \"only\" needs a different emoji for every member, but %s and %s share %s",
				other, m.Name, m.Emoji)
		}
		emoji[emojiKey(m.Emoji)] = m.Name
	}
	return nil
}

// emojiKey compares emoji while ignoring the variation selector, so "🛡️" and "🛡" count as the same.
func emojiKey(e string) string {
	return strings.ReplaceAll(strings.TrimSpace(e), "️", "")
}

// isEmoji reports whether a token looks like an emoji: no letters or digits, and at least one pictographic symbol.
func isEmoji(token string) bool {
	hasSymbol := false
	for _, r := range token {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
		if unicode.Is(unicode.So, r) || unicode.Is(unicode.Sk, r) || r >= 0x1F000 {
			hasSymbol = true
		}
	}
	return hasSymbol
}

// emojiPool is what names without an emoji are given, one each, at random.
var emojiPool = strings.Fields(`
🦊 🦉 🐙 🦄 🐝 🦋 🐢 🐬 🦁 🐯 🐼 🐨 🐸 🐧 🦅 🦆 🦩 🦚 🦜 🐳 🦈 🐞 🦀 🦔 🦦 🦥 🦘 🐘 🦒 🦓
🐊 🐍 🦎 🦖 🐉 🐲 🌵 🌲 🌴 🍀 🍁 🍄 🌻 🌷 🌹 🌸 🌼 🌙 🌞 🌈 🌊 🔥 💧 🍎 🍋 🍉 🍇 🍓 🍒 🍑
🥝 🥥 🥑 🌶 🥕 🌽 🥨 🧀 🍕 🍔 🌮 🍣 🍩 🍪 🎂 🍫 🍿 🧁 ☕ 🍵 🧃 ⚽ 🏀 🏈 ⚾ 🎾 🏐 🎱 🏓 🥊
🎯 🎳 🎮 🎲 🧩 🎨 🎭 🎸 🎺 🎻 🥁 🎷 🎤 🎧 📷 🔭 🔬 💡 🔦 📚 📌 📎 ✏ 🖋 📐 🧭 ⏰ ⌛ 🔑 🗝
🔨 🪓 🧲 🧪 🧬 🛸 🚀 🛶 ⛵ 🚲 🛹 🎈 🎁 🏆 🥇 💎 👑 🎩 🧢 🪁 🪐 ⭐ ☄ ⚡ ❄ ☂ 🌋 🗿 🏰 🗼
`)
