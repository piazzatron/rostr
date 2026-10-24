package roster

import (
	"strings"
	"testing"
)

func TestNamesMatchIgnoringCaseAccentsAndSpacing(t *testing.T) {
	for _, pair := range [][2]string{
		{"Éowyn", "eowyn"},
		{"Jokić", "JOKIC"},
		{"Nausicaä", "nausicaa"},
		{"Hašek", "hasek"},
		{"Donkey  Kong ", "donkey kong"},
		{"Gandalf 2", "gandalf 2"},
	} {
		if !NamesMatch(pair[0], pair[1]) {
			t.Errorf("%q and %q should match", pair[0], pair[1])
		}
	}
	for _, pair := range [][2]string{{"Gandalf", "Gandalf 2"}, {"Ron", "Rob"}, {"Luke", "Luka"}} {
		if NamesMatch(pair[0], pair[1]) {
			t.Errorf("%q and %q shouldn't match", pair[0], pair[1])
		}
	}
}

func TestParseMember(t *testing.T) {
	for in, want := range map[string]Member{
		"🧮 Ada":         {Name: "Ada", Emoji: "🧮"},
		"  Grace ":      {Name: "Grace"},
		"🦍 Donkey Kong": {Name: "Donkey Kong", Emoji: "🦍"},
		"🧑‍💻 Dev":       {Name: "Dev", Emoji: "🧑‍💻"},
		"🛡️ Boromir":    {Name: "Boromir", Emoji: "🛡️"},
		"R2-D2":         {Name: "R2-D2"},
		"Mary Jane":     {Name: "Mary Jane"},
		"3 Musketeers":  {Name: "3 Musketeers"},
		"O'Brien":       {Name: "O'Brien"},
		"♟️ Ron":        {Name: "Ron", Emoji: "♟️"},
		"⚡ Pikachu":     {Name: "Pikachu", Emoji: "⚡"},
		"🐦‍⬛ Itachi":    {Name: "Itachi", Emoji: "🐦‍⬛"},
	} {
		if got := ParseMember(in); got != want {
			t.Errorf("ParseMember(%q) = %+v, want %+v", in, got, want)
		}
	}
}

func TestParseNamesKeepsOrderAndAssignsUniqueEmoji(t *testing.T) {
	members, err := ParseNames("🧮 Ada, Grace, Alan, Margaret, Donald, Linus, Dennis, ,Barbara")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 8 || members[0] != (Member{Name: "Ada", Emoji: "🧮"}) || members[7].Name != "Barbara" {
		t.Fatalf("got %+v", members)
	}
	if err := ValidateCast(members, EmojiOnly); err != nil {
		t.Fatalf("assigned emoji aren't unique: %v", err)
	}

	if _, err := ParseNames(" , "); err == nil {
		t.Error("an empty list was accepted")
	}
}

func TestAssignMissingEmojiAvoidsTypedEmoji(t *testing.T) {
	// Typed emoji (with and without the variation selector) are never handed out again
	for range 50 {
		members := []Member{{Name: "A", Emoji: "🦊"}, {Name: "B", Emoji: "✏️"}}
		for _, n := range strings.Fields("C D E F G H I J K L M N O P") {
			members = append(members, Member{Name: n})
		}
		assigned, err := AssignMissingEmoji(members)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateCast(assigned, EmojiOnly); err != nil {
			t.Fatal(err)
		}
		if assigned[0].Emoji != "🦊" || members[2].Emoji != "" {
			t.Fatal("changed a typed emoji or mutated the input")
		}
	}
}

func TestAssignMissingEmojiRunsOut(t *testing.T) {
	members := make([]Member, len(emojiPool)+1)
	for i := range members {
		members[i] = Member{Name: strings.Repeat("x", i+1)}
	}
	if _, err := AssignMissingEmoji(members); err == nil {
		t.Fatal("expected an error with more names than emoji")
	}
}

func TestEmojiPoolIsUniqueEmoji(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range emojiPool {
		if seen[emojiKey(e)] || !isEmoji(e) {
			t.Errorf("pool entry %q is duplicated or not an emoji", e)
		}
		seen[emojiKey(e)] = true
	}
}

func TestValidateCast(t *testing.T) {
	ok := []Member{{Name: "Gandalf", Emoji: "🧙"}, {Name: "Frodo", Emoji: "💍"}}
	if err := ValidateCast(ok, EmojiOnly); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		members []Member
		mode    EmojiMode
	}{
		"no members":          {nil, EmojiOn},
		"loose duplicate":     {[]Member{{Name: "Éowyn", Emoji: "⚔️"}, {Name: "eowyn", Emoji: "🗡️"}}, EmojiOn},
		"missing name":        {[]Member{{Name: " ", Emoji: "⚔️"}}, EmojiOn},
		"missing emoji":       {[]Member{{Name: "Ada"}}, EmojiOff},
		"bad mode":            {ok, "sometimes"},
		"only, shared emoji":  {[]Member{{Name: "Pikachu", Emoji: "⚡"}, {Name: "Zeus", Emoji: "⚡"}}, EmojiOnly},
		"only, VS16 variants": {[]Member{{Name: "Boromir", Emoji: "🛡️"}, {Name: "Cap", Emoji: "🛡"}}, EmojiOnly},
	} {
		if err := ValidateCast(tc.members, tc.mode); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Shared emoji are fine outside "only" mode
	if err := ValidateCast([]Member{{Name: "Pikachu", Emoji: "⚡"}, {Name: "Zeus", Emoji: "⚡"}}, EmojiOn); err != nil {
		t.Error(err)
	}
}

func TestDisplay(t *testing.T) {
	for _, tc := range []struct {
		mode EmojiMode
		copy int
		want string
	}{
		{EmojiOn, 1, "🧙 Gandalf"},
		{EmojiOff, 1, "Gandalf"},
		{EmojiOnly, 1, "🧙"},
		{EmojiOn, 2, "🧙 Gandalf 2"},
		{EmojiOff, 3, "Gandalf 3"},
		{EmojiOnly, 2, "🧙 2"},
	} {
		if got := Display("🧙", "Gandalf", tc.copy, tc.mode); got != tc.want {
			t.Errorf("Display(%s, copy %d) = %q, want %q", tc.mode, tc.copy, got, tc.want)
		}
	}
}

func TestParseEmojiMode(t *testing.T) {
	if mode, err := ParseEmojiMode(" ONLY "); err != nil || mode != EmojiOnly {
		t.Fatalf("got %q, %v", mode, err)
	}
	if _, err := ParseEmojiMode("yes"); err == nil {
		t.Fatal("accepted yes")
	}
}
