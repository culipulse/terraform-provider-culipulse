package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/culipulse/terraform-provider-culipulse/internal/fakeapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const routeAddr = "culipulse_channel_routing.r"

const twoMonitors = `
resource "culipulse_http_monitor" "a" {
  name             = "a"
  url              = "https://a.example.com"
  interval_seconds = 300
  agent_ids        = ["agt_sg"]
}

resource "culipulse_http_monitor" "b" {
  name             = "b"
  url              = "https://b.example.com"
  interval_seconds = 300
  agent_ids        = ["agt_sg"]
}
`

func routingConfig(f *fakeapi.Server, chID, body string) string {
	return fakeConfig(f, twoMonitors+fmt.Sprintf(`
resource "culipulse_channel_routing" "r" {
  channel_id = %q
%s
}
`, chID, body))
}

// A9.1: extra is out of config (never sent in a plan's monitor_ids) so that CheckDestroy
// (all_monitors=true, 0 rows) fails if Delete doesn't send monitor_ids: [] — a routing row
// left over from a bygone list-mode config would otherwise still be sitting there.
func TestChannelRouting_lifecycle(t *testing.T) {
	f := fakeapi.New(t)
	chID := f.SeedChannel("telegram", "ops-tg")
	extra := f.SeedMonitor("http", "extra")
	list := routingConfig(f, chID, fmt.Sprintf("  monitor_ids = [culipulse_http_monitor.a.id, %q]", extra))
	all := routingConfig(f, chID, "  all_monitors = true")
	fakeRouting := func(wantAll bool, wantIDs int) resource.TestCheckFunc {
		return func(*terraform.State) error {
			gotAll, ids := f.Routing(chID)
			if gotAll != wantAll || len(ids) != wantIDs {
				return fmt.Errorf("fake routing: all=%v ids=%v, want all=%v with %d ids", gotAll, ids, wantAll, wantIDs)
			}
			return nil
		}
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		CheckDestroy: func(*terraform.State) error {
			if gotAll, ids := f.Routing(chID); !gotAll || len(ids) != 0 {
				return fmt.Errorf("destroy must reset to all_monitors=true, got all=%v ids=%v", gotAll, ids)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: list,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(routeAddr, "id", chID),
					resource.TestCheckResourceAttr(routeAddr, "all_monitors", "false"),
					resource.TestCheckResourceAttr(routeAddr, "monitor_ids.#", "2"),
					resource.TestCheckTypeSetElemAttrPair(routeAddr, "monitor_ids.*", "culipulse_http_monitor.a", "id"),
					fakeRouting(false, 2),
				),
			},
			{
				Config: all,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(routeAddr, "all_monitors", "true"),
					resource.TestCheckNoResourceAttr(routeAddr, "monitor_ids.#"),
					fakeRouting(true, 0), // rows cleared, not left behind
				),
			},
			{Config: list, Check: fakeRouting(false, 2)},
			{ResourceName: routeAddr, ImportState: true, ImportStateId: chID, ImportStateVerify: true},
		},
	})
}

// Review Focus #4.
func TestChannelRouting_unknownMonitorIDsFail(t *testing.T) {
	f := fakeapi.New(t)
	chID := f.SeedChannel("slack", "ops-slack")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      routingConfig(f, chID, `  monitor_ids = ["mon_does_not_exist"]`),
			ExpectError: regexp.MustCompile(`(?s)mon_does_not_exist.*not\s+found\s+in\s+this\s+CuliPulse\s+account|not\s+found\s+in\s+this\s+CuliPulse\s+account.*mon_does_not_exist`),
		}},
	})
}

func TestChannelRouting_allMonitorsConflictsWithIDs(t *testing.T) {
	f := fakeapi.New(t)
	chID := f.SeedChannel("telegram", "tg")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      routingConfig(f, chID, "  all_monitors = true\n  monitor_ids  = [culipulse_http_monitor.a.id]"),
			ExpectError: regexp.MustCompile(`(?s)Conflicting\s+routing`),
		}},
	})
}

// A9.2: neither all_monitors nor monitor_ids set at all — the config is ambiguous about
// what to route, so ValidateConfig must reject it with a plain-language nudge.
func TestChannelRouting_neitherAllMonitorsNorIDsIsRejected(t *testing.T) {
	f := fakeapi.New(t)
	chID := f.SeedChannel("telegram", "tg")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{{
			Config:      routingConfig(f, chID, ""),
			ExpectError: regexp.MustCompile(`(?s)all_monitors\s+=\s+true|list\s+of\s+monitors`),
		}},
	})
}

// A9.2: an explicit empty list is an intentional "route nothing" and must be allowed.
func TestChannelRouting_explicitEmptyMonitorIDsIsAllowed(t *testing.T) {
	f := fakeapi.New(t)
	chID := f.SeedChannel("telegram", "tg")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{{
			Config: routingConfig(f, chID, "  monitor_ids = []"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(routeAddr, "all_monitors", "false"),
				resource.TestCheckResourceAttr(routeAddr, "monitor_ids.#", "0"),
			),
		}},
	})
}

// Fix round 1: an UNKNOWN all_monitors at plan time (derived here from a monitor's id, which
// isn't known until that resource is created in the same apply) must not be judged against
// A9.2 yet — ValidateConfig must defer, not read the unknown Bool's zero value (false) and
// falsely report "Routing not specified" because monitor_ids is also omitted.
func TestChannelRouting_unknownAllMonitorsIsNotRejected(t *testing.T) {
	f := fakeapi.New(t)
	chID := f.SeedChannel("telegram", "unk-all")
	cfg := fakeConfig(f, fmt.Sprintf(`
resource "culipulse_http_monitor" "x" {
  name             = "x"
  url              = "https://x.example.com"
  interval_seconds = 300
  agent_ids        = ["agt_sg"]
}

resource "culipulse_channel_routing" "r" {
  channel_id   = %q
  all_monitors = culipulse_http_monitor.x.id != ""
}
`, chID))
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{{
			Config: cfg,
			Check:  resource.TestCheckResourceAttr(routeAddr, "all_monitors", "true"),
		}},
	})
}

// Fix round 1: an UNKNOWN monitor_ids at plan time (one element is a monitor id not yet
// known because that monitor is created in the same apply), with all_monitors left unset,
// must likewise not be judged yet.
func TestChannelRouting_unknownMonitorIDsIsNotRejected(t *testing.T) {
	f := fakeapi.New(t)
	chID := f.SeedChannel("telegram", "unk-ids")
	cfg := fakeConfig(f, fmt.Sprintf(`
resource "culipulse_http_monitor" "x" {
  name             = "x"
  url              = "https://x.example.com"
  interval_seconds = 300
  agent_ids        = ["agt_sg"]
}

resource "culipulse_channel_routing" "r" {
  channel_id  = %q
  monitor_ids = [culipulse_http_monitor.x.id]
}
`, chID))
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{{
			Config: cfg,
			Check:  resource.TestCheckResourceAttr(routeAddr, "monitor_ids.#", "1"),
		}},
	})
}
