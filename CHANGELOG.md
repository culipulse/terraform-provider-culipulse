## 0.1.2 (October 6, 2026)

BUG FIXES:

* `culipulse_channel_routing`: routing a channel to 100 or more monitors now works. Before, the API answered 500 and had already removed the channel's existing routes. The whole routing list is now replaced in one step, so a failed or refused request leaves the current routing as it was. This is a server-side fix and applies to every provider version.
* `culipulse_http_monitor`: `body_match` is now checked. Before, it was saved but never evaluated, so a body without the text did not mark the monitor down. This is a server-side fix and applies to every provider version.

FEATURES:

* `culipulse_http_monitor`: when a save works but something won't behave as you might expect, the API's warning now appears as a Terraform warning on `apply` (create and update). The first warning is for a monitor that has `secret_headers`, `bearer_token` or `basic_auth_*` and runs from an agent you run yourself: agents you run never receive saved secrets, so those checks run without them. The warning repeats on every apply while the combination stands. It does not fail the apply. The `secret_headers`, `bearer_token`, `basic_auth_username` and `basic_auth_password` descriptions now say this.

NOTES:

* `culipulse_http_monitor`: `url` may now be a private, loopback or link-local IP address (for example `http://192.168.1.10/`) when every entry in `agent_ids` is one of your own agents. With any shared agent, or none, the API still refuses it. This is a server-side change and applies to every provider version; no schema change.
* `culipulse_http_monitor`: `down_min_sources` now follows the agents that are reporting. Before, with `down_min_sources = 2` and one of two agents offline, the monitor stayed up even when the target was down. Now the agents that are reporting decide, so one agent alone can mark the monitor down while the other is offline (the monitor page says which agent is not reporting). Deleting an agent also lowers `down_min_sources` to the number of agents that remain (never below 1). If your configuration leaves `down_min_sources` unset, the next refresh simply reads the lowered value. If it pins the old number (for example `down_min_sources = 2` after removing one of two agents), `terraform apply` is rejected by the API because the number is above the agents, so lower it. This is a server-side change and applies to every provider version; no schema change. `GET /v1/monitors/{id}` also returns a read-only `coverage` object; the provider ignores it.
* `culipulse_webhook_channel`: the test delivery CuliPulse sends before saving the channel (and the console's "Send test") now carries `"test": true` at the top level of the JSON body, so a receiver can tell it from a real alert, and the sample no longer pairs a connection timeout with an HTTP 503. Real alerts are unchanged. This is a server-side change and applies to every provider version; no schema change.
* `culipulse_webhook_channel`: an account can now have at most 20 notification destinations (all types, email included). Creating one past that fails at apply with "You can have up to 20 notification destinations" (API error code `channel_limit_reached`); accounts already above 20 keep what they have. A webhook `url` that another webhook channel in the account already uses is also rejected (`duplicate_webhook_url`), and this is checked before the test delivery is sent. Both are server-side and apply to every provider version.
* `culipulse_channel_routing`: the `all_monitors` description now says that a channel with `all_monitors = false` covers only the monitors in `monitor_ids` and does not pick up monitors created later, so a new monitor stays silent on that channel until you add it. Documentation only; behaviour is unchanged.
* `culipulse_channel_routing`: a list longer than your plan's monitor limit (or than the monitors you own, if that is more) is rejected (`too_many_monitors`) and leaves the current routing unchanged. `all_monitors = false` with `monitor_ids = []` is still accepted, so muting a channel keeps working.
* `culipulse_http_monitor`: `headers` and `secret_headers` now fail at plan time when a header name is longer than 256 characters or a value is longer than 8,192 characters. `url` now fails at plan time past 2,048 characters and `expected_status` past 128 characters. The API enforces the same limits (plus 128 KB for the whole check) for every provider version, so this only moves the error from apply to plan.
* `culipulse_http_monitor`: `body_match` now fails at plan time past 1,024 characters; the API enforces the same limit for every provider version.
* `culipulse_heartbeat_monitor`: `down_after_failures` is deprecated. It never had an effect on a heartbeat: the monitor goes down at the first missed ping (after `grace_seconds`) and comes back up at the first ping. The old description ("Missed pings in a row before the monitor is marked down") was wrong. Configurations that set it keep working and now show a deprecation warning at plan time; remove the attribute and use `grace_seconds` to allow a late ping. Behaviour is unchanged. The API still accepts the field for every provider version.

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
