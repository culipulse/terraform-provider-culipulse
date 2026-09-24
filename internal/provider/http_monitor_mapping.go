package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/culipulse/terraform-provider-culipulse/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type httpMonitorModel struct {
	ID                types.String  `tfsdk:"id"`
	Name              types.String  `tfsdk:"name"`
	URL               types.String  `tfsdk:"url"`
	IntervalSeconds   types.Int64   `tfsdk:"interval_seconds"`
	TimeoutMS         types.Int64   `tfsdk:"timeout_ms"`
	Method            types.String  `tfsdk:"method"`
	ExpectedStatus    types.String  `tfsdk:"expected_status"`
	BodyMatch         types.String  `tfsdk:"body_match"`
	FollowRedirects   types.Bool    `tfsdk:"follow_redirects"`
	DownAfterFailures types.Int64   `tfsdk:"down_after_failures"`
	UpAfterSuccesses  types.Int64   `tfsdk:"up_after_successes"`
	DownMinSources    types.Int64   `tfsdk:"down_min_sources"`
	SLATarget         types.Float64 `tfsdk:"sla_target"`
	AgentIDs          types.Set     `tfsdk:"agent_ids"`
	Headers           types.Map     `tfsdk:"headers"`
	SecretHeaders     types.Map     `tfsdk:"secret_headers"`
	BasicAuthUsername types.String  `tfsdk:"basic_auth_username"`
	BasicAuthPassword types.String  `tfsdk:"basic_auth_password"`
	BearerToken       types.String  `tfsdk:"bearer_token"`
	RequestBody       types.String  `tfsdk:"request_body"`
	ContentType       types.String  `tfsdk:"content_type"`
	Assertions        types.List    `tfsdk:"assertions"`
	Status            types.String  `tfsdk:"status"`
}

type assertionModel struct {
	Source types.String `tfsdk:"source"`
	Op     types.String `tfsdk:"op"`
	Value  types.String `tfsdk:"value"`
	Path   types.String `tfsdk:"path"`
	Name   types.String `tfsdk:"name"`
}

var assertionObjectType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"source": types.StringType, "op": types.StringType, "value": types.StringType,
	"path": types.StringType, "name": types.StringType,
}}

// httpMonitorWrite builds the POST/PATCH body from a fully-known plan.
func httpMonitorWrite(ctx context.Context, m httpMonitorModel, create bool) (*client.MonitorWrite, diag.Diagnostics) {
	var diags diag.Diagnostics
	w := &client.MonitorWrite{
		Name:              m.Name.ValueString(),
		Target:            stringPtr(m.URL),
		IntervalSeconds:   m.IntervalSeconds.ValueInt64(),
		TimeoutMS:         int64Ptr(m.TimeoutMS),
		Method:            stringPtr(m.Method),
		ExpectedStatus:    stringPtr(m.ExpectedStatus),
		BodyMatch:         stringPtr(m.BodyMatch),
		FollowRedirects:   boolPtr(m.FollowRedirects),
		DownAfterFailures: int64Ptr(m.DownAfterFailures),
		UpAfterSuccesses:  int64Ptr(m.UpAfterSuccesses),
		// Unknown (user didn't set it and agent_ids changed) → omitted → server default quorum.
		// Known → always sent: omitting it on PATCH resets the quorum (monitor-service.ts:565).
		DownMinSources: int64Ptr(m.DownMinSources),
		SLATarget:      float64Ptr(m.SLATarget), // nil → JSON null → cleared
	}
	if create {
		w.Type = "http"
	} else if w.BodyMatch == nil {
		empty := ""
		w.BodyMatch = &empty // PATCH: "" clears; omitting keeps the old value (monitor-service.ts:586)
	}
	diags.Append(m.AgentIDs.ElementsAs(ctx, &w.AgentSources, false)...)
	sort.Strings(w.AgentSources)

	// check_spec is always sent, built from the full desired state: PATCH replaces it
	// wholesale, and omitting it wipes headers/auth/body/assertions (monitor-service.ts:619-633).
	spec := &client.CheckSpec{}
	req := &client.RequestSpec{}
	if known(m.Headers) {
		diags.Append(m.Headers.ElementsAs(ctx, &req.Headers, false)...)
	}
	if known(m.SecretHeaders) {
		diags.Append(m.SecretHeaders.ElementsAs(ctx, &req.SecretHeaders, false)...)
	}
	switch {
	case known(m.BearerToken):
		req.Auth = &client.AuthSpec{Type: "bearer", Token: m.BearerToken.ValueString()}
	case known(m.BasicAuthUsername):
		req.Auth = &client.AuthSpec{Type: "basic", Username: m.BasicAuthUsername.ValueString(), Password: m.BasicAuthPassword.ValueString()}
	}
	req.Body = stringPtr(m.RequestBody)
	req.ContentType = stringPtr(m.ContentType)
	if req.Headers != nil || req.SecretHeaders != nil || req.Auth != nil || req.Body != nil || req.ContentType != nil {
		spec.Request = req
	}
	if known(m.Assertions) {
		var as []assertionModel
		diags.Append(m.Assertions.ElementsAs(ctx, &as, false)...)
		for _, a := range as {
			spec.Assertions = append(spec.Assertions, client.Assertion{
				Source: a.Source.ValueString(), Op: a.Op.ValueString(),
				Value: stringPtr(a.Value), Path: stringPtr(a.Path), Name: stringPtr(a.Name),
			})
		}
	}
	w.CheckSpec = spec
	return w, diags
}

// applyHTTPMonitor copies what the API can return into m. Write-only attributes
// (secret_headers, bearer_token, basic_auth_*) are left as they are: reads only return
// header names and the auth type (src/api.ts:80-88), so the last value Terraform sent stays
// in state and out-of-band edits to secrets are not detected (spec §4).
func applyHTTPMonitor(ctx context.Context, api *client.Monitor, m *httpMonitorModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if api.Type != "http" {
		diags.AddError("Wrong monitor type", fmt.Sprintf(
			"Monitor %s is %s monitor, not an HTTP monitor. Manage it with the matching culipulse_%s_monitor resource, if the provider has one.",
			api.ID, monitorTypeArticle(api.Type), api.Type))
		return diags
	}
	m.ID = types.StringValue(api.ID)
	m.Name = types.StringValue(api.Name)
	m.URL = types.StringValue(api.Target)
	m.IntervalSeconds = types.Int64Value(api.IntervalSeconds)
	m.TimeoutMS = types.Int64Value(api.TimeoutMS)
	m.Method = optString(api.Method)
	m.ExpectedStatus = optString(api.ExpectedStatus)
	m.BodyMatch = optString(api.BodyMatch)
	m.FollowRedirects = types.BoolValue(api.FollowRedirects != 0)
	m.DownAfterFailures = types.Int64Value(api.DownAfterFailures)
	m.UpAfterSuccesses = types.Int64Value(api.UpAfterSuccesses)
	m.DownMinSources = types.Int64Value(api.DownMinSources)
	m.SLATarget = optFloat64(api.SLATarget)
	m.Status = types.StringValue(api.Status)

	agents := append([]string{}, api.AgentSources...)
	sort.Strings(agents)
	ids, d := types.SetValueFrom(ctx, types.StringType, agents)
	diags.Append(d...)
	m.AgentIDs = ids

	var req client.RequestSpec
	var assertions []client.Assertion
	if api.CheckSpec != nil {
		if api.CheckSpec.Request != nil {
			req = *api.CheckSpec.Request
		}
		assertions = api.CheckSpec.Assertions
	}
	// A6.7: collapsing an empty map/list to null here (instead of an empty {}/[]) is only safe
	// because the schema's mapvalidator.SizeAtLeast(1) / listvalidator.SizeAtLeast(1) rule out a
	// config of `{}` or `[]` at plan time. Without those validators, a user writing `headers = {}`
	// would plan a known empty map but read back null — "Provider produced inconsistent result
	// after apply" — the same trap this function avoids for assertions below.
	if len(req.Headers) == 0 {
		m.Headers = types.MapNull(types.StringType)
	} else {
		v, d := types.MapValueFrom(ctx, types.StringType, req.Headers)
		diags.Append(d...)
		m.Headers = v
	}
	if !req.BodySecret { // an encrypted body can't be read back; keep what we have
		m.RequestBody = optString(req.Body)
	}
	m.ContentType = optString(req.ContentType)
	if len(assertions) == 0 {
		m.Assertions = types.ListNull(assertionObjectType)
	} else {
		as := make([]assertionModel, 0, len(assertions))
		for _, a := range assertions {
			as = append(as, assertionModel{
				Source: types.StringValue(a.Source), Op: types.StringValue(a.Op),
				Value: optString(a.Value), Path: optString(a.Path), Name: optString(a.Name),
			})
		}
		v, d := types.ListValueFrom(ctx, assertionObjectType, as)
		diags.Append(d...)
		m.Assertions = v
	}
	return diags
}

// secretDriftWarning is F2: after `terraform import` (or a console edit CuliPulse never told
// Terraform about), the API can hold secrets applyHTTPMonitor deliberately never overwrites —
// secret header NAMES, an auth type, or a secret request body — while the config/state models
// none of them. The next apply's PATCH always sends the full check_spec (global constraint),
// so anything CuliPulse holds that isn't in m is silently wiped with no diff shown. This is a
// pure function (api, the already-applied model) so it's unit-testable without a live resource
// run; it never returns a secret VALUE, only names/types.
func secretDriftWarning(api *client.Monitor, m httpMonitorModel) (string, bool) {
	if api.Type != "http" || api.CheckSpec == nil || api.CheckSpec.Request == nil {
		return "", false
	}
	req := api.CheckSpec.Request
	var held []string

	var missingHeaders []string
	for _, name := range req.SecretHeaderNames {
		if known(m.SecretHeaders) {
			if _, ok := m.SecretHeaders.Elements()[name]; ok {
				continue
			}
		}
		missingHeaders = append(missingHeaders, name)
	}
	if len(missingHeaders) > 0 {
		sort.Strings(missingHeaders)
		word := "header"
		if len(missingHeaders) > 1 {
			word = "headers"
		}
		held = append(held, fmt.Sprintf("secret %s %s", word, strings.Join(missingHeaders, ", ")))
	}
	if req.Auth != nil && !known(m.BearerToken) && !known(m.BasicAuthUsername) {
		held = append(held, fmt.Sprintf("%s authentication", req.Auth.Type))
	}
	if req.BodySecret && !known(m.RequestBody) {
		held = append(held, "a secret request body")
	}
	if len(held) == 0 {
		return "", false
	}
	return fmt.Sprintf(
		"CuliPulse still holds %s for this monitor that your configuration doesn't set. "+
			"The next apply will remove it from CuliPulse unless you add it to the configuration.",
		strings.Join(held, " and "),
	), true
}
