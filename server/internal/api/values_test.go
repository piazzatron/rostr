package api

import "testing"

func TestNameKeyIgnoresCaseAndAccents(t *testing.T) {
	t.Parallel()
	for _, pair := range [][2]string{{"Éowyn", "eowyn"}, {"Pokémon", "POKEMON"}, {" Zoë ", "zoe"}, {"Ñandú", "nandu"}} {
		if nameKey(pair[0]) != nameKey(pair[1]) {
			t.Errorf("%q and %q don't match", pair[0], pair[1])
		}
	}
	if nameKey("Gandalf") == nameKey("Gandalf 2") {
		t.Error("copy numbers matched")
	}
}

func TestExpiryParsingAndFormatting(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{"48h": "2d", "7d": "7d", "2w": "14d", "36h": "36h", "1h": "1h", "52w": "364d"} {
		hours, err := parseExpiryHours(input)
		if err != nil || formatExpiryHours(hours) != want {
			t.Errorf("%s: got %s (%v), want %s", input, formatExpiryHours(hours), err, want)
		}
	}
	for _, input := range []string{"", "0h", "7", "7 d", "-1d", "1.5d", "53w", "1m"} {
		if _, err := parseExpiryHours(input); err == nil {
			t.Errorf("%q parsed", input)
		}
	}
}
