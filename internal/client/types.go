package client

// Monitor is GET /v1/monitors/{id}. The list endpoint returns the same shape without
// check_spec / agent_sources.
type Monitor struct {
	ID                    string     `json:"id"`
	Name                  string     `json:"name"`
	Type                  string     `json:"type"`
	Target                string     `json:"target"`
	IntervalSeconds       int64      `json:"interval_seconds"`
	TimeoutMS             int64      `json:"timeout_ms"`
	Method                *string    `json:"method"`
	ExpectedStatus        *string    `json:"expected_status"`
	BodyMatch             *string    `json:"body_match"`
	FollowRedirects       int64      `json:"follow_redirects"` // 1 = follow, 0 = don't
	DownAfterFailures     int64      `json:"down_after_failures"`
	UpAfterSuccesses      int64      `json:"up_after_successes"`
	DownMinSources        int64      `json:"down_min_sources"`
	HeartbeatGraceSeconds *int64     `json:"heartbeat_grace_seconds"`
	SLATarget             *float64   `json:"sla_target"`
	Status                string     `json:"status"`
	CheckSpec             *CheckSpec `json:"check_spec,omitempty"`
	AgentSources          []string   `json:"agent_sources,omitempty"`
}

// CheckSpec is the type-specific settings object (src/lib/check-spec.ts). Only the keys the
// provider manages are modelled.
type CheckSpec struct {
	Request    *RequestSpec   `json:"request,omitempty"`
	Assertions []Assertion    `json:"assertions,omitempty"`
	Heartbeat  *HeartbeatSpec `json:"heartbeat,omitempty"`
}

type RequestSpec struct {
	Headers map[string]string `json:"headers,omitempty"`
	// Write-only. Reads return SecretHeaderNames instead (src/api.ts:80-88).
	SecretHeaders     map[string]string `json:"secretHeaders,omitempty"`
	SecretHeaderNames []string          `json:"secretHeaderNames,omitempty"`
	// Reads return {type} only.
	Auth *AuthSpec `json:"auth,omitempty"`
	Body *string   `json:"body,omitempty"`
	// Read-only marker: the body is stored encrypted and not returned. The provider never
	// writes secret bodies in v0.1.
	BodySecret  bool    `json:"bodySecret,omitempty"`
	ContentType *string `json:"contentType,omitempty"`
}

type AuthSpec struct {
	Type     string `json:"type"` // "bearer" | "basic"
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type Assertion struct {
	Source string  `json:"source"`
	Op     string  `json:"op"`
	Value  *string `json:"value,omitempty"`
	Path   *string `json:"path,omitempty"`
	Name   *string `json:"name,omitempty"`
}

type HeartbeatSpec struct {
	Token string `json:"token,omitempty"`
}

// MonitorWrite is the body of POST /v1/monitors and PATCH /v1/monitors/{id}.
// nil pointers are omitted. On PATCH an omitted field keeps its value EXCEPT check_spec and
// down_min_sources (see the Global Constraints). sla_target has no omitempty: null clears it.
type MonitorWrite struct {
	Name              string     `json:"name"`
	Type              string     `json:"type,omitempty"` // create only
	Target            *string    `json:"target,omitempty"`
	IntervalSeconds   int64      `json:"interval_seconds"`
	TimeoutMS         *int64     `json:"timeout_ms,omitempty"`
	Method            *string    `json:"method,omitempty"`
	ExpectedStatus    *string    `json:"expected_status,omitempty"`
	BodyMatch         *string    `json:"body_match,omitempty"` // "" clears on PATCH
	FollowRedirects   *bool      `json:"follow_redirects,omitempty"`
	DownAfterFailures *int64     `json:"down_after_failures,omitempty"`
	UpAfterSuccesses  *int64     `json:"up_after_successes,omitempty"`
	DownMinSources    *int64     `json:"down_min_sources,omitempty"`
	GraceSeconds      *int64     `json:"grace_seconds,omitempty"`
	SLATarget         *float64   `json:"sla_target"`
	AgentSources      []string   `json:"agent_sources,omitempty"`
	CheckSpec         *CheckSpec `json:"check_spec,omitempty"`
}

type Channel struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Enabled     bool     `json:"enabled"`
	CreatedAt   int64    `json:"created_at"`
	Status      string   `json:"status"`
	AllMonitors bool     `json:"all_monitors"`
	MonitorIDs  []string `json:"monitor_ids"`
	URLHost     *string  `json:"url_host,omitempty"`
}

type ChannelCreate struct {
	Type string `json:"type"`
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

type ChannelCreateResult struct {
	ID            string `json:"id"`
	SigningSecret string `json:"signing_secret,omitempty"`
}

// ChannelPatch: a nil field is omitted. MonitorIDs is a pointer so an empty list is sent as
// [] (clear) while a name-only PATCH sends no routing at all.
type ChannelPatch struct {
	Name        *string   `json:"name,omitempty"`
	AllMonitors *bool     `json:"all_monitors,omitempty"`
	MonitorIDs  *[]string `json:"monitor_ids,omitempty"`
}

type Agent struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"` // "first_party" | "tenant"
	Name       string  `json:"name"`
	Region     *string `json:"region"`
	Version    *string `json:"version"`
	LastSeenAt *int64  `json:"last_seen_at"`
}
