package provider

import (
	"testing"

	"github.com/culipulse/terraform-provider-culipulse/internal/fakeapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAgentsDataSource_filters(t *testing.T) {
	f := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{{
			Config: fakeConfig(f, `
data "culipulse_agents" "all" {}

data "culipulse_agents" "sg" {
  kind   = "first_party"
  region = "sg"
}

data "culipulse_agents" "mine" {
  kind = "tenant"
}
`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.culipulse_agents.all", "ids.#", "3"),
				resource.TestCheckResourceAttr("data.culipulse_agents.all", "ids.0", "agt_eu"), // sorted by id
				resource.TestCheckResourceAttr("data.culipulse_agents.sg", "ids.#", "1"),
				resource.TestCheckResourceAttr("data.culipulse_agents.sg", "ids.0", "agt_sg"),
				resource.TestCheckResourceAttr("data.culipulse_agents.sg", "agents.0.name", "Singapore"),
				resource.TestCheckResourceAttr("data.culipulse_agents.mine", "ids.#", "1"),
				resource.TestCheckResourceAttr("data.culipulse_agents.mine", "agents.0.region", "hanoi"),
			),
		}},
	})
}
