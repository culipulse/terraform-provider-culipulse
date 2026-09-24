# After import, url and signing_secret are unknown: set url in your configuration. CuliPulse
# checks it points at the same host the channel already sends to (it can't compare the full
# URL — only the host is returned) and, if it matches, records it without recreating the
# channel or sending a new test delivery; a different host is a plan-time error. signing_secret
# stays empty — it can't be recovered after import.
terraform import culipulse_webhook_channel.incidents nch_9e8d7c6b-5a4f-4e3d-8c2b-1a0f9e8d7c6b
