// Package fakeapi is an in-memory stand-in for the CuliPulse /v1 API, used by the provider's
// unit-level lifecycle tests. It reproduces the server behaviours the provider depends on —
// each cites the real code in the plan's Task 3 table — and nothing more. Real-server
// fidelity is checked by the TF_ACC tests against staging.
package fakeapi

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/culipulse/terraform-provider-culipulse/internal/client"
	"github.com/culipulse/terraform-provider-culipulse/internal/urlhost"
)

const Token = "cpk_test"

type storedMonitor struct {
	m         client.Monitor   // readable columns; CheckSpec/AgentSources unset
	checkSpec client.CheckSpec // as last written, secrets included
	agents    []string
}

type storedChannel struct {
	c   client.Channel // MonitorIDs unset; rows live in ids
	ids map[string]bool
}

type Server struct {
	srv      *httptest.Server
	mu       sync.Mutex
	seq      int
	monitors map[string]*storedMonitor
	channels map[string]*storedChannel
	agents   []client.Agent

	// OnMonitorPatch is a test-only hook invoked inside the PATCH /v1/monitors/:id handler,
	// after the patch has been applied to the stored monitor and before the response is
	// written. It simulates the real MonitorDO writing `status` asynchronously mid-request
	// (e.g. a probe completing while the PATCH is in flight). nil is a no-op.
	//
	// It runs while s.mu is held (the whole serve() handler runs under the lock): it must
	// mutate *m directly and must NOT call any Server method that takes s.mu (SeedMonitor,
	// StoredMonitor, DeleteMonitorNamed, ...) — doing so would deadlock.
	OnMonitorPatch func(m *client.Monitor)

	// FailNextChannelGet, when true, makes the very next GET /v1/channels/:id answer 400 and
	// resets itself to false. Test-only, for F7: simulates a Create's own signing-secret
	// read-back failing (a transient blip, a channel deleted moments after creation, ...) so a
	// test can prove the secret was already persisted to state before that GET ran, rather
	// than only living in a local variable the failed read-back never reaches.
	FailNextChannelGet bool
}

func strp(s string) *string { return &s }

var (
	statusCodeRE     = regexp.MustCompile(`^\d{3}$`)
	statusWildcardRE = regexp.MustCompile(`(?i)^\dxx$`)
)

// isValidExpectedStatus mirrors src/lib/classify.ts's isValidExpectedStatus exactly (K2): split
// on ",", trim each token, drop empty tokens, require at least one surviving token, and every
// token must be a 3-digit status code or a case-insensitive "digit + xx" wildcard. Enforced by
// validateCreate (src/lib/plan.ts:52) on both create and the edit path (monitor-service.ts
// reuses validateCreate), which is why it's checked in both createMonitor and patchMonitor
// below. The provider's expectedStatusValidator (internal/provider) duplicates this same rule
// for the same reason K1's urlhost is a shared package doesn't apply here: this is a pure
// function, not something both sides need byte-identical behavior from a single source to avoid
// an import cycle — a comment on each copy is enough to keep them in sync.
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

func New(t testing.TB) *Server {
	s := &Server{
		monitors: map[string]*storedMonitor{},
		channels: map[string]*storedChannel{},
		agents: []client.Agent{
			{ID: "agt_eu", Kind: "first_party", Name: "Europe", Region: strp("eu")},
			{ID: "agt_home", Kind: "tenant", Name: "home-lab", Region: strp("hanoi")},
			{ID: "agt_sg", Kind: "first_party", Name: "Singapore", Region: strp("sg")},
		},
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *Server) URL() string { return s.srv.URL }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "request_id": "req_fake"})
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+Token {
		writeErr(w, http.StatusUnauthorized, "invalid or expired token")
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "monitors" && r.Method == http.MethodGet:
		s.listMonitors(w)
	case len(parts) == 1 && parts[0] == "monitors" && r.Method == http.MethodPost:
		s.createMonitor(w, body)
	case len(parts) == 2 && parts[0] == "monitors":
		s.monitorByID(w, r.Method, parts[1], body)
	case len(parts) == 1 && parts[0] == "channels" && r.Method == http.MethodGet:
		s.listChannels(w)
	case len(parts) == 1 && parts[0] == "channels" && r.Method == http.MethodPost:
		s.createChannel(w, body)
	case len(parts) == 2 && parts[0] == "channels":
		s.channelByID(w, r.Method, parts[1], body)
	case len(parts) == 1 && parts[0] == "agents" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"agents": s.agents})
	default:
		writeErr(w, http.StatusNotFound, "not found")
	}
}

// ---- monitors ----

// consensus.ts:46-52
func downMinSources(raw *int64, sources int) (int64, string) {
	n := int64(max(1, sources))
	if raw == nil {
		return min(2, n), ""
	}
	if *raw < 1 || *raw > n {
		return 0, fmt.Sprintf("down_min_sources must be an integer in 1..%d", n)
	}
	return *raw, ""
}

// normalizeAssertions mirrors src/lib/check-spec.ts:125-168 (A6.5): `value` is dropped when
// op == "exists"; `path` is kept only for source == "json_body"; `name` is kept only for
// source == "header". A nil slice stays nil so callers can tell "no assertions" from
// "assertions: []" the same way the real server's spec.assertions does.
func normalizeAssertions(in []client.Assertion) []client.Assertion {
	if in == nil {
		return nil
	}
	out := make([]client.Assertion, len(in))
	for i, a := range in {
		n := client.Assertion{Source: a.Source, Op: a.Op}
		if a.Op != "exists" {
			n.Value = a.Value
		}
		if a.Source == "json_body" {
			n.Path = a.Path
		}
		if a.Source == "header" {
			n.Name = a.Name
		}
		out[i] = n
	}
	return out
}

func (s *Server) checkAgents(ids []string) string {
	known := map[string]bool{}
	for _, a := range s.agents {
		known[a.ID] = true
	}
	for _, id := range ids {
		if !known[id] {
			return "unknown agent " + id
		}
	}
	return ""
}

func sortedCopy(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

func (s *Server) createMonitor(w http.ResponseWriter, body []byte) {
	var in client.MonitorWrite
	if err := json.Unmarshal(body, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	typ := in.Type
	if typ == "" {
		typ = "http"
	}
	if in.Name == "" || in.IntervalSeconds <= 0 || (typ != "heartbeat" && (in.Target == nil || *in.Target == "")) {
		writeErr(w, http.StatusBadRequest, "name, target, interval_seconds are required")
		return
	}
	if typ != "heartbeat" && len(in.AgentSources) == 0 {
		writeErr(w, http.StatusBadRequest, "a monitor needs at least one source")
		return
	}
	if msg := s.checkAgents(in.AgentSources); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	dms, msg := downMinSources(in.DownMinSources, len(in.AgentSources))
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	s.seq++
	id := fmt.Sprintf("mon_%d", s.seq)
	m := client.Monitor{
		ID: id, Name: in.Name, Type: typ, IntervalSeconds: in.IntervalSeconds,
		TimeoutMS: 10000, Method: strp("GET"), ExpectedStatus: strp("2xx"), BodyMatch: in.BodyMatch,
		FollowRedirects: 1, DownAfterFailures: 2, UpAfterSuccesses: 1, DownMinSources: dms,
		SLATarget: in.SLATarget, Status: "pending",
	}
	if in.Target != nil {
		m.Target = *in.Target
	}
	if in.TimeoutMS != nil {
		m.TimeoutMS = *in.TimeoutMS
	}
	if in.Method != nil {
		m.Method = strp(strings.ToUpper(*in.Method))
	}
	if in.ExpectedStatus != nil {
		if !isValidExpectedStatus(*in.ExpectedStatus) {
			writeErr(w, http.StatusBadRequest, "expected_status must be codes like '200', '2xx', or '200,301'")
			return
		}
		m.ExpectedStatus = in.ExpectedStatus
	}
	if in.FollowRedirects != nil && !*in.FollowRedirects {
		m.FollowRedirects = 0
	}
	if in.DownAfterFailures != nil {
		m.DownAfterFailures = *in.DownAfterFailures
	}
	if in.UpAfterSuccesses != nil {
		m.UpAfterSuccesses = *in.UpAfterSuccesses
	}
	var cs client.CheckSpec
	if in.CheckSpec != nil {
		cs = *in.CheckSpec
	}
	cs.Assertions = normalizeAssertions(cs.Assertions) // A6.5: check-spec.ts:125-168
	if typ == "heartbeat" {
		g := max(60, int64(math.Round(float64(in.IntervalSeconds)*0.2)))
		if in.GraceSeconds != nil {
			g = *in.GraceSeconds
		}
		m.HeartbeatGraceSeconds = &g
		cs.Heartbeat = &client.HeartbeatSpec{Token: fmt.Sprintf("%s.fakesecret%d", id, s.seq)}
	}
	s.monitors[id] = &storedMonitor{m: m, checkSpec: cs, agents: sortedCopy(in.AgentSources)}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, "status": "pending"})
}

func (s *Server) listMonitors(w http.ResponseWriter) {
	out := []client.Monitor{}
	for _, sm := range s.monitors {
		out = append(out, sm.m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{"monitors": out})
}

// redact mirrors src/api.ts:80-88.
func redact(cs client.CheckSpec) client.CheckSpec {
	out := cs
	if cs.Request != nil {
		r := *cs.Request
		if len(r.SecretHeaders) > 0 {
			names := make([]string, 0, len(r.SecretHeaders))
			for k := range r.SecretHeaders {
				names = append(names, k)
			}
			sort.Strings(names)
			r.SecretHeaderNames = names
		}
		r.SecretHeaders = nil
		if r.Auth != nil {
			r.Auth = &client.AuthSpec{Type: r.Auth.Type}
		}
		out.Request = &r
	}
	out.Assertions = append([]client.Assertion(nil), cs.Assertions...)
	return out
}

func (s *Server) detail(sm *storedMonitor) client.Monitor {
	m := sm.m
	cs := redact(sm.checkSpec)
	m.CheckSpec = &cs
	m.AgentSources = sortedCopy(sm.agents)
	return m
}

func (s *Server) monitorByID(w http.ResponseWriter, method, id string, body []byte) {
	sm, ok := s.monitors[id]
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	switch method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.detail(sm))
	case http.MethodDelete:
		s.deleteMonitor(id)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPatch:
		s.patchMonitor(w, sm, body)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) deleteMonitor(id string) {
	delete(s.monitors, id)
	for _, ch := range s.channels {
		delete(ch.ids, id)
	}
}

func (s *Server) patchMonitor(w http.ResponseWriter, sm *storedMonitor, body []byte) {
	var raw map[string]json.RawMessage
	var in client.MonitorWrite
	if json.Unmarshal(body, &raw) != nil || json.Unmarshal(body, &in) != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	has := func(k string) bool { _, ok := raw[k]; return ok }
	next := *sm // validate everything before mutating
	m := &next.m
	if has("type") && in.Type != m.Type {
		writeErr(w, http.StatusBadRequest, "type cannot be changed; delete and recreate the monitor")
		return
	}
	if has("name") {
		m.Name = in.Name
	}
	if has("target") && in.Target != nil {
		m.Target = *in.Target
	}
	if has("interval_seconds") {
		m.IntervalSeconds = in.IntervalSeconds
	}
	if in.TimeoutMS != nil {
		m.TimeoutMS = *in.TimeoutMS
	}
	if in.Method != nil {
		m.Method = strp(strings.ToUpper(*in.Method))
	}
	if in.ExpectedStatus != nil {
		if !isValidExpectedStatus(*in.ExpectedStatus) {
			writeErr(w, http.StatusBadRequest, "expected_status must be codes like '200', '2xx', or '200,301'")
			return
		}
		m.ExpectedStatus = in.ExpectedStatus
	}
	if in.FollowRedirects != nil {
		m.FollowRedirects = map[bool]int64{true: 1, false: 0}[*in.FollowRedirects]
	}
	if in.DownAfterFailures != nil {
		m.DownAfterFailures = *in.DownAfterFailures
	}
	if in.UpAfterSuccesses != nil {
		m.UpAfterSuccesses = *in.UpAfterSuccesses
	}
	if in.GraceSeconds != nil && m.Type == "heartbeat" {
		g := *in.GraceSeconds
		m.HeartbeatGraceSeconds = &g
	}
	if has("body_match") { // monitor-service.ts:586
		if in.BodyMatch == nil || *in.BodyMatch == "" {
			m.BodyMatch = nil
		} else {
			m.BodyMatch = in.BodyMatch
		}
	}
	if has("sla_target") {
		m.SLATarget = in.SLATarget
	}
	if has("agent_sources") { // omitted → kept (monitor-service.ts:563)
		if msg := s.checkAgents(in.AgentSources); msg != "" {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		next.agents = sortedCopy(in.AgentSources)
	}
	if m.Type != "heartbeat" && len(next.agents) == 0 {
		writeErr(w, http.StatusBadRequest, "a monitor needs at least one source")
		return
	}
	dms, msg := downMinSources(in.DownMinSources, len(next.agents)) // omitted/null → default
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	m.DownMinSources = dms
	var cs client.CheckSpec // omitted → {} (validateCheckSpec(undefined))
	if in.CheckSpec != nil {
		cs = *in.CheckSpec
	}
	cs.Assertions = normalizeAssertions(cs.Assertions) // A6.5: check-spec.ts:125-168
	if m.Type == "heartbeat" {                         // monitor-service.ts:636-643
		cs.Heartbeat = sm.checkSpec.Heartbeat
	}
	next.checkSpec = cs
	*sm = next
	if s.OnMonitorPatch != nil { // A3.3: async status write, mid-request, before responding
		s.OnMonitorPatch(&sm.m)
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": sm.m.ID, "status": sm.m.Status})
}

// ---- channels ----

func (s *Server) projectChannel(ch *storedChannel) client.Channel {
	c := ch.c
	c.MonitorIDs = []string{}
	for id := range ch.ids {
		c.MonitorIDs = append(c.MonitorIDs, id)
	}
	sort.Strings(c.MonitorIDs)
	return c
}

func (s *Server) setRouting(ch *storedChannel, ids []string) {
	ch.ids = map[string]bool{}
	for _, id := range ids {
		if _, ok := s.monitors[id]; ok { // foreign ids silently dropped (channel-service.ts:196-205)
			ch.ids[id] = true
		}
	}
}

func (s *Server) listChannels(w http.ResponseWriter) {
	out := []client.Channel{}
	for _, ch := range s.channels {
		out = append(out, s.projectChannel(ch))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}

func (s *Server) createChannel(w http.ResponseWriter, body []byte) {
	var in struct {
		Type        string   `json:"type"`
		Name        string   `json:"name"`
		URL         string   `json:"url"`
		AllMonitors *bool    `json:"all_monitors"`
		MonitorIDs  []string `json:"monitor_ids"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if in.Type == "" {
		in.Type = "telegram"
	}
	// A3.4 + fix-round-1 finding 1: channel-service.ts:46-57 trims the name on create and
	// treats missing/empty/whitespace-only as the normal "create unnamed" case — it does
	// NOT 400 (a 400 there was invented behavior the real server doesn't have).
	in.Name = strings.TrimSpace(in.Name)
	s.seq++
	id := fmt.Sprintf("ch_%d", s.seq)
	ch := &storedChannel{
		c:   client.Channel{ID: id, Type: in.Type, Name: in.Name, Enabled: true, CreatedAt: 1, Status: "connected", AllMonitors: true},
		ids: map[string]bool{},
	}
	res := map[string]string{"id": id}
	if in.Type == "webhook" {
		u, err := url.Parse(in.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			writeErr(w, http.StatusBadRequest, "url must be https")
			return
		}
		// K1: mirror the real server's WHATWG `URL.host` (channel-service.ts:126), not just the
		// hostname — url_host must carry a non-default port so the provider's post-import guard
		// (checkWebhookURLHostMatches) can be exercised against a realistic value.
		ch.c.URLHost = strp(urlhost.WHATWGHost(u))
		res["signing_secret"] = fmt.Sprintf("whsec_fake%d", s.seq)
	}
	if in.AllMonitors != nil {
		ch.c.AllMonitors = *in.AllMonitors
	}
	if !ch.c.AllMonitors {
		s.setRouting(ch, in.MonitorIDs)
	}
	s.channels[id] = ch
	writeJSON(w, http.StatusCreated, res)
}

func (s *Server) channelByID(w http.ResponseWriter, method, id string, body []byte) {
	ch, ok := s.channels[id]
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	switch method {
	case http.MethodGet:
		if s.FailNextChannelGet {
			s.FailNextChannelGet = false
			writeErr(w, http.StatusBadRequest, "simulated read-back failure")
			return
		}
		writeJSON(w, http.StatusOK, s.projectChannel(ch))
	case http.MethodDelete:
		delete(s.channels, id)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case http.MethodPatch:
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(body, &raw); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if t, ok := raw["type"]; ok {
			var typ string
			_ = json.Unmarshal(t, &typ)
			if typ != ch.c.Type {
				writeErr(w, http.StatusBadRequest, "type cannot be changed; delete and recreate the channel")
				return
			}
		}
		if _, ok := raw["url"]; ok {
			writeErr(w, http.StatusBadRequest, "url cannot be changed; delete and recreate the channel")
			return
		}
		if v, ok := raw["name"]; ok {
			_ = json.Unmarshal(v, &ch.c.Name)
			ch.c.Name = strings.TrimSpace(ch.c.Name) // A3.4: channel-service.ts:186 trims on patch
		}
		if v, ok := raw["enabled"]; ok {
			_ = json.Unmarshal(v, &ch.c.Enabled)
		}
		if v, ok := raw["all_monitors"]; ok { // does NOT clear rows (channel-service.ts:192-206)
			_ = json.Unmarshal(v, &ch.c.AllMonitors)
		}
		if v, ok := raw["monitor_ids"]; ok {
			var ids []string
			_ = json.Unmarshal(v, &ids)
			s.setRouting(ch, ids)
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ---- test helpers ----

func (s *Server) SeedMonitor(typ, name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	id := fmt.Sprintf("mon_%d", s.seq)
	m := client.Monitor{ID: id, Name: name, Type: typ, Target: "https://seed.example.com", IntervalSeconds: 300,
		TimeoutMS: 10000, Method: strp("GET"), ExpectedStatus: strp("2xx"), FollowRedirects: 1,
		DownAfterFailures: 2, UpAfterSuccesses: 1, DownMinSources: 1, Status: "up"}
	sm := &storedMonitor{m: m}
	if typ == "heartbeat" {
		g := int64(60)
		sm.m.Target = ""
		sm.m.HeartbeatGraceSeconds = &g
		sm.checkSpec.Heartbeat = &client.HeartbeatSpec{Token: id + ".seedsecret"}
	} else {
		sm.agents = []string{"agt_sg"}
	}
	s.monitors[id] = sm
	return id
}

func (s *Server) SeedChannel(typ, name string) string {
	return s.SeedChannelWithHost(typ, name, "hooks.example.com")
}

// SeedChannelWithHost is SeedChannel with an explicit webhook url_host (ignored for
// non-webhook types), passed through verbatim (already-WHATWG-shaped, e.g. with a
// non-default port like "hooks.example.com:8443") — K1: lets a test import a webhook whose
// url_host carries a port and exercise checkWebhookURLHostMatches against it.
func (s *Server) SeedChannelWithHost(typ, name, urlHost string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	id := fmt.Sprintf("ch_%d", s.seq)
	c := client.Channel{ID: id, Type: typ, Name: name, Enabled: true, CreatedAt: 1, Status: "connected", AllMonitors: true}
	if typ == "webhook" { // A3.2: so Task 8 can import a seeded webhook and diff it against `url`
		c.URLHost = strp(urlHost)
	}
	s.channels[id] = &storedChannel{c: c, ids: map[string]bool{}}
	return id
}

// SeedMonitorSecrets is a test helper for the F2 fix (secretDriftWarning): it writes a secret
// header, an auth type and/or a secret-body marker directly into a monitor's stored
// check_spec, bypassing the normal PATCH path — as if set by a console edit, or by whatever
// created the monitor before it was imported into Terraform. secretHeaders/authType/bodySecret
// are each optional (nil/"" /false to skip).
func (s *Server) SeedMonitorSecrets(name string, secretHeaders map[string]string, authType string, bodySecret bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sm := s.byName(name)
	req := sm.checkSpec.Request
	if req == nil {
		req = &client.RequestSpec{}
	}
	if len(secretHeaders) > 0 {
		req.SecretHeaders = secretHeaders
	}
	if authType != "" {
		req.Auth = &client.AuthSpec{Type: authType}
	}
	if bodySecret {
		req.BodySecret = true
	}
	sm.checkSpec.Request = req
}

func (s *Server) byName(name string) *storedMonitor {
	for _, sm := range s.monitors {
		if sm.m.Name == name {
			return sm
		}
	}
	panic("fakeapi: no monitor named " + name)
}

func (s *Server) DeleteMonitorNamed(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteMonitor(s.byName(name).m.ID)
}

func (s *Server) DeleteChannelNamed(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ch := range s.channels {
		if ch.c.Name == name {
			delete(s.channels, id)
			return
		}
	}
	panic("fakeapi: no channel named " + name)
}

func (s *Server) StoredMonitor(name string) client.Monitor {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byName(name).m
}

func (s *Server) StoredCheckSpec(name string) client.CheckSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byName(name).checkSpec
}

func (s *Server) Routing(channelID string) (bool, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channels[channelID]
	if !ok {
		return false, nil
	}
	c := s.projectChannel(ch)
	return c.AllMonitors, c.MonitorIDs
}
