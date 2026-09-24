package urlhost

import (
	"net/url"
	"testing"
)

// K1: table test pinned in the known-issues-fix-brief — WHATWGHost must reproduce WHATWG
// URL.host exactly as the JS `URL` constructor computes it (src/lib/channel-service.ts:126).
func TestWHATWGHost(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://Hooks.Example.com/a", "hooks.example.com"},
		{"https://h.example.com:443/a", "h.example.com"},
		{"https://h.example.com:8443/a", "h.example.com:8443"},
		{"https://[::1]:8443/x", "[::1]:8443"},
		{"https://bücher.example/", "xn--bcher-kva.example"},
		{"http://h.example.com:80/a", "h.example.com"},
		{"http://h.example.com:8080/a", "h.example.com:8080"},
	}
	for _, c := range cases {
		u, err := url.Parse(c.in)
		if err != nil {
			t.Fatalf("url.Parse(%q): %v", c.in, err)
		}
		if got := WHATWGHost(u); got != c.want {
			t.Errorf("WHATWGHost(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
