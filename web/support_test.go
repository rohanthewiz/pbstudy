package web

import "testing"

// TestSafeReturn covers the redirect guard on every form's "return" field.
//
// Local app or not, a request is a request: a link handed to the user could
// carry any return value, and Redirect will honour whatever it is given.
func TestSafeReturn(t *testing.T) {
	const fallback = "/notes"

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"a plain path is kept", "/verse/43/3/16", "/verse/43/3/16"},
		{"a path with a query is kept", "/verse/43/3/16?t=kjv", "/verse/43/3/16?t=kjv"},
		{"empty falls back", "", fallback},
		{"whitespace falls back", "   ", fallback},
		{"a relative path falls back", "notes/abc", fallback},
		// A browser reads "//host/path" as a protocol-relative URL, so this
		// is an off-site redirect wearing a path's clothing.
		{"protocol-relative falls back", "//evil.example/x", fallback},
		{"absolute http falls back", "http://evil.example/x", fallback},
		{"absolute https falls back", "https://evil.example/x", fallback},
		{"backslash falls back", `/\evil.example`, fallback},
		{"header injection falls back", "/notes\r\nSet-Cookie: x=1", fallback},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := safeReturn(c.in, fallback); got != c.want {
				t.Errorf("safeReturn(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestTranslationFromURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"reads the t parameter", "/verse/43/3/16?t=asv", "asv"},
		{"unknown translation falls back", "/verse/43/3/16?t=nonsense", "kjv"},
		{"no parameter falls back", "/verse/43/3/16", "kjv"},
		{"empty falls back", "", "kjv"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := translationFromURL(c.in, "kjv"); got != c.want {
				t.Errorf("translationFromURL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
