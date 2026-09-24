# A Telegram channel connected in the console.
data "culipulse_channel" "oncall" {
  name = "On-call"
  type = "telegram"
}

resource "culipulse_channel_routing" "oncall" {
  channel_id  = data.culipulse_channel.oncall.id
  monitor_ids = [culipulse_http_monitor.api.id]
}

# Every monitor, including ones created later:
resource "culipulse_channel_routing" "incidents" {
  channel_id   = culipulse_webhook_channel.incidents.id
  all_monitors = true
}
