data "culipulse_agents" "singapore" {
  kind   = "first_party"
  region = "sg"
}

variable "health_check_key" {
  type      = string
  sensitive = true
}

resource "culipulse_http_monitor" "api" {
  name             = "API health"
  url              = "https://api.example.com/health"
  interval_seconds = 300
  agent_ids        = data.culipulse_agents.singapore.ids

  expected_status = "200"
  headers         = { "Accept" = "application/json" }
  secret_headers  = { "X-Api-Key" = var.health_check_key }

  assertions = [
    { source = "json_body", op = "equals", path = "$.status", value = "ok" },
    { source = "response_time", op = "lt", value = "2000" },
  ]
}
