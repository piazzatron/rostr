package store

import (
	"os"
	"testing"
)

// TestMain makes drafting predictable: always the first free member, in roster order.
func TestMain(m *testing.M) {
	pickIndex = func(int) int { return 0 }
	os.Exit(m.Run())
}
