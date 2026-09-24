terraform {
  required_providers {
    culipulse = {
      source  = "culipulse/culipulse"
      version = "~> 0.1"
    }
  }
}

# The token can also come from the CULIPULSE_API_TOKEN environment variable.
provider "culipulse" {
  api_token = var.culipulse_api_token
}

variable "culipulse_api_token" {
  type      = string
  sensitive = true
}
