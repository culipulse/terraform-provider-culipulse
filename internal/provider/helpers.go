package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/culipulse/terraform-provider-culipulse/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// configureClient unpacks the provider's client from Resource/DataSource Configure data.
// data is nil during early validation; callers must tolerate a nil client there.
func configureClient(data any, diags *diag.Diagnostics) *client.Client {
	if data == nil {
		return nil
	}
	c, ok := data.(*client.Client)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", data))
		return nil
	}
	return c
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func known(v attr.Value) bool { return !v.IsNull() && !v.IsUnknown() }

func stringPtr(v types.String) *string {
	if !known(v) {
		return nil
	}
	s := v.ValueString()
	return &s
}

func int64Ptr(v types.Int64) *int64 {
	if !known(v) {
		return nil
	}
	i := v.ValueInt64()
	return &i
}

func boolPtr(v types.Bool) *bool {
	if !known(v) {
		return nil
	}
	b := v.ValueBool()
	return &b
}

func float64Ptr(v types.Float64) *float64 {
	if !known(v) {
		return nil
	}
	f := v.ValueFloat64()
	return &f
}

// optString maps an API string to state. nil and "" are both "not set" — the API stores a
// cleared body_match as null but a created-with-"" one as "" (monitor-service.ts:428,586).
func optString(p *string) types.String {
	if p == nil || *p == "" {
		return types.StringNull()
	}
	return types.StringValue(*p)
}

func optFloat64(p *float64) types.Float64 {
	if p == nil {
		return types.Float64Null()
	}
	return types.Float64Value(*p)
}

// noSurroundingWhitespaceValidator rejects a string with leading or trailing whitespace.
// CuliPulse's server trims channel names on create and update (channel-service.ts:57,186),
// so a config value with surrounding whitespace would never match what's read back and would
// diff forever. Shared identifier: noSurroundingWhitespace() — used by
// culipulse_webhook_channel's `name` (Task 8) and reused by Task 9's culipulse_channel_routing
// and the culipulse_channel data source.
type noSurroundingWhitespaceValidator struct{}

func (noSurroundingWhitespaceValidator) Description(_ context.Context) string {
	return "must not have leading or trailing whitespace"
}

func (v noSurroundingWhitespaceValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (noSurroundingWhitespaceValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	if strings.TrimSpace(s) != s {
		resp.Diagnostics.AddAttributeError(req.Path, "Name has leading or trailing whitespace",
			"CuliPulse trims whitespace from channel names on the server, so a name with leading or trailing "+
				"whitespace would never match what's read back and would show as a permanent difference. "+
				"Remove the extra whitespace.")
	}
}

func noSurroundingWhitespace() validator.String { return noSurroundingWhitespaceValidator{} }

// monitorTypeArticle renders a monitor type with the article a sentence needs (F5): "an HTTP"
// (an initialism, read letter by letter, starting with a vowel sound) vs "a heartbeat".
func monitorTypeArticle(monitorType string) string {
	if monitorType == "http" {
		return "an HTTP"
	}
	return "a " + monitorType
}
