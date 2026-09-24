package provider

import (
	"regexp"
	"testing"

	"github.com/culipulse/terraform-provider-culipulse/internal/fakeapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestChannelDataSource(t *testing.T) {
	f := fakeapi.New(t)
	f.SeedChannel("telegram", "ops")
	slackID := f.SeedChannel("slack", "ops")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config:      fakeConfig(f, "data \"culipulse_channel\" \"c\" {\n  name = \"ops\"\n}\n"),
				ExpectError: regexp.MustCompile(`(?s)2\s+channels\s+are\s+named\s+"ops"`),
			},
			{
				Config:      fakeConfig(f, "data \"culipulse_channel\" \"c\" {\n  name = \"nope\"\n}\n"),
				ExpectError: regexp.MustCompile(`(?s)No\s+channel\s+named\s+"nope"`),
			},
			{
				Config: fakeConfig(f, "data \"culipulse_channel\" \"c\" {\n  name = \"ops\"\n  type = \"slack\"\n}\n"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.culipulse_channel.c", "id", slackID),
					resource.TestCheckResourceAttr("data.culipulse_channel.c", "status", "connected"),
					resource.TestCheckResourceAttr("data.culipulse_channel.c", "enabled", "true"),
				),
			},
		},
	})
}
