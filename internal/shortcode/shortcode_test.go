package shortcode

import "testing"

func TestGenerate(t *testing.T) {
	seen := make(map[string]bool)
	for range 1000 {
		c, err := Generate(DefaultLength)
		if err != nil {
			t.Fatal(err)
		}
		if len(c) != DefaultLength || !Valid(c) {
			t.Fatalf("invalid generated code %q", c)
		}
		if seen[c] {
			t.Fatalf("duplicate code %q in 1000 draws", c)
		}
		seen[c] = true
	}
}

func TestValid(t *testing.T) {
	cases := map[string]bool{
		"abc":                               true,
		"my-link_2":                         true,
		"ab":                                false, // too short
		"has space":                         false,
		"slash/y":                           false,
		"api":                               true,
		"abcdefghijklmnopqrstuvwxyz0123456": false, // 33 chars
	}
	for in, want := range cases {
		if got := Valid(in); got != want {
			t.Errorf("Valid(%q) = %v, want %v", in, got, want)
		}
	}
}
