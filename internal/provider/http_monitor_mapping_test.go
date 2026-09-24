package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/culipulse/terraform-provider-culipulse/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// baseHTTPModel is a plan with only the required attributes set; everything else null or,
// for Optional+Computed attributes, unknown — exactly what Terraform plans on create.
func baseHTTPModel() httpMonitorModel {
	return httpMonitorModel{
		ID: types.StringUnknown(), Name: types.StringValue("api"), URL: types.StringValue("https://e.com"),
		IntervalSeconds: types.Int64Value(300), TimeoutMS: types.Int64Unknown(), Method: types.StringUnknown(),
		ExpectedStatus: types.StringUnknown(), BodyMatch: types.StringNull(), FollowRedirects: types.BoolUnknown(),
		DownAfterFailures: types.Int64Unknown(), UpAfterSuccesses: types.Int64Unknown(), DownMinSources: types.Int64Unknown(),
		SLATarget: types.Float64Null(),
		AgentIDs:  types.SetValueMust(types.StringType, []attr.Value{types.StringValue("agt_sg")}),
		Headers:   types.MapNull(types.StringType), SecretHeaders: types.MapNull(types.StringType),
		BasicAuthUsername: types.StringNull(), BasicAuthPassword: types.StringNull(), BearerToken: types.StringNull(),
		RequestBody: types.StringNull(), ContentType: types.StringNull(),
		Assertions: types.ListNull(assertionObjectType), Status: types.StringUnknown(),
	}
}

func writeJSONMap(t *testing.T, m httpMonitorModel, create bool) map[string]any {
	t.Helper()
	w, diags := httpMonitorWrite(context.Background(), m, create)
	if diags.HasError() {
		t.Fatal(diags)
	}
	b, _ := json.Marshal(w)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func TestHTTPMonitorWrite_createMinimal(t *testing.T) {
	body := writeJSONMap(t, baseHTTPModel(), true)
	if body["type"] != "http" || body["target"] != "https://e.com" || body["interval_seconds"] != float64(300) {
		t.Fatalf("%v", body)
	}
	for _, k := range []string{"timeout_ms", "method", "expected_status", "follow_redirects", "down_after_failures",
		"up_after_successes", "down_min_sources", "body_match"} {
		if _, ok := body[k]; ok {
			t.Errorf("%s must be omitted on create when unset (server default applies)", k)
		}
	}
	if v, ok := body["sla_target"]; !ok || v != nil {
		t.Errorf("sla_target must be sent as null")
	}
	if cs, ok := body["check_spec"].(map[string]any); !ok || len(cs) != 0 {
		t.Errorf("check_spec must be sent as {}, got %v", body["check_spec"])
	}
	if ids := body["agent_sources"].([]any); len(ids) != 1 || ids[0] != "agt_sg" {
		t.Errorf("agent_sources = %v", ids)
	}
}

func TestHTTPMonitorWrite_updateClearsAndCarriesFullCheckSpec(t *testing.T) {
	ctx := context.Background()
	m := baseHTTPModel()
	m.ID = types.StringValue("mon_1")
	m.DownMinSources = types.Int64Value(1)
	m.Headers, _ = types.MapValueFrom(ctx, types.StringType, map[string]string{"X-Env": "t"})
	m.SecretHeaders, _ = types.MapValueFrom(ctx, types.StringType, map[string]string{"X-Key": "s"})
	m.BearerToken = types.StringValue("tok")
	m.RequestBody = types.StringValue(`{"a":1}`)
	m.ContentType = types.StringValue("application/json")
	m.Assertions, _ = types.ListValueFrom(ctx, assertionObjectType, []assertionModel{{
		Source: types.StringValue("json_body"), Op: types.StringValue("equals"),
		Path: types.StringValue("$.ok"), Value: types.StringValue("true"), Name: types.StringNull(),
	}})
	body := writeJSONMap(t, m, false)
	if _, ok := body["type"]; ok {
		t.Error("PATCH must not send type")
	}
	if body["body_match"] != "" {
		t.Errorf("unset body_match must be sent as \"\" on PATCH to clear it, got %v", body["body_match"])
	}
	if body["down_min_sources"] != float64(1) {
		t.Errorf("down_min_sources must always be sent on PATCH when known, got %v", body["down_min_sources"])
	}
	req := body["check_spec"].(map[string]any)["request"].(map[string]any)
	if req["headers"].(map[string]any)["X-Env"] != "t" || req["secretHeaders"].(map[string]any)["X-Key"] != "s" {
		t.Errorf("headers/secretHeaders missing: %v", req)
	}
	if auth := req["auth"].(map[string]any); auth["type"] != "bearer" || auth["token"] != "tok" {
		t.Errorf("auth = %v", auth)
	}
	if req["body"] != `{"a":1}` || req["contentType"] != "application/json" {
		t.Errorf("body/contentType = %v", req)
	}
	a := body["check_spec"].(map[string]any)["assertions"].([]any)[0].(map[string]any)
	if a["source"] != "json_body" || a["path"] != "$.ok" || a["value"] != "true" {
		t.Errorf("assertion = %v", a)
	}
	if _, ok := a["name"]; ok {
		t.Error("null assertion name must be omitted")
	}
}

func TestHTTPMonitorWrite_basicAuth(t *testing.T) {
	m := baseHTTPModel()
	m.BasicAuthUsername = types.StringValue("u")
	m.BasicAuthPassword = types.StringValue("p")
	body := writeJSONMap(t, m, true)
	auth := body["check_spec"].(map[string]any)["request"].(map[string]any)["auth"].(map[string]any)
	if auth["type"] != "basic" || auth["username"] != "u" || auth["password"] != "p" {
		t.Fatalf("auth = %v", auth)
	}
}

func sampleAPIMonitor() *client.Monitor {
	s := func(v string) *string { return &v }
	sla := 99.5
	return &client.Monitor{
		ID: "mon_1", Name: "api", Type: "http", Target: "https://e.com", IntervalSeconds: 300, TimeoutMS: 5000,
		Method: s("HEAD"), ExpectedStatus: s("200"), BodyMatch: s(""), FollowRedirects: 0,
		DownAfterFailures: 3, UpAfterSuccesses: 2, DownMinSources: 1, SLATarget: &sla, Status: "up",
		AgentSources: []string{"agt_sg"},
		CheckSpec: &client.CheckSpec{
			Request: &client.RequestSpec{
				Headers: map[string]string{"X-Env": "t"}, SecretHeaderNames: []string{"X-Key"},
				Auth: &client.AuthSpec{Type: "bearer"}, ContentType: s("text/plain"),
			},
			Assertions: []client.Assertion{{Source: "response_time", Op: "lt", Value: s("2000")}},
		},
	}
}

func TestApplyHTTPMonitor_readableFieldsAndWriteOnlyKept(t *testing.T) {
	ctx := context.Background()
	m := baseHTTPModel()
	m.SecretHeaders, _ = types.MapValueFrom(ctx, types.StringType, map[string]string{"X-Key": "s"})
	m.BearerToken = types.StringValue("tok")
	if diags := applyHTTPMonitor(ctx, sampleAPIMonitor(), &m); diags.HasError() {
		t.Fatal(diags)
	}
	if m.ID.ValueString() != "mon_1" || m.Method.ValueString() != "HEAD" || m.TimeoutMS.ValueInt64() != 5000 {
		t.Errorf("%+v", m)
	}
	if m.FollowRedirects.ValueBool() {
		t.Error("follow_redirects 0 must map to false")
	}
	if !m.BodyMatch.IsNull() {
		t.Error(`body_match "" must map to null`)
	}
	if m.SLATarget.ValueFloat64() != 99.5 || m.Status.ValueString() != "up" {
		t.Errorf("%+v", m)
	}
	if m.Headers.Elements()["X-Env"].(types.String).ValueString() != "t" {
		t.Error("headers not read")
	}
	if m.ContentType.ValueString() != "text/plain" || !m.RequestBody.IsNull() {
		t.Error("content_type/request_body not read")
	}
	if len(m.Assertions.Elements()) != 1 {
		t.Error("assertions not read")
	}
	// write-only: the API can't return them, so the planned/state values stay.
	if m.BearerToken.ValueString() != "tok" || len(m.SecretHeaders.Elements()) != 1 {
		t.Error("write-only fields were overwritten")
	}
}

func TestApplyHTTPMonitor_emptyCollectionsAreNull(t *testing.T) {
	ctx := context.Background()
	api := sampleAPIMonitor()
	api.CheckSpec = nil // also covers the api.CheckSpec == nil branch
	m := baseHTTPModel()
	// Start from STALE non-null state (as if a prior apply, or out-of-band removal on the
	// server, left these set) so the test actually exercises applyHTTPMonitor resetting them,
	// not just baseHTTPModel()'s own already-null defaults passing through untouched.
	var d diag.Diagnostics
	m.Headers, d = types.MapValueFrom(ctx, types.StringType, map[string]string{"X-Old": "stale"})
	if d.HasError() {
		t.Fatal(d)
	}
	m.ContentType = types.StringValue("text/stale")
	m.Assertions, d = types.ListValueFrom(ctx, assertionObjectType, []assertionModel{{
		Source: types.StringValue("json_body"), Op: types.StringValue("equals"),
		Path: types.StringValue("$.old"), Value: types.StringValue("true"), Name: types.StringNull(),
	}})
	if d.HasError() {
		t.Fatal(d)
	}

	if diags := applyHTTPMonitor(ctx, api, &m); diags.HasError() {
		t.Fatal(diags)
	}
	if !m.Headers.IsNull() || !m.Assertions.IsNull() || !m.ContentType.IsNull() {
		t.Errorf("empty collections must be reset to null when the API returns none (even from stale non-null state), got headers=%v assertions=%v content_type=%v", m.Headers, m.Assertions, m.ContentType)
	}
}

func TestApplyHTTPMonitor_secretBodyKeepsState(t *testing.T) {
	api := sampleAPIMonitor()
	api.CheckSpec.Request.BodySecret = true
	m := baseHTTPModel()
	m.RequestBody = types.StringValue("from-state")
	if diags := applyHTTPMonitor(context.Background(), api, &m); diags.HasError() {
		t.Fatal(diags)
	}
	if m.RequestBody.ValueString() != "from-state" {
		t.Error("an encrypted body can't be read back; the state value must stay")
	}
}

func TestApplyHTTPMonitor_rejectsOtherTypes(t *testing.T) {
	api := sampleAPIMonitor()
	api.Type = "heartbeat"
	m := baseHTTPModel()
	diags := applyHTTPMonitor(context.Background(), api, &m)
	if !diags.HasError() || !strings.Contains(diags.Errors()[0].Detail(), "is a heartbeat monitor") {
		t.Fatalf("want wrong-type error, got %v", diags)
	}
}

// F2: secretDriftWarning flags what CuliPulse holds that the config/state doesn't model — the
// case right after `terraform import` or a console edit, since the next PATCH always sends the
// whole check_spec and would silently wipe it. Pure function, unit-tested directly.

func apiWithRequest(req *client.RequestSpec) *client.Monitor {
	m := sampleAPIMonitor()
	m.CheckSpec = &client.CheckSpec{Request: req}
	return m
}

func TestSecretDriftWarning_noDriftWhenNothingSecretHeld(t *testing.T) {
	api := apiWithRequest(&client.RequestSpec{Headers: map[string]string{"X-Env": "t"}})
	if _, ok := secretDriftWarning(api, baseHTTPModel()); ok {
		t.Fatal("no secret held by the API; must not warn")
	}
}

func TestSecretDriftWarning_missingSecretHeader(t *testing.T) {
	api := apiWithRequest(&client.RequestSpec{SecretHeaderNames: []string{"X-Key"}})
	m := baseHTTPModel() // SecretHeaders null: config/state doesn't model it
	msg, ok := secretDriftWarning(api, m)
	if !ok || !strings.Contains(msg, "X-Key") {
		t.Fatalf("want a warning naming X-Key, got ok=%v msg=%q", ok, msg)
	}
}

func TestSecretDriftWarning_secretHeaderPresentInStateIsNotDrift(t *testing.T) {
	ctx := context.Background()
	api := apiWithRequest(&client.RequestSpec{SecretHeaderNames: []string{"X-Key"}})
	m := baseHTTPModel()
	var d diag.Diagnostics
	m.SecretHeaders, d = types.MapValueFrom(ctx, types.StringType, map[string]string{"X-Key": "s"})
	if d.HasError() {
		t.Fatal(d)
	}
	if _, ok := secretDriftWarning(api, m); ok {
		t.Fatal("X-Key is modeled in state; must not warn")
	}
}

func TestSecretDriftWarning_missingAuth(t *testing.T) {
	api := apiWithRequest(&client.RequestSpec{Auth: &client.AuthSpec{Type: "bearer"}})
	msg, ok := secretDriftWarning(api, baseHTTPModel())
	if !ok || !strings.Contains(msg, "bearer authentication") {
		t.Fatalf("want a warning naming bearer authentication, got ok=%v msg=%q", ok, msg)
	}
}

func TestSecretDriftWarning_authModeledInStateIsNotDrift(t *testing.T) {
	api := apiWithRequest(&client.RequestSpec{Auth: &client.AuthSpec{Type: "bearer"}})
	m := baseHTTPModel()
	m.BearerToken = types.StringValue("tok")
	if _, ok := secretDriftWarning(api, m); ok {
		t.Fatal("bearer_token is modeled in state; must not warn")
	}
	m2 := baseHTTPModel()
	m2.BasicAuthUsername = types.StringValue("u") // basic auth also counts as "modeled"
	if _, ok := secretDriftWarning(api, m2); ok {
		t.Fatal("basic_auth_username is modeled in state; must not warn")
	}
}

func TestSecretDriftWarning_missingSecretBody(t *testing.T) {
	api := apiWithRequest(&client.RequestSpec{BodySecret: true})
	msg, ok := secretDriftWarning(api, baseHTTPModel())
	if !ok || !strings.Contains(msg, "secret request body") {
		t.Fatalf("want a warning naming a secret request body, got ok=%v msg=%q", ok, msg)
	}
}

func TestSecretDriftWarning_secretBodyModeledInStateIsNotDrift(t *testing.T) {
	api := apiWithRequest(&client.RequestSpec{BodySecret: true})
	m := baseHTTPModel()
	m.RequestBody = types.StringValue("from-state")
	if _, ok := secretDriftWarning(api, m); ok {
		t.Fatal("request_body is modeled in state; must not warn")
	}
}

func TestSecretDriftWarning_neverIncludesAValue(t *testing.T) {
	api := apiWithRequest(&client.RequestSpec{
		SecretHeaderNames: []string{"X-Key"},
		Auth:              &client.AuthSpec{Type: "basic"},
		BodySecret:        true,
	})
	msg, ok := secretDriftWarning(api, baseHTTPModel())
	if !ok {
		t.Fatal("want a warning")
	}
	for _, secret := range []string{"s3cret", "tok", "from-state"} {
		if strings.Contains(msg, secret) {
			t.Fatalf("warning must never contain a secret value, got %q", msg)
		}
	}
	if !strings.Contains(msg, "X-Key") || !strings.Contains(msg, "basic authentication") || !strings.Contains(msg, "secret request body") {
		t.Fatalf("warning must name all three findings, got %q", msg)
	}
}

func TestSecretDriftWarning_notHTTPTypeIsNoOp(t *testing.T) {
	api := apiWithRequest(&client.RequestSpec{SecretHeaderNames: []string{"X-Key"}})
	api.Type = "heartbeat"
	if _, ok := secretDriftWarning(api, baseHTTPModel()); ok {
		t.Fatal("non-http monitor: secretDriftWarning must be a no-op (applyHTTPMonitor already rejects it)")
	}
}
