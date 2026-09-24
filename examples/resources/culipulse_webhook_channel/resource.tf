resource "culipulse_webhook_channel" "incidents" {
  name = "Incident bot"
  url  = "https://hooks.example.com/culipulse"
}

# Verify deliveries on your side with this secret.
output "webhook_signing_secret" {
  value     = culipulse_webhook_channel.incidents.signing_secret
  sensitive = true
}
