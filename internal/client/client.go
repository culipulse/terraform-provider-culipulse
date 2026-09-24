// Package client is the only package in this provider that speaks HTTP to the CuliPulse
// public API (/v1). It handles auth, retries and error decoding; the typed endpoint
// methods live in api.go.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultEndpoint is the production /v1 base URL.
const DefaultEndpoint = "https://culipulse.dev/v1"

type Config struct {
	Endpoint  string
	Token     string
	UserAgent string
	// Optional Cloudflare Access service token. Staging sits behind Access; production doesn't.
	AccessClientID     string
	AccessClientSecret string
	HTTPClient         *http.Client
	// MaxRetryWait caps the total time spent sleeping between retries. Default 5 minutes —
	// the API's rate-limit window is 60s and it always answers Retry-After: 60.
	MaxRetryWait time.Duration
	// Sleep is injectable for tests.
	Sleep func(ctx context.Context, d time.Duration) error
}

type Client struct {
	cfg  Config
	base string
}

func New(cfg Config) (*Client, error) {
	if cfg.Token == "" {
		return nil, errors.New("api_token is required (or set CULIPULSE_API_TOKEN)")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("endpoint %q is not a valid http(s) URL", cfg.Endpoint)
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.MaxRetryWait == 0 {
		cfg.MaxRetryWait = 5 * time.Minute
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepCtx
	}
	return &Client{cfg: cfg, base: strings.TrimRight(cfg.Endpoint, "/")}, nil
}

// Origin is scheme://host of the endpoint. Heartbeat ping URLs live there (/ping/<token>),
// outside /v1.
func (c *Client) Origin() string {
	u, _ := url.Parse(c.base)
	return u.Scheme + "://" + u.Host
}

// APIError is a non-2xx answer. The API's error body is {error, request_id}
// (src/lib/errors.ts:28-36); request_id is what support needs to find the request.
type APIError struct {
	StatusCode int
	Message    string
	RequestID  string
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("CuliPulse API returned %d: %s", e.StatusCode, e.Message)
	if e.RequestID != "" {
		msg += " (request_id " + e.RequestID + ")"
	}
	return msg
}

func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.StatusCode == http.StatusNotFound
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func backoff(attempt int) time.Duration {
	if attempt > 5 {
		return 30 * time.Second
	}
	d := time.Second << attempt
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

// retryAfter honors only a POSITIVE Retry-After. A server answering `Retry-After: 0` (or a
// negative value) is not asking for a zero-second wait — treating it that way makes `waited`
// in do() never grow, so MaxRetryWait never trips and the client hot-loops (F1). <=0 falls
// back to the exponential backoff instead.
func retryAfter(h string, fallback time.Duration) time.Duration {
	if s, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	return fallback
}

func decodeError(status int, body []byte) *APIError {
	e := &APIError{StatusCode: status}
	var parsed struct {
		Error     string `json:"error"`
		RequestID string `json:"request_id"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Error != "" {
		e.Message, e.RequestID = parsed.Error, parsed.RequestID
		return e
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 200 {
		msg = msg[:200]
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	e.Message = msg
	return e
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var payload []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encoding request: %w", err)
		}
		payload = b
	}
	// GET/PATCH/DELETE are safe to repeat: every PATCH this provider sends carries the full
	// desired state. POST is retried only on 429, which the API returns from its auth
	// middleware before any work is done (src/public-api.ts:73-78). A 5xx on POST may mean
	// the monitor/channel was created, so it is surfaced instead of retried.
	idempotent := method != http.MethodPost
	var waited time.Duration
	for attempt := 0; ; attempt++ {
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
		req.Header.Set("Accept", "application/json")
		if c.cfg.UserAgent != "" {
			req.Header.Set("User-Agent", c.cfg.UserAgent)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.cfg.AccessClientID != "" && c.cfg.AccessClientSecret != "" {
			req.Header.Set("CF-Access-Client-Id", c.cfg.AccessClientID)
			req.Header.Set("CF-Access-Client-Secret", c.cfg.AccessClientSecret)
		}

		var wait time.Duration
		var lastErr error
		resp, err := c.cfg.HTTPClient.Do(req)
		if err != nil {
			if !idempotent || ctx.Err() != nil {
				return err
			}
			lastErr, wait = err, backoff(attempt)
		} else {
			data, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			switch {
			case resp.StatusCode == http.StatusTooManyRequests:
				lastErr = decodeError(resp.StatusCode, data)
				wait = retryAfter(resp.Header.Get("Retry-After"), backoff(attempt))
			case resp.StatusCode >= 500 && idempotent:
				lastErr, wait = decodeError(resp.StatusCode, data), backoff(attempt)
			case resp.StatusCode >= 400:
				return decodeError(resp.StatusCode, data)
			default:
				if readErr != nil {
					return fmt.Errorf("reading response: %w", readErr)
				}
				if out == nil || len(bytes.TrimSpace(data)) == 0 {
					return nil
				}
				if err := json.Unmarshal(data, out); err != nil {
					return fmt.Errorf("decoding %s %s response: %w", method, path, err)
				}
				return nil
			}
		}
		wait += time.Duration(rand.Int64N(int64(wait/10) + 1)) // up to +10% jitter
		if waited+wait > c.cfg.MaxRetryWait {
			return fmt.Errorf("giving up after %s of retries: %w", waited.Round(time.Second), lastErr)
		}
		if err := c.cfg.Sleep(ctx, wait); err != nil {
			return err
		}
		waited += wait
	}
}
