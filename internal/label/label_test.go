package label

import "testing"

func TestBullet(t *testing.T) {
	for _, c := range []struct{ text, font, want string }{
		{"•", "Arial", "•"}, {"–", "Arial", "–"}, {"o", "Courier New", "o"}, {"-", "", "-"}, {"*", "", "*"},
		// In a symbol font a character is a code, in the private range or
		// out of it.
		{"\uf0b7", "Symbol", "•"}, {"·", "Symbol", "•"}, {"\uf0a7", "Wingdings", "▪"}, {"§", "Wingdings", "▪"},
		{"ü", "Wingdings", "✓"}, {"q", "Wingdings", "❑"}, {"\uf0d8", "wingdings", "⮚"}, {"v", "Wingdings", "❖"},
		{"\uf021", "Wingdings", "🖉"}, {"\uf0e0", "Wingdings 3", "🡶"}, {"\uf097", "Webdings", "🗣"},
		// A code the font has no character for, a private-use character
		// in an ordinary font, and nothing at all, are a plain bullet:
		// something stood there.
		{"\uf0ff", "Wingdings", "•"}, {"\ue000", "Arial", "•"}, {"", "Arial", "•"}, {"x", "Arial", "•"},
		// A ligature is not written as a bullet, nor is a control
		// character.
		{"\ufb01", "Arial", "•"}, {"\u0085", "Arial", "•"}, {"\u200b→", "Arial", "→"},
	} {
		if got := Bullet(c.text, c.font); got != c.want {
			t.Errorf("Bullet(%q, %q) = %q, want %q", c.text, c.font, got, c.want)
		}
	}
}

func TestNumerals(t *testing.T) {
	for n, want := range map[int]string{1: "I", 4: "IV", 9: "IX", 14: "XIV", 40: "XL", 1994: "MCMXCIV", 3999: "MMMCMXCIX", 0: "", 4000: "", -1: ""} {
		if got := Roman(n); got != want {
			t.Errorf("Roman(%d) = %q, want %q", n, got, want)
		}
	}
	for n, want := range map[int]string{1: "A", 26: "Z", 27: "AA", 28: "BB", 53: "AAA", 0: "", 781: ""} {
		if got := Letters(n); got != want {
			t.Errorf("Letters(%d) = %q, want %q", n, got, want)
		}
	}
}
