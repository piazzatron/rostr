package app

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteTableCountsEveryEmojiAsTwoColumns(t *testing.T) {
	var out bytes.Buffer
	writeTable(&out, "", [][]string{{"💍", "Fellowship"}, {"⚡", "Hogwarts"}, {"🛡️", "Avengers"}, {"🏛️", "Olympus"}, {"🧑‍💻", "CS Heroes"}})
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		emoji, _, _ := strings.Cut(line, " ")
		if gap := len(line) - len(strings.TrimLeft(line[len(emoji):], " ")) - len(emoji); gap != 3 {
			t.Fatalf("%q: %d spaces after the emoji, want 3", line, gap)
		}
	}
	if cellWidth("🛡️ Boromir") != 10 || cellWidth("Éowyn") != 5 {
		t.Fatalf("widths %d %d", cellWidth("🛡️ Boromir"), cellWidth("Éowyn"))
	}
}
