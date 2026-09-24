# Terraform / OpenTofu provider for CuliPulse

Manage [CuliPulse](https://culipulse.dev) monitors and alert routing as code with
[Terraform](https://www.terraform.io) (1.5 or newer) or [OpenTofu](https://opentofu.org) (1.6 or newer).

- Terraform Registry: <https://registry.terraform.io/providers/culipulse/culipulse/latest>
- OpenTofu Registry: <https://search.opentofu.org/provider/culipulse/culipulse/latest>
- Guide: <https://docs.culipulse.dev/api/terraform/>

## Usage

```terraform
terraform {
  required_providers {
    culipulse = {
      source  = "culipulse/culipulse"
      version = "~> 0.1"
    }
  }
}

# Reads the token from the CULIPULSE_API_TOKEN environment variable.
# Create a "Read + Write" token in the CuliPulse app under Settings → API Tokens.
provider "culipulse" {}

data "culipulse_agents" "singapore" {
  kind   = "first_party"
  region = "sg"
}

resource "culipulse_http_monitor" "api" {
  name             = "API health"
  url              = "https://api.example.com/health"
  interval_seconds = 300
  agent_ids        = data.culipulse_agents.singapore.ids
}
```

Resources: `culipulse_http_monitor`, `culipulse_heartbeat_monitor`, `culipulse_webhook_channel`,
`culipulse_channel_routing`. Data sources: `culipulse_agents`, `culipulse_channel`.
Full reference: [`docs/`](docs/) (the same pages the registries show).

## About this repository

This repository is a **read-only mirror**. The provider is developed in CuliPulse's main
repository and copied here, as one snapshot per release, by an automated job.

- **Issues are welcome** here: bug reports, questions and feature requests.
- **Pull requests** are welcome too, but they are not merged in this repository. A maintainer
  ports the change upstream, and it appears here with the next release. Your authorship is
  credited in the CHANGELOG and release notes for that release.
- Security problems: please email support@culipulse.dev instead of opening a public issue.

## Verifying a release

Every release on the [Releases](../../releases) page has a `SHA256SUMS` file and a detached
signature `SHA256SUMS.sig`. The signing key is [`signing-key.asc`](signing-key.asc) in this
repository (RSA 4096). Terraform and OpenTofu check this signature automatically when they
download the provider. To check it by hand:

```shell
gpg --import signing-key.asc
gpg --verify terraform-provider-culipulse_0.1.0_SHA256SUMS.sig terraform-provider-culipulse_0.1.0_SHA256SUMS
sha256sum --ignore-missing -c terraform-provider-culipulse_0.1.0_SHA256SUMS
```

## Building from source

Requires Go (the version in `go.mod`).

```shell
go build -o terraform-provider-culipulse
go test ./...
```

Acceptance tests (`TF_ACC=1`) run against CuliPulse's staging environment and need
credentials that only the maintainers have.

## License

[Mozilla Public License 2.0](LICENSE).
