package provider

import (
	"context"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

var (
	statusCodeRE     = regexp.MustCompile(`^\d{3}$`)
	statusWildcardRE = regexp.MustCompile(`(?i)^\dxx$`)
)

// isValidExpectedStatus mirrors src/lib/classify.ts's isValidExpectedStatus exactly (K2): split
// on ",", trim each token, drop empty tokens, require at least one surviving token, and every
// token must be a 3-digit status code or a case-insensitive "digit + xx" wildcard. The provider
// used to validate this with a single regex over the whole string, which was closer but not
// identical — the fake mirrors this same function (internal/fakeapi/fakeapi.go) so both sides of
// the unit tests accept/reject the same set the real server does.
func isValidExpectedStatus(expected string) bool {
	var tokens []string
	for _, t := range strings.Split(expected, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			tokens = append(tokens, t)
		}
	}
	if len(tokens) == 0 {
		return false
	}
	for _, t := range tokens {
		if !statusCodeRE.MatchString(t) && !statusWildcardRE.MatchString(t) {
			return false
		}
	}
	return true
}

// expectedStatusValidator replaces a single whole-string regex (which rejected server-accepted
// values like a trailing comma, "200,") with the same token-by-token rule the server applies.
type expectedStatusValidator struct{}

func (expectedStatusValidator) Description(_ context.Context) string {
	return "comma-separated status codes like 200 or 2xx (case-insensitive)"
}

func (v expectedStatusValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (expectedStatusValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if !isValidExpectedStatus(req.ConfigValue.ValueString()) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Attribute",
			"expected_status must be one or more comma-separated status codes, each either a 3-digit code "+
				"(e.g. \"200\") or a case-insensitive wildcard like \"2xx\" (e.g. \"200, 301\" or \"2xx\").")
	}
}

func expectedStatusValid() validator.String { return expectedStatusValidator{} }
