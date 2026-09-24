package fakeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/culipulse/terraform-provider-culipulse/internal/client"
)

func newClient(t *testing.T, s *Server) *client.Client {
	t.Helper()
	c, err := client.New(client.Config{Endpoint: s.URL() + "/v1", Token: Token})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// NOTE (amendment A3.1): strp is NOT redeclared here — it is the package-level helper in
// fakeapi.go. Declaring it again here is a compile error (duplicate declaration).

func TestPatchWithoutCheckSpecWipesHTTPRequest(t *testing.T) {
	s := New(t)
	c := newClient(t, s)
	ctx := context.Background()
	id, err := c.CreateMonitor(ctx, &client.MonitorWrite{
		Name: "a", Type: "http", Target: strp("https://e.com"), IntervalSeconds: 300,
		AgentSources: []string{"agt_sg"},
		CheckSpec: &client.CheckSpec{Request: &client.RequestSpec{
			Headers: map[string]string{"X-A": "1"}, SecretHeaders: map[string]string{"X-K": "s"},
			Auth: &client.AuthSpec{Type: "bearer", Token: "t"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateMonitor(ctx, id, &client.MonitorWrite{Name: "a2", IntervalSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	if cs := s.StoredCheckSpec("a2"); cs.Request != nil {
		t.Fatalf("check_spec not wiped: %+v", cs.Request)
	}
}

func TestReadRedactsSecretsAndDefaults(t *testing.T) {
	s := New(t)
	c := newClient(t, s)
	ctx := context.Background()
	id, _ := c.CreateMonitor(ctx, &client.MonitorWrite{
		Name: "a", Type: "http", Target: strp("https://e.com"), IntervalSeconds: 300,
		AgentSources: []string{"agt_sg", "agt_eu"},
		CheckSpec: &client.CheckSpec{Request: &client.RequestSpec{
			SecretHeaders: map[string]string{"X-K": "s"},
			Auth:          &client.AuthSpec{Type: "basic", Username: "u", Password: "p"},
		}},
	})
	m, err := c.GetMonitor(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	r := m.CheckSpec.Request
	if len(r.SecretHeaders) != 0 || len(r.SecretHeaderNames) != 1 || r.SecretHeaderNames[0] != "X-K" {
		t.Fatalf("secret headers not redacted: %+v", r)
	}
	if r.Auth.Type != "basic" || r.Auth.Username != "" || r.Auth.Password != "" {
		t.Fatalf("auth not redacted: %+v", r.Auth)
	}
	if m.TimeoutMS != 10000 || *m.Method != "GET" || *m.ExpectedStatus != "2xx" || m.FollowRedirects != 1 ||
		m.DownAfterFailures != 2 || m.UpAfterSuccesses != 1 || m.DownMinSources != 2 || m.Status != "pending" {
		t.Fatalf("defaults wrong: %+v", m)
	}
}

func TestHTTPCreateNeedsAnAgent(t *testing.T) {
	s := New(t)
	_, err := newClient(t, s).CreateMonitor(context.Background(), &client.MonitorWrite{
		Name: "a", Type: "http", Target: strp("https://e.com"), IntervalSeconds: 300,
	})
	if err == nil {
		t.Fatal("http monitor without agent_sources accepted")
	}
}

func TestQuorumDefaultsAndRange(t *testing.T) {
	s := New(t)
	c := newClient(t, s)
	ctx := context.Background()
	id, _ := c.CreateMonitor(ctx, &client.MonitorWrite{Name: "q", Type: "http", Target: strp("https://e.com"),
		IntervalSeconds: 300, AgentSources: []string{"agt_sg", "agt_eu"}})
	two := int64(2)
	err := c.UpdateMonitor(ctx, id, &client.MonitorWrite{Name: "q", IntervalSeconds: 300,
		AgentSources: []string{"agt_sg"}, DownMinSources: &two})
	if err == nil {
		t.Fatal("down_min_sources 2 with 1 agent accepted")
	}
	if err := c.UpdateMonitor(ctx, id, &client.MonitorWrite{Name: "q", IntervalSeconds: 300, AgentSources: []string{"agt_sg"}}); err != nil {
		t.Fatal(err)
	}
	if got := s.StoredMonitor("q").DownMinSources; got != 1 {
		t.Fatalf("default quorum for 1 agent = %d", got)
	}
}

func TestHeartbeatTokenSurvivesPatch(t *testing.T) {
	s := New(t)
	c := newClient(t, s)
	ctx := context.Background()
	id, _ := c.CreateMonitor(ctx, &client.MonitorWrite{Name: "hb", Type: "heartbeat", IntervalSeconds: 300})
	before, _ := c.GetMonitor(ctx, id)
	if before.CheckSpec.Heartbeat == nil || before.CheckSpec.Heartbeat.Token == "" {
		t.Fatal("no heartbeat token on create")
	}
	if *before.HeartbeatGraceSeconds != 60 {
		t.Fatalf("grace default = %d", *before.HeartbeatGraceSeconds)
	}
	if err := c.UpdateMonitor(ctx, id, &client.MonitorWrite{Name: "hb", IntervalSeconds: 600}); err != nil {
		t.Fatal(err)
	}
	after, _ := c.GetMonitor(ctx, id)
	if after.CheckSpec.Heartbeat.Token != before.CheckSpec.Heartbeat.Token {
		t.Fatal("heartbeat token changed on PATCH")
	}
}

func TestPatchTypeChangeRejectedSameTypeAllowed(t *testing.T) {
	s := New(t)
	id := s.SeedMonitor("http", "a")
	body := func(typ string) *client.MonitorWrite {
		return &client.MonitorWrite{Name: "a", Type: typ, IntervalSeconds: 300}
	}
	c := newClient(t, s)
	if err := c.UpdateMonitor(context.Background(), id, body("heartbeat")); err == nil {
		t.Fatal("type change accepted")
	}
	if err := c.UpdateMonitor(context.Background(), id, body("http")); err != nil {
		t.Fatalf("same-type PATCH rejected: %v", err)
	}
}

func TestChannelRoutingSemantics(t *testing.T) {
	s := New(t)
	c := newClient(t, s)
	ctx := context.Background()
	m1 := s.SeedMonitor("http", "m1")
	ch := s.SeedChannel("telegram", "ops")
	f := false
	ids := []string{m1, "mon_foreign"}
	if err := c.UpdateChannel(ctx, ch, client.ChannelPatch{AllMonitors: &f, MonitorIDs: &ids}); err != nil {
		t.Fatal(err)
	}
	if all, got := s.Routing(ch); all || len(got) != 1 || got[0] != m1 {
		t.Fatalf("foreign id not dropped: all=%v ids=%v", all, got)
	}
	tr := true
	if err := c.UpdateChannel(ctx, ch, client.ChannelPatch{AllMonitors: &tr}); err != nil {
		t.Fatal(err)
	}
	read, _ := c.GetChannel(ctx, ch)
	if !read.AllMonitors || len(read.MonitorIDs) != 1 {
		t.Fatalf("all_monitors must not clear rows: %+v", read)
	}
	s.DeleteMonitorNamed("m1")
	if _, got := s.Routing(ch); len(got) != 0 {
		t.Fatalf("deleting a monitor must drop its routing rows: %v", got)
	}
}

func TestWebhookCreateAndDeletes(t *testing.T) {
	s := New(t)
	c := newClient(t, s)
	ctx := context.Background()
	if _, err := c.CreateChannel(ctx, client.ChannelCreate{Type: "webhook", Name: "h", URL: "http://insecure"}); err == nil {
		t.Fatal("http:// webhook accepted")
	}
	res, err := c.CreateChannel(ctx, client.ChannelCreate{Type: "webhook", Name: "h", URL: "https://hooks.example.com/x"})
	if err != nil || res.SigningSecret == "" {
		t.Fatalf("%+v %v", res, err)
	}
	ch, _ := c.GetChannel(ctx, res.ID)
	if ch.URLHost == nil || *ch.URLHost != "hooks.example.com" || !ch.AllMonitors {
		t.Fatalf("%+v", ch)
	}
	if err := c.DeleteChannel(ctx, res.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetChannel(ctx, res.ID); !client.IsNotFound(err) {
		t.Fatalf("want 404 after delete, got %v", err)
	}
}

func TestBadTokenIs401(t *testing.T) {
	s := New(t)
	c, _ := client.New(client.Config{Endpoint: s.URL() + "/v1", Token: "cpk_wrong"})
	_, err := c.ListAgents(context.Background())
	var ae *client.APIError
	if !errors.As(err, &ae) || ae.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %v", err)
	}
}

// ---- amendments (2026-09-23-terraform-provider-part-b-provider/amendments.md, Task 3) ----

// A3.2: SeedChannel("webhook", ...) must set URLHost so Task 8 can import a seeded webhook
// and diff it against a `url` attribute in config.
func TestSeedChannelWebhookSetsURLHost(t *testing.T) {
	s := New(t)
	c := newClient(t, s)
	id := s.SeedChannel("webhook", "seeded-hook")
	ch, err := c.GetChannel(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if ch.URLHost == nil || *ch.URLHost != "hooks.example.com" {
		t.Fatalf("SeedChannel(webhook) did not set URLHost: %+v", ch)
	}
}

// A3.3: OnMonitorPatch is a test-only hook invoked inside the PATCH /v1/monitors/:id handler,
// after the patch is applied and before the response is written — simulating the MonitorDO
// writing `status` asynchronously mid-request. nil is a no-op (exercised implicitly by every
// other test above, none of which sets the hook).
//
// This asserts on the PATCH RESPONSE BODY itself (not a later StoredMonitor/GetMonitor read),
// because only the response body proves the hook ran BEFORE the response was written — an
// implementation that ran the hook after writeJSON would still make a later read see the
// mutation, and would wrongly pass a StoredMonitor-only assertion. The typed client's
// UpdateMonitor discards the response body, so this issues the PATCH directly over HTTP.
func TestOnMonitorPatchHookMutatesBeforeResponding(t *testing.T) {
	s := New(t)
	c := newClient(t, s)
	ctx := context.Background()
	id, err := c.CreateMonitor(ctx, &client.MonitorWrite{
		Name: "a", Type: "http", Target: strp("https://e.com"), IntervalSeconds: 300,
		AgentSources: []string{"agt_sg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.OnMonitorPatch = func(m *client.Monitor) { m.Status = "down" }

	reqBody, err := json.Marshal(&client.MonitorWrite{Name: "a", IntervalSeconds: 300, AgentSources: []string{"agt_sg"}})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPatch, s.URL()+"/v1/monitors/"+id, bytes.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "down" {
		t.Fatalf("PATCH response did not carry the hook's mutation before responding: %+v", out)
	}
}

// Fix round 1, finding 1: the real server (src/lib/channel-service.ts:46-57) treats a
// missing/empty/whitespace-only name as the normal "create unnamed" case (trims to ""), and
// does NOT 400. The fake must not invent a "name is required" 400 the server doesn't have.
func TestCreateChannelAcceptsEmptyOrWhitespaceName(t *testing.T) {
	s := New(t)
	c := newClient(t, s)
	ctx := context.Background()

	res, err := c.CreateChannel(ctx, client.ChannelCreate{Type: "telegram", Name: ""})
	if err != nil {
		t.Fatalf("empty name rejected: %v", err)
	}
	ch, err := c.GetChannel(ctx, res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Name != "" {
		t.Fatalf("empty name not stored as empty: %q", ch.Name)
	}

	res2, err := c.CreateChannel(ctx, client.ChannelCreate{Type: "telegram", Name: "   "})
	if err != nil {
		t.Fatalf("whitespace-only name rejected: %v", err)
	}
	ch2, err := c.GetChannel(ctx, res2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ch2.Name != "" {
		t.Fatalf("whitespace-only name not trimmed to empty: %q", ch2.Name)
	}
}

// A6.5: the fake must normalize assertions on write exactly like check-spec.ts:125-168, so a
// provider-side test asserting on read-back sees what the real server would actually store.
func TestAssertionsAreNormalizedLikeCheckSpecTS(t *testing.T) {
	s := New(t)
	c := newClient(t, s)
	ctx := context.Background()
	str := func(v string) *string { return &v }
	id, err := c.CreateMonitor(ctx, &client.MonitorWrite{
		Name: "a", Type: "http", Target: strp("https://e.com"), IntervalSeconds: 300,
		AgentSources: []string{"agt_sg"},
		CheckSpec: &client.CheckSpec{Assertions: []client.Assertion{
			// header + exists is server-valid (HEADER_OPS includes "exists", check-spec.ts:26):
			// value is ignored because op == "exists"; path is ignored because source != json_body;
			// name is kept because source == header.
			{Source: "header", Op: "exists", Value: str("y"), Path: str("$.x"), Name: str("X-A")},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	cs := s.StoredCheckSpec("a")
	if len(cs.Assertions) != 1 {
		t.Fatalf("assertions = %+v", cs.Assertions)
	}
	a := cs.Assertions[0]
	if a.Value != nil || a.Path != nil {
		t.Fatalf("ignored fields not dropped: %+v", a)
	}
	if a.Name == nil || *a.Name != "X-A" {
		t.Fatalf("kept field dropped: %+v", a)
	}

	// PATCH goes through the same normalization.
	if err := c.UpdateMonitor(ctx, id, &client.MonitorWrite{Name: "a", IntervalSeconds: 300,
		AgentSources: []string{"agt_sg"},
		CheckSpec: &client.CheckSpec{Assertions: []client.Assertion{
			{Source: "header", Op: "equals", Value: str("v"), Name: str("X-A"), Path: str("$.ignored")},
		}}}); err != nil {
		t.Fatal(err)
	}
	a2 := s.StoredCheckSpec("a").Assertions[0]
	if a2.Path != nil || a2.Name == nil || *a2.Name != "X-A" || a2.Value == nil || *a2.Value != "v" {
		t.Fatalf("header assertion not normalized on PATCH: %+v", a2)
	}
}

// A3.4: the real server trims channel names (src/lib/channel-service.ts:57,186) on both
// create and PATCH; a raw echo would diff forever against a provider-side trimmed value.
func TestChannelNameIsTrimmedOnCreateAndPatch(t *testing.T) {
	s := New(t)
	c := newClient(t, s)
	ctx := context.Background()
	res, err := c.CreateChannel(ctx, client.ChannelCreate{Type: "telegram", Name: "  ops  "})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := c.GetChannel(ctx, res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Name != "ops" {
		t.Fatalf("channel name not trimmed on create: %q", ch.Name)
	}
	if err := c.UpdateChannel(ctx, res.ID, client.ChannelPatch{Name: strp("  ops2  ")}); err != nil {
		t.Fatal(err)
	}
	ch2, err := c.GetChannel(ctx, res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ch2.Name != "ops2" {
		t.Fatalf("channel name not trimmed on patch: %q", ch2.Name)
	}
}

// K2: the fake must reject an invalid expected_status the same way the server does
// (src/lib/classify.ts's isValidExpectedStatus, enforced via validateCreate on both create and
// update — src/lib/plan.ts:52, src/lib/monitor-service.ts's edit path reuses validateCreate) —
// otherwise a provider-side regression that starts accepting a bad value would go undetected by
// the unit tests, which only talk to the fake. It must also store an accepted value verbatim
// (no trimming/normalization), matching monitor-service.ts's raw `expected` assignment.
func TestExpectedStatusValidatedLikeTheServer(t *testing.T) {
	s := New(t)
	c := newClient(t, s)
	ctx := context.Background()

	bad := strp("20x")
	_, err := c.CreateMonitor(ctx, &client.MonitorWrite{
		Name: "a", Type: "http", Target: strp("https://e.com"), IntervalSeconds: 300,
		AgentSources: []string{"agt_sg"}, ExpectedStatus: bad,
	})
	if err == nil {
		t.Fatal("create with invalid expected_status (20x) accepted")
	}

	good := strp("200,")
	id, err := c.CreateMonitor(ctx, &client.MonitorWrite{
		Name: "b", Type: "http", Target: strp("https://e.com"), IntervalSeconds: 300,
		AgentSources: []string{"agt_sg"}, ExpectedStatus: good,
	})
	if err != nil {
		t.Fatalf("create with valid (if unusual) expected_status (200,) rejected: %v", err)
	}
	m, err := c.GetMonitor(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if m.ExpectedStatus == nil || *m.ExpectedStatus != "200," {
		t.Fatalf("expected_status not stored verbatim: %+v", m.ExpectedStatus)
	}

	if err := c.UpdateMonitor(ctx, id, &client.MonitorWrite{Name: "b", IntervalSeconds: 300,
		AgentSources: []string{"agt_sg"}, ExpectedStatus: bad}); err == nil {
		t.Fatal("update with invalid expected_status (20x) accepted")
	}
}
