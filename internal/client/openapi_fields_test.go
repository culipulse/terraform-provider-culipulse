package client

// openAPIFields classifies every /v1 field the provider could see, on each schema it consumes
// (see api.go and types.go). TestOpenAPIDrift fails when openapi/culipulse.json has a field
// that isn't listed here, or no longer has one that is.
//
// Keys are "<Schema>.<property>[.<nested>...]". A field is either modeled (attr names the
// Terraform attribute it maps to) or ignored (with the reason). The guard doesn't descend into
// ignored fields, and a property that $refs another component is checked under that component.
//
// Why this matters: a monitor PATCH always sends the whole check_spec (omitting it wipes
// headers, auth, body and assertions), so a check_spec field the provider doesn't model is
// silently dropped on the next apply.
var openAPIFields = map[string]openAPIField{
	// GET /v1/monitors/{id}
	"Monitor.id":                      {attr: "id"},
	"Monitor.name":                    {attr: "name"},
	"Monitor.type":                    {ignored: "fixed by the resource type; sent on create only"},
	"Monitor.target":                  {attr: "culipulse_http_monitor.url"},
	"Monitor.interval_seconds":        {attr: "interval_seconds"},
	"Monitor.timeout_ms":              {attr: "culipulse_http_monitor.timeout_ms"},
	"Monitor.method":                  {attr: "culipulse_http_monitor.method"},
	"Monitor.expected_status":         {attr: "culipulse_http_monitor.expected_status"},
	"Monitor.body_match":              {attr: "culipulse_http_monitor.body_match"},
	"Monitor.follow_redirects":        {attr: "culipulse_http_monitor.follow_redirects"},
	"Monitor.down_after_failures":     {attr: "down_after_failures"},
	"Monitor.up_after_successes":      {attr: "culipulse_http_monitor.up_after_successes"},
	"Monitor.down_min_sources":        {attr: "culipulse_http_monitor.down_min_sources"},
	"Monitor.heartbeat_grace_seconds": {attr: "culipulse_heartbeat_monitor.grace_seconds"},
	"Monitor.sla_target":              {attr: "sla_target"},
	"Monitor.status":                  {attr: "status"},
	"Monitor.enabled":                 {ignored: "read-only: PATCH rejects it with 400; pausing is POST /v1/monitors/{id}/pause and /resume, which the provider doesn't model; the provider never sends it"},
	"Monitor.created_at":              {ignored: "read-only timestamp"},
	"Monitor.updated_at":              {ignored: "read-only timestamp"},
	"Monitor.check_spec":              {attr: "see CheckSpec"},
	"Monitor.coverage":                {ignored: "read-only status information (which agents are not reporting right now); not configuration, so the provider neither sends nor stores it"},
	"Monitor.agent_sources":           {attr: "culipulse_http_monitor.agent_ids"},

	// POST /v1/monitors
	"MonitorCreate.name":                {attr: "name"},
	"MonitorCreate.type":                {attr: "fixed by the resource type"},
	"MonitorCreate.target":              {attr: "culipulse_http_monitor.url"},
	"MonitorCreate.interval_seconds":    {attr: "interval_seconds"},
	"MonitorCreate.timeout_ms":          {attr: "culipulse_http_monitor.timeout_ms"},
	"MonitorCreate.method":              {attr: "culipulse_http_monitor.method"},
	"MonitorCreate.expected_status":     {attr: "culipulse_http_monitor.expected_status"},
	"MonitorCreate.body_match":          {attr: "culipulse_http_monitor.body_match"},
	"MonitorCreate.follow_redirects":    {attr: "culipulse_http_monitor.follow_redirects"},
	"MonitorCreate.down_after_failures": {attr: "down_after_failures"},
	"MonitorCreate.up_after_successes":  {attr: "culipulse_http_monitor.up_after_successes"},
	"MonitorCreate.down_min_sources":    {attr: "culipulse_http_monitor.down_min_sources"},
	"MonitorCreate.sla_target":          {attr: "sla_target"},
	"MonitorCreate.grace_seconds":       {attr: "culipulse_heartbeat_monitor.grace_seconds"},
	"MonitorCreate.agent_sources":       {attr: "culipulse_http_monitor.agent_ids"},
	"MonitorCreate.check_spec":          {attr: "see CheckSpec"},
	"MonitorCreate.warn_days":           {ignored: "not in v0.1 (the provider has no domain resource); culipulse_http_monitor could model it later as the domain-expiry add-on, a separate provider change"},
	"MonitorCreate.components":          {ignored: "vendor monitors only; no vendor resource in v0.1"},
	"MonitorCreate.dependencies":        {ignored: "not in v0.1; pins to vendor monitors are set in the console"},

	// PATCH /v1/monitors/{id}
	"MonitorPatch.name":                {attr: "name"},
	"MonitorPatch.target":              {attr: "culipulse_http_monitor.url"},
	"MonitorPatch.interval_seconds":    {attr: "interval_seconds"},
	"MonitorPatch.timeout_ms":          {attr: "culipulse_http_monitor.timeout_ms"},
	"MonitorPatch.method":              {attr: "culipulse_http_monitor.method"},
	"MonitorPatch.expected_status":     {attr: "culipulse_http_monitor.expected_status"},
	"MonitorPatch.body_match":          {attr: "culipulse_http_monitor.body_match"},
	"MonitorPatch.follow_redirects":    {attr: "culipulse_http_monitor.follow_redirects"},
	"MonitorPatch.down_after_failures": {attr: "down_after_failures"},
	"MonitorPatch.up_after_successes":  {attr: "culipulse_http_monitor.up_after_successes"},
	"MonitorPatch.down_min_sources":    {attr: "culipulse_http_monitor.down_min_sources"},
	"MonitorPatch.sla_target":          {attr: "sla_target"},
	"MonitorPatch.grace_seconds":       {attr: "culipulse_heartbeat_monitor.grace_seconds"},
	"MonitorPatch.agent_sources":       {attr: "culipulse_http_monitor.agent_ids"},
	"MonitorPatch.check_spec":          {attr: "see CheckSpec"},
	"MonitorPatch.warn_days":           {ignored: "not in v0.1; the server keeps the stored domain-expiry setting when a PATCH omits it, even when check_spec is sent"},
	"MonitorPatch.components":          {ignored: "vendor monitors only; no vendor resource in v0.1"},
	"MonitorPatch.dependencies":        {ignored: "not in v0.1; the server keeps the stored pins when a PATCH omits it"},

	// GET /v1/monitors
	"MonitorList.monitors": {attr: "client.ListMonitors (the acceptance-test sweeper)"},

	// POST/PATCH result
	"MonitorMutationResult.id":               {attr: "id"},
	"MonitorMutationResult.status":           {ignored: "status is read back with GET after every write"},
	"MonitorMutationResult.warnings":         {attr: "diagnostic warning on create and update"},
	"MonitorMutationResult.warnings.code":    {attr: "diagnostic warning on create and update"},
	"MonitorMutationResult.warnings.message": {attr: "diagnostic warning on create and update"},

	// check_spec (shared by Monitor, MonitorCreate, MonitorPatch)
	"CheckSpec.request":                       {attr: "see below"},
	"CheckSpec.request.headers":               {attr: "culipulse_http_monitor.headers"},
	"CheckSpec.request.secretHeaders":         {attr: "culipulse_http_monitor.secret_headers"},
	"CheckSpec.request.preserveSecretHeaders": {ignored: "the provider always sends secret_headers from config, never asks the server to keep them"},
	"CheckSpec.request.secretHeaderNames":     {attr: "culipulse_http_monitor.secret_headers (drift on names)"},
	"CheckSpec.request.auth":                  {attr: "see below"},
	"CheckSpec.request.auth.type":             {attr: "which of basic_auth_* / bearer_token is set"},
	"CheckSpec.request.auth.token":            {attr: "culipulse_http_monitor.bearer_token"},
	"CheckSpec.request.auth.username":         {attr: "culipulse_http_monitor.basic_auth_username"},
	"CheckSpec.request.auth.password":         {attr: "culipulse_http_monitor.basic_auth_password"},
	"CheckSpec.request.body":                  {attr: "culipulse_http_monitor.request_body"},
	"CheckSpec.request.bodySecret":            {attr: "culipulse_http_monitor.request_body (read: keep state when the stored body is encrypted)"},
	"CheckSpec.request.preserveBody":          {ignored: "the provider never writes secret bodies in v0.1"},
	"CheckSpec.request.contentType":           {attr: "culipulse_http_monitor.content_type"},
	"CheckSpec.assertions":                    {attr: "culipulse_http_monitor.assertions"},
	"CheckSpec.assertions.source":             {attr: "assertions.source"},
	"CheckSpec.assertions.op":                 {attr: "assertions.op"},
	"CheckSpec.assertions.value":              {attr: "assertions.value"},
	"CheckSpec.assertions.path":               {attr: "assertions.path"},
	"CheckSpec.assertions.name":               {attr: "assertions.name"},
	"CheckSpec.transport":                     {ignored: "tcp/udp monitors only; no tcp/udp resource in v0.1, and the server keeps the stored transport when a PATCH omits check_spec"},
	"CheckSpec.domain":                        {ignored: "http expiry add-on set by the top-level warn_days; the server keeps it when a PATCH omits warn_days"},
	"CheckSpec.ping":                          {ignored: "icmp monitors only; no icmp resource in v0.1, and the server keeps the stored ping settings when a PATCH omits check_spec"},
	"CheckSpec.heartbeat":                     {attr: "see below"},
	"CheckSpec.heartbeat.token":               {attr: "culipulse_heartbeat_monitor.ping_url"},
	"CheckSpec.heartbeat.failEnabled":         {ignored: "not in v0.1; the heartbeat resource never sends check_spec, and the server keeps it when a PATCH omits check_spec"},
	"CheckSpec.heartbeat.maxDurationSeconds":  {ignored: "not in v0.1; the heartbeat resource never sends check_spec, and the server keeps it when a PATCH omits check_spec"},
	"CheckSpec.dns":                           {ignored: "dns monitors only; no dns resource in v0.1"},
	"CheckSpec.vendor":                        {ignored: "vendor monitors only; no vendor resource in v0.1"},
	"CheckSpec.dependencies":                  {ignored: "set by the top-level dependencies field, which the server keeps when a PATCH omits it"},

	// GET /v1/channels, GET /v1/channels/{id}
	"ChannelList.channels": {attr: "data.culipulse_channel"},
	"Channel.id":           {attr: "id"},
	"Channel.type":         {attr: "data.culipulse_channel.type"},
	"Channel.name":         {attr: "name"},
	"Channel.enabled":      {attr: "data.culipulse_channel.enabled"},
	"Channel.created_at":   {ignored: "read-only timestamp"},
	"Channel.status":       {attr: "data.culipulse_channel.status"},
	"Channel.all_monitors": {attr: "culipulse_channel_routing.all_monitors"},
	"Channel.monitor_ids":  {attr: "culipulse_channel_routing.monitor_ids"},
	"Channel.url_host":     {attr: "culipulse_webhook_channel.url_host"},
	"Channel.team_name":    {ignored: "Slack channels only; no slack resource in v0.1"},
	"Channel.channel_name": {ignored: "Slack channels only; no slack resource in v0.1"},

	// POST /v1/channels
	"ChannelCreate.type":         {attr: "fixed by the resource type"},
	"ChannelCreate.name":         {attr: "culipulse_webhook_channel.name"},
	"ChannelCreate.url":          {attr: "culipulse_webhook_channel.url"},
	"ChannelCreate.all_monitors": {ignored: "routing is managed by culipulse_channel_routing (PATCH), not at create"},
	"ChannelCreate.monitor_ids":  {ignored: "routing is managed by culipulse_channel_routing (PATCH), not at create"},

	"ChannelCreateResult.id":              {attr: "id"},
	"ChannelCreateResult.signing_secret":  {attr: "culipulse_webhook_channel.signing_secret"},
	"ChannelCreateResult.deep_link":       {ignored: "Telegram onboarding link; no telegram resource in v0.1"},
	"ChannelCreateResult.group_deep_link": {ignored: "Telegram onboarding link; no telegram resource in v0.1"},
	"ChannelCreateResult.authorize_url":   {ignored: "Slack OAuth link; no slack resource in v0.1"},

	// PATCH /v1/channels/{id}
	"ChannelPatch.name":         {attr: "culipulse_webhook_channel.name"},
	"ChannelPatch.enabled":      {ignored: "pausing a channel isn't in v0.1; omitted on PATCH keeps it"},
	"ChannelPatch.all_monitors": {attr: "culipulse_channel_routing.all_monitors"},
	"ChannelPatch.monitor_ids":  {attr: "culipulse_channel_routing.monitor_ids"},

	// GET /v1/agents
	"AgentList.agents":   {attr: "data.culipulse_agents.agents"},
	"Agent.id":           {attr: "agents.id"},
	"Agent.tenant_id":    {ignored: "internal owner id; kind says first-party vs your own"},
	"Agent.kind":         {attr: "agents.kind"},
	"Agent.name":         {attr: "agents.name"},
	"Agent.region":       {attr: "agents.region"},
	"Agent.capabilities": {ignored: "not exposed in v0.1"},
	"Agent.version":      {attr: "agents.version"},
	"Agent.arch":         {ignored: "not exposed in v0.1"},
	"Agent.last_seen_at": {attr: "agents.last_seen_at"},
	"Agent.created_at":   {ignored: "read-only timestamp"},
}

type openAPIField struct {
	attr    string // the Terraform attribute(s) the field maps to
	ignored string // why the provider deliberately doesn't use it
}
