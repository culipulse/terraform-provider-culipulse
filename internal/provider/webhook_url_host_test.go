package provider

import (
	"testing"
)

// K1: checkWebhookURLHostMatches must compare the WHATWG host (hostname AND non-default port),
// not just url.Hostname() — otherwise a configured url pointed at the wrong port on the right
// hostname is silently accepted (Review Focus / known-issues-fix-brief K1).
func TestCheckWebhookURLHostMatches(t *testing.T) {
	strp := func(s string) *string { return &s }
	cases := []struct {
		name          string
		configuredURL string
		apiHost       *string
		wantErr       bool
	}{
		{
			name:          "matching host and port",
			configuredURL: "https://hooks.example.com:8443/a",
			apiHost:       strp("hooks.example.com:8443"),
			wantErr:       false,
		},
		{
			name:          "configured has a non-default port the api host lacks",
			configuredURL: "https://hooks.example.com:8443/a",
			apiHost:       strp("hooks.example.com"),
			wantErr:       true,
		},
		{
			name:          "configured default port drops to match a bare api host",
			configuredURL: "https://hooks.example.com:443/a",
			apiHost:       strp("hooks.example.com"),
			wantErr:       false,
		},
		{
			name:          "mismatched hostname still errors",
			configuredURL: "https://wrong.example.com/a",
			apiHost:       strp("hooks.example.com"),
			wantErr:       true,
		},
		{
			name:          "nil apiHost is allowed through",
			configuredURL: "https://anything.example.com/a",
			apiHost:       nil,
			wantErr:       false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			diags := checkWebhookURLHostMatches(c.configuredURL, c.apiHost)
			if diags.HasError() != c.wantErr {
				t.Fatalf("checkWebhookURLHostMatches(%q, %v) HasError = %v, want %v: %v",
					c.configuredURL, c.apiHost, diags.HasError(), c.wantErr, diags)
			}
		})
	}
}
