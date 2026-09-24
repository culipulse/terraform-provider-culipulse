package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.HandlerFunc, mut ...func(*Config)) (*Client, *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	var sleeps []time.Duration
	cfg := Config{
		Endpoint:  srv.URL + "/v1",
		Token:     "cpk_test",
		UserAgent: "terraform-provider-culipulse/test",
		Sleep: func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		},
	}
	for _, m := range mut {
		m(&cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c, &sleeps
}

func TestNew_requiresToken(t *testing.T) {
	_, err := New(Config{Endpoint: DefaultEndpoint})
	if err == nil || !strings.Contains(err.Error(), "api_token is required") {
		t.Fatalf("want api_token error, got %v", err)
	}
}

func TestNew_rejectsBadEndpoint(t *testing.T) {
	for _, ep := range []string{"ftp://x/v1", "not a url", "https://"} {
		if _, err := New(Config{Endpoint: ep, Token: "cpk_x"}); err == nil {
			t.Errorf("endpoint %q: want error", ep)
		}
	}
}

func TestNew_defaultsEndpointAndOrigin(t *testing.T) {
	c, err := New(Config{Token: "cpk_x"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Origin() != "https://culipulse.dev" {
		t.Fatalf("origin = %q", c.Origin())
	}
}

func TestDo_sendsAuthUserAgentAndNoAccessHeaders(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agents" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer cpk_test" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "terraform-provider-culipulse/test" {
			t.Errorf("User-Agent = %q", got)
		}
		if r.Header.Get("CF-Access-Client-Id") != "" {
			t.Error("Access header sent without being configured")
		}
		w.Write([]byte(`{"agents":[]}`))
	})
	var out struct{ Agents []any }
	if err := c.do(context.Background(), http.MethodGet, "/agents", nil, &out); err != nil {
		t.Fatal(err)
	}
}

func TestDo_sendsAccessHeadersWhenConfigured(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("CF-Access-Client-Id") != "id" || r.Header.Get("CF-Access-Client-Secret") != "sec" {
			t.Error("Access headers missing")
		}
		w.Write([]byte(`{}`))
	}, func(c *Config) { c.AccessClientID, c.AccessClientSecret = "id", "sec" })
	if err := c.do(context.Background(), http.MethodGet, "/agents", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestDo_retries429HonoringRetryAfter(t *testing.T) {
	var calls atomic.Int32
	c, sleeps := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate limit exceeded","request_id":"r1"}`))
			return
		}
		w.Write([]byte(`{}`))
	})
	if err := c.do(context.Background(), http.MethodGet, "/monitors", nil, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || len(*sleeps) != 2 {
		t.Fatalf("calls=%d sleeps=%v", calls.Load(), *sleeps)
	}
	for _, d := range *sleeps {
		if d < 60*time.Second || d > 66*time.Second {
			t.Errorf("sleep %s outside [60s,66s]", d)
		}
	}
}

func TestDo_retriesPOSTOn429(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"mon_1","status":"pending"}`))
	})
	var out struct{ ID string }
	if err := c.do(context.Background(), http.MethodPost, "/monitors", map[string]string{"name": "x"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != "mon_1" || calls.Load() != 2 {
		t.Fatalf("id=%q calls=%d", out.ID, calls.Load())
	}
}

func TestDo_doesNotRetryPOSTOn5xx(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("<html>502 Bad Gateway</html>"))
	})
	err := c.do(context.Background(), http.MethodPost, "/monitors", map[string]string{}, nil)
	var ae *APIError
	if !errors.As(err, &ae) || ae.StatusCode != 502 {
		t.Fatalf("want APIError 502, got %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("POST retried on 5xx: calls=%d", calls.Load())
	}
	if !strings.Contains(ae.Message, "502 Bad Gateway") {
		t.Errorf("non-JSON body not surfaced: %q", ae.Message)
	}
}

func TestDo_retriesGETOn5xx(t *testing.T) {
	var calls atomic.Int32
	c, sleeps := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{}`))
	})
	if err := c.do(context.Background(), http.MethodGet, "/monitors/mon_1", nil, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || len(*sleeps) != 1 || (*sleeps)[0] > 2*time.Second {
		t.Fatalf("calls=%d sleeps=%v", calls.Load(), *sleeps)
	}
}

// F1: Retry-After: 0 (or negative) must NOT be honored as "wait zero seconds forever" — that
// makes `waited` never grow, so MaxRetryWait never trips and the client hot-loops. Only a
// positive Retry-After is honored; <=0 falls back to the exponential backoff.
func TestDo_retryAfterZeroFallsBackToBackoffAndBounds(t *testing.T) {
	var calls atomic.Int32
	c, sleeps := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limit exceeded","request_id":"r0"}`))
	}, func(c *Config) { c.MaxRetryWait = 5 * time.Second })
	err := c.do(context.Background(), http.MethodGet, "/monitors", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "giving up") {
		t.Fatalf("want a bounded give-up error, got %v", err)
	}
	// Every sleep must come from backoff(attempt) (>=1s, since backoff starts at 1s for
	// attempt 0), never from the zero/negative Retry-After header.
	for i, d := range *sleeps {
		if d < time.Second {
			t.Fatalf("sleep[%d] = %s; a Retry-After: 0 must fall back to backoff(), not wait ~0", i, d)
		}
	}
	if calls.Load() > 10 {
		t.Fatalf("calls = %d; Retry-After: 0 caused a hot loop instead of bounded backoff", calls.Load())
	}
}

func TestDo_givesUpAfterMaxRetryWait(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limit exceeded","request_id":"r9"}`))
	}, func(c *Config) { c.MaxRetryWait = 90 * time.Second })
	err := c.do(context.Background(), http.MethodGet, "/monitors", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "rate limit exceeded") || !strings.Contains(err.Error(), "giving up") {
		t.Fatalf("want give-up error wrapping the 429, got %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d, want 2", calls.Load())
	}
}

func TestDo_errorIncludesRequestIDAndIsNotFound(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"not found","request_id":"req_abc"}`))
	})
	err := c.do(context.Background(), http.MethodGet, "/monitors/nope", nil, nil)
	if !IsNotFound(err) {
		t.Fatalf("IsNotFound false for %v", err)
	}
	if !strings.Contains(err.Error(), "request_id req_abc") || !strings.Contains(err.Error(), "404") {
		t.Fatalf("error text = %q", err.Error())
	}
	if IsNotFound(errors.New("other")) {
		t.Fatal("IsNotFound true for a plain error")
	}
}

func TestDo_doesNotRetry4xx(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"this token lacks the 'write' scope","request_id":"r"}`))
	})
	err := c.do(context.Background(), http.MethodPatch, "/monitors/m", map[string]string{}, nil)
	if err == nil || calls.Load() != 1 || !strings.Contains(err.Error(), "write' scope") {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
}
