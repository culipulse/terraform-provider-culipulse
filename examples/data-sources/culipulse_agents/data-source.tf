# CuliPulse's shared agents in Europe.
data "culipulse_agents" "eu" {
  kind   = "first_party"
  region = "eu"
}

# Agents you run yourself.
data "culipulse_agents" "own" {
  kind = "tenant"
}
