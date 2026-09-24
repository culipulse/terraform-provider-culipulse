// Package urlhost reproduces the WHATWG URL "host" serialization the CuliPulse server stores
// for a webhook channel (src/lib/channel-service.ts:126, `parsedUrl.host` on a `new URL(...)`).
// It is shared by internal/provider (the post-import host guard, K1) and internal/fakeapi (so
// the fake mirrors the same value the real server would compute) — a tiny leaf package so
// neither side has to import the other.
package urlhost

import (
	"net/url"
	"strings"

	"golang.org/x/net/idna"
)

// defaultPort is the WHATWG default-port table for the two schemes a webhook `url` can use.
// WHATWG URL.host omits the port when it equals the scheme's default (https:443, http:80).
var defaultPort = map[string]string{
	"https": "443",
	"http":  "80",
}

// WHATWGHost returns u's host the way a WHATWG `URL.host` getter would: the hostname
// lowercased (IDN labels converted to their ASCII/punycode form via golang.org/x/net/idna,
// mirroring what the JS `URL` constructor does internally), followed by ":<port>" unless the
// port is empty or equals the scheme's default. IPv6 literals keep their brackets, e.g.
// "[::1]:8443", and are not run through idna (bracket-notation isn't a domain name).
func WHATWGHost(u *url.URL) string {
	host := u.Hostname()
	port := u.Port()

	isIPv6 := strings.Contains(host, ":")
	if isIPv6 {
		host = "[" + host + "]"
	} else {
		host = strings.ToLower(host)
		if ascii, err := idna.Lookup.ToASCII(host); err == nil {
			host = ascii
		}
		// on error, keep the lowercased hostname as-is (best-effort; unreachable for the
		// https-only, already-URL-parsed inputs this is used on in practice)
	}

	if port == "" || port == defaultPort[strings.ToLower(u.Scheme)] {
		return host
	}
	return host + ":" + port
}
