resource "culipulse_heartbeat_monitor" "backup" {
  name             = "Nightly backup"
  interval_seconds = 86400
  grace_seconds    = 1800
}

# Give this to your job, e.g. `curl -fsS "$PING_URL"` as its last step.
output "backup_ping_url" {
  value     = culipulse_heartbeat_monitor.backup.ping_url
  sensitive = true
}
