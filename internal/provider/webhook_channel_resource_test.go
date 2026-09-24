package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/culipulse/terraform-provider-culipulse/internal/fakeapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const hookAddr = "culipulse_webhook_channel.h"

func hookConfig(f *fakeapi.Server, name, url string) string {
	return fakeConfig(f, fmt.Sprintf(`
resource "culipulse_webhook_channel" "h" {
  name = %q
  url  = %q
}
`, name, url))
}

func TestWebhookChannel_lifecycle(t *testing.T) {
	f := fakeapi.New(t)
	var secret string
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: hookConfig(f, "ops-hook", "https://hooks.example.com/a"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr(hookAddr, "signing_secret", regexp.MustCompile(`^whsec_`)),
					resource.TestCheckResourceAttr(hookAddr, "url_host", "hooks.example.com"),
					func(s *terraform.State) error {
						secret = s.RootModule().Resources[hookAddr].Primary.Attributes["signing_secret"]
						return nil
					},
				),
			},
			{
				Config:           hookConfig(f, "ops-hook-2", "https://hooks.example.com/a"),
				ConfigPlanChecks: expectAction(hookAddr, plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(hookAddr, "name", "ops-hook-2"),
					func(s *terraform.State) error {
						if got := s.RootModule().Resources[hookAddr].Primary.Attributes["signing_secret"]; got != secret {
							return fmt.Errorf("signing_secret changed on rename")
						}
						return nil
					},
				),
			},
			{
				Config:           hookConfig(f, "ops-hook-2", "https://other.example.com/b"),
				ConfigPlanChecks: expectAction(hookAddr, plancheck.ResourceActionReplace),
				Check:            resource.TestCheckResourceAttr(hookAddr, "url_host", "other.example.com"),
			},
			{
				ResourceName: hookAddr, ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"url", "signing_secret"},
			},
			{
				PreConfig:        func() { f.DeleteChannelNamed("ops-hook-2") },
				Config:           hookConfig(f, "ops-hook-2", "https://other.example.com/b"),
				ConfigPlanChecks: expectAction(hookAddr, plancheck.ResourceActionCreate),
			},
		},
	})
}

// Review Focus #3. Rewritten import-first per amendment A8.1: ImportStatePersist into a
// directory that already manages the resource fails with "Resource already managed by
// Terraform", so the import step must come first, against a fake-seeded channel.
func TestWebhookChannel_importThenSetURLIsInPlace(t *testing.T) {
	f := fakeapi.New(t)
	id := f.SeedChannel("webhook", "imported")
	cfg := hookConfig(f, "imported", "https://hooks.example.com/a")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{
			{Config: cfg, ResourceName: hookAddr, ImportState: true, ImportStateId: id, ImportStatePersist: true},
			{
				Config:           cfg,
				ConfigPlanChecks: expectAction(hookAddr, plancheck.ResourceActionUpdate),
				Check:            resource.TestCheckResourceAttr(hookAddr, "url", "https://hooks.example.com/a"),
			},
		},
	})
}

// F4: right after import, setting a `url` whose host doesn't match what the channel already
// sends to must fail at plan time — the server can't be asked to compare (it only returns the
// host, never the full URL, and never re-verifies with a test delivery), so a typo would
// otherwise be silently accepted and Terraform would believe it's managing the wrong endpoint.
func TestWebhookChannel_importSetWrongHostFails(t *testing.T) {
	f := fakeapi.New(t)
	id := f.SeedChannel("webhook", "imported") // fakeapi seeds url_host = hooks.example.com
	cfg := hookConfig(f, "imported", "https://wrong.example.com/a")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{
			{Config: cfg, ResourceName: hookAddr, ImportState: true, ImportStateId: id, ImportStatePersist: true},
			{
				Config:      cfg,
				ExpectError: regexp.MustCompile(`(?s)this\s+channel\s+sends\s+to\s+hooks\.example\.com.*wrong\.example\.com`),
			},
		},
	})
}

// K1: a seeded webhook whose url_host carries a non-default port (as the real server stores
// per channel-service.ts:126's WHATWG `URL.host`) must be importable and then updated in place
// once the config's url is set to the matching host:port — the port must not be dropped from
// the comparison and cause a false "URL doesn't match the channel" error.
func TestWebhookChannel_importThenSetURLWithPortIsInPlace(t *testing.T) {
	f := fakeapi.New(t)
	id := f.SeedChannelWithHost("webhook", "imported", "hooks.example.com:8443")
	cfg := hookConfig(f, "imported", "https://hooks.example.com:8443/a")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{
			{Config: cfg, ResourceName: hookAddr, ImportState: true, ImportStateId: id, ImportStatePersist: true},
			{
				Config:           cfg,
				ConfigPlanChecks: expectAction(hookAddr, plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(hookAddr, "url", "https://hooks.example.com:8443/a"),
					resource.TestCheckResourceAttr(hookAddr, "url_host", "hooks.example.com:8443"),
				),
			},
		},
	})
}

func TestWebhookChannel_rejectsPlainHTTP(t *testing.T) {
	f := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      hookConfig(f, "h", "http://hooks.example.com/a"),
			ExpectError: regexp.MustCompile(`(?s)must\s+start\s+with\s+https://`),
		}},
	})
}

func TestWebhookChannel_importRejectsOtherChannelTypes(t *testing.T) {
	f := fakeapi.New(t)
	id := f.SeedChannel("telegram", "tg")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{{
			Config: hookConfig(f, "tg", "https://x.example.com"), ResourceName: hookAddr,
			ImportState: true, ImportStateId: id,
			ExpectError: regexp.MustCompile(`(?s)is\s+a\s+telegram\s+channel`),
		}},
	})
}

// A8.2: the server trims channel names, so a name with leading/trailing whitespace would
// diff forever. Unit-test the shared validator (reused by Task 9's channel_routing and
// culipulse_channel data source).
func TestWebhookChannel_nameRejectsSurroundingWhitespace(t *testing.T) {
	f := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      hookConfig(f, " ops-hook", "https://hooks.example.com/a"),
			ExpectError: regexp.MustCompile(`(?s)leading\s+or\s+trailing\s+whitespace`),
		}},
	})
}
