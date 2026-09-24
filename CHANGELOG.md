## 0.1.0 (Unreleased)

FEATURES:

* **New Resource:** `culipulse_http_monitor` — HTTP(S) checks with headers, secret headers, basic/bearer auth, request body and assertions.
* **New Resource:** `culipulse_heartbeat_monitor` — dead man's switch with a sensitive `ping_url`.
* **New Resource:** `culipulse_webhook_channel` — signed JSON webhooks.
* **New Resource:** `culipulse_channel_routing` — choose which monitors alert to a channel.
* **New Data Source:** `culipulse_agents`, `culipulse_channel`.

NOTES:

* Secret values (`secret_headers`, `bearer_token`, `basic_auth_password`, `url` of a webhook) are stored in Terraform state as sensitive values. Protect your state.
* Changes made to secret values outside Terraform are not detected.
