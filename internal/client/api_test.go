package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	b, _ := io.ReadAll(r.Body)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("body not JSON: %s", b)
	}
	return m
}

func TestCreateMonitor_postsAndReturnsID(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/monitors" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		body := decodeBody(t, r)
		// sla_target is always sent (null clears it); unset pointers are omitted.
		if v, ok := body["sla_target"]; !ok || v != nil {
			t.Errorf("sla_target = %v (present %v), want explicit null", v, ok)
		}
		if _, ok := body["down_min_sources"]; ok {
			t.Error("unset down_min_sources must be omitted")
		}
		if cs, ok := body["check_spec"].(map[string]any); !ok || len(cs) != 0 {
			t.Errorf("empty check_spec must be sent as {}, got %v", body["check_spec"])
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"mon_1","status":"pending"}`))
	})
	id, err := c.CreateMonitor(context.Background(), &MonitorWrite{Name: "x", Type: "http", IntervalSeconds: 300, CheckSpec: &CheckSpec{}})
	if err != nil || id != "mon_1" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestUpdateMonitor_patchesEscapedPath(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.EscapedPath() != "/v1/monitors/mon%2F1" {
			t.Errorf("%s %s", r.Method, r.URL.EscapedPath())
		}
		w.Write([]byte(`{"id":"mon/1","status":"up"}`))
	})
	if err := c.UpdateMonitor(context.Background(), "mon/1", &MonitorWrite{Name: "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestGetMonitor_decodesDetail(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"mon_1","name":"api","type":"http","target":"https://e.com","interval_seconds":300,
		"timeout_ms":10000,"method":"GET","expected_status":"2xx","body_match":null,"follow_redirects":1,
		"down_after_failures":2,"up_after_successes":1,"down_min_sources":1,"heartbeat_grace_seconds":null,
		"sla_target":99.5,"status":"up","enabled":1,"created_at":1,"updated_at":1,"agent_sources":["agt_sg"],
		"check_spec":{"request":{"headers":{"X-Env":"t"},"secretHeaderNames":["X-Key"],"auth":{"type":"bearer"}},
		"assertions":[{"source":"json_body","op":"equals","path":"$.ok","value":"true"}]}}`))
	})
	m, err := c.GetMonitor(context.Background(), "mon_1")
	if err != nil {
		t.Fatal(err)
	}
	if m.FollowRedirects != 1 || *m.SLATarget != 99.5 || m.AgentSources[0] != "agt_sg" {
		t.Fatalf("%+v", m)
	}
	r := m.CheckSpec.Request
	if r.Headers["X-Env"] != "t" || r.SecretHeaderNames[0] != "X-Key" || r.Auth.Type != "bearer" {
		t.Fatalf("request = %+v", r)
	}
	if *m.CheckSpec.Assertions[0].Path != "$.ok" {
		t.Fatalf("assertions = %+v", m.CheckSpec.Assertions)
	}
}

func TestDeleteMonitor_accepts204AndDeleteChannel_accepts200(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method %s", r.Method)
		}
		if r.URL.Path == "/v1/monitors/mon_1" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	})
	if err := c.DeleteMonitor(context.Background(), "mon_1"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteChannel(context.Background(), "ch_1"); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateChannel_emptyMonitorIDsSentAsArrayAndNilOmitted(t *testing.T) {
	var bodies []map[string]any
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		bodies = append(bodies, decodeBody(t, r))
		w.Write([]byte(`{"ok":true}`))
	})
	all, ids := true, []string{}
	if err := c.UpdateChannel(context.Background(), "ch_1", ChannelPatch{AllMonitors: &all, MonitorIDs: &ids}); err != nil {
		t.Fatal(err)
	}
	name := "n"
	if err := c.UpdateChannel(context.Background(), "ch_1", ChannelPatch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if v, ok := bodies[0]["monitor_ids"].([]any); !ok || len(v) != 0 {
		t.Errorf("monitor_ids = %v, want []", bodies[0]["monitor_ids"])
	}
	if _, ok := bodies[1]["monitor_ids"]; ok {
		t.Error("name-only PATCH must not send monitor_ids")
	}
	if _, ok := bodies[1]["all_monitors"]; ok {
		t.Error("name-only PATCH must not send all_monitors")
	}
}

func TestCreateChannel_returnsSigningSecret(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b := decodeBody(t, r)
		if b["type"] != "webhook" || b["url"] != "https://h.example.com/x" {
			t.Errorf("body %v", b)
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"ch_1","signing_secret":"whsec_abc"}`))
	})
	res, err := c.CreateChannel(context.Background(), ChannelCreate{Type: "webhook", Name: "n", URL: "https://h.example.com/x"})
	if err != nil || res.ID != "ch_1" || res.SigningSecret != "whsec_abc" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestListAgentsAndChannelsAndMonitors(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agents":
			w.Write([]byte(`{"agents":[{"id":"agt_sg","kind":"first_party","name":"SG","region":"sg","version":"1.9.0","last_seen_at":5,"tenant_id":null}]}`))
		case "/v1/channels":
			w.Write([]byte(`{"channels":[{"id":"ch_1","type":"telegram","name":"ops","enabled":true,"created_at":1,"status":"connected","all_monitors":true,"monitor_ids":[]}]}`))
		case "/v1/monitors":
			w.Write([]byte(`{"monitors":[{"id":"mon_1","name":"a","type":"http"}]}`))
		}
	})
	ctx := context.Background()
	agents, err := c.ListAgents(ctx)
	if err != nil || len(agents) != 1 || *agents[0].Region != "sg" {
		t.Fatalf("%+v %v", agents, err)
	}
	chs, err := c.ListChannels(ctx)
	if err != nil || len(chs) != 1 || !chs[0].AllMonitors {
		t.Fatalf("%+v %v", chs, err)
	}
	ms, err := c.ListMonitors(ctx)
	if err != nil || len(ms) != 1 || ms[0].ID != "mon_1" {
		t.Fatalf("%+v %v", ms, err)
	}
}
