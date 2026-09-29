## 0.1.1 (September 29, 2026)

SECURITY:

* Built with Go 1.25.13 and updated dependencies (`google.golang.org/grpc` v1.83.2, `golang.org/x/crypto` v0.55.0, `golang.org/x/net` v0.58.0, `golang.org/x/text` v0.41.0) to pick up upstream security fixes.

NOTES:

* `culipulse_heartbeat_monitor`: settings made in the console that the resource doesn't manage, such as a maximum run time, are now kept when Terraform updates the monitor. This is a server-side fix, so it applies to every provider version; the resource description now says so.

## 0.1.0 (September 24, 2026)

FEATURES:

* **New Resource:** `culipulse_http_monitor` — HTTP(S) checks with headers, secret headers, basic/bearer auth, request body and assertions.
* **New Resource:** `culipulse_heartbeat_monitor` — dead man's switch with a sensitive `ping_url`.
* **New Resource:** `culipulse_webhook_channel` — signed JSON webhooks.
* **New Resource:** `culipulse_channel_routing` — choose which monitors alert to a channel.
* **New Data Source:** `culipulse_agents`, `culipulse_channel`.

NOTES:

* Secret values (`secret_headers`, `bearer_token`, `basic_auth_password`, `url` of a webhook) are stored in Terraform state as sensitive values. Protect your state.
* Changes made to secret values outside Terraform are not detected.
