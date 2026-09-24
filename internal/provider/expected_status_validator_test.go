package provider

import "testing"

// K2: isValidExpectedStatus must mirror src/lib/classify.ts's isValidExpectedStatus exactly:
// split on ",", trim each token, drop empty tokens, require at least one token, and each
// surviving token must be a 3-digit code or a case-insensitive "digit + xx" wildcard.
func TestIsValidExpectedStatus(t *testing.T) {
	accepted := []string{
		"200", "2xx", "2XX", "2Xx", "200,301", "200, 301", " 2XX , 301 ",
		"200,", ",200", "200,,301",
	}
	rejected := []string{
		"", ",", " , ", "20x", "2000", "abc", "xx", "2x",
	}
	for _, v := range accepted {
		if !isValidExpectedStatus(v) {
			t.Errorf("isValidExpectedStatus(%q) = false, want true", v)
		}
	}
	for _, v := range rejected {
		if isValidExpectedStatus(v) {
			t.Errorf("isValidExpectedStatus(%q) = true, want false", v)
		}
	}
}
