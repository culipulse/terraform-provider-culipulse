package provider

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/culipulse/terraform-provider-culipulse/internal/client"
	"github.com/culipulse/terraform-provider-culipulse/internal/fakeapi"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const hbAddr = "culipulse_heartbeat_monitor.hb"

func hbConfig(f *fakeapi.Server, interval, down int) string {
	return fakeConfig(f, fmt.Sprintf(`
resource "culipulse_heartbeat_monitor" "hb" {
  name                = "nightly-backup"
  interval_seconds    = %d
  down_after_failures = %d
}
`, interval, down))
}

func TestHeartbeatMonitorWrite_neverSendsCheckSpecOrSources(t *testing.T) {
	m := heartbeatMonitorModel{Name: types.StringValue("hb"), IntervalSeconds: types.Int64Value(300),
		GraceSeconds: types.Int64Value(60), DownAfterFailures: types.Int64Unknown(), SLATarget: types.Float64Null()}
	for _, create := range []bool{true, false} {
		b, _ := json.Marshal(heartbeatMonitorWrite(m, create))
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		for _, k := range []string{"check_spec", "agent_sources", "target", "down_min_sources"} {
			if _, ok := body[k]; ok {
				t.Errorf("create=%v: %s must not be sent", create, k)
			}
		}
		if (body["type"] == "heartbeat") != create {
			t.Errorf("create=%v: type = %v", create, body["type"])
		}
		if body["grace_seconds"] != float64(60) {
			t.Errorf("grace_seconds = %v", body["grace_seconds"])
		}
	}
}

func TestApplyHeartbeatMonitor_rejectsHTTP(t *testing.T) {
	var m heartbeatMonitorModel
	d := applyHeartbeatMonitor(&client.Monitor{ID: "mon_1", Type: "http"}, "https://x", &m)
	if !d.HasError() || !strings.Contains(d.Errors()[0].Detail(), "is an HTTP monitor") {
		t.Fatalf("%v", d)
	}
}

func TestHeartbeatMonitor_lifecycle(t *testing.T) {
	f := fakeapi.New(t)
	var firstPing string
	pingURL := func(s *terraform.State) string {
		return s.RootModule().Resources[hbAddr].Primary.Attributes["ping_url"]
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: hbConfig(f, 300, 2),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(hbAddr, "grace_seconds", "60"),
					resource.TestCheckResourceAttr(hbAddr, "status", "pending"),
					resource.TestMatchResourceAttr(hbAddr, "ping_url",
						regexp.MustCompile("^"+regexp.QuoteMeta(f.URL())+`/ping/mon_\d+\.fakesecret\d+$`)),
					func(s *terraform.State) error { firstPing = pingURL(s); return nil },
				),
			},
			{
				Config:           hbConfig(f, 600, 3),
				ConfigPlanChecks: expectAction(hbAddr, plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(hbAddr, "interval_seconds", "600"),
					resource.TestCheckResourceAttr(hbAddr, "down_after_failures", "3"),
					resource.TestCheckResourceAttr(hbAddr, "grace_seconds", "60"),
					func(s *terraform.State) error {
						if got := pingURL(s); got != firstPing {
							return fmt.Errorf("ping_url changed on update: %s -> %s", firstPing, got)
						}
						return nil
					},
				),
			},
			{ResourceName: hbAddr, ImportState: true, ImportStateVerify: true},
			{
				PreConfig:        func() { f.DeleteMonitorNamed("nightly-backup") },
				Config:           hbConfig(f, 600, 3),
				ConfigPlanChecks: expectAction(hbAddr, plancheck.ResourceActionCreate),
			},
		},
	})
}

func TestHeartbeatMonitor_importRejectsHTTP(t *testing.T) {
	f := fakeapi.New(t)
	id := f.SeedMonitor("http", "site")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{{
			Config: hbConfig(f, 300, 2), ResourceName: hbAddr, ImportState: true, ImportStateId: id,
			// A7.2 (= A6.3): tolerate Terraform's diagnostic line wrapping. F5: "an HTTP", not
			// "a http" — a grammar fix.
			ExpectError: regexp.MustCompile(`(?s)is\s+an\s+HTTP\s+monitor`),
		}},
	})
}

// A7.1: status is written asynchronously by probes (a MonitorDO can flip it between an
// apply's PATCH and its follow-up GET). If the schema kept the prior state value
// (UseStateForUnknown / "keepStr"), a genuine server-side change during Update would look
// like "Provider produced inconsistent result after apply" — a false positive, not a bug in
// the change itself. Using the A3.3 hook to flip status mid-PATCH proves the resource
// tolerates that instead of erroring. Mirrors TestHTTPMonitor_statusChangeDuringUpdateIsNotAnError.
func TestHeartbeatMonitor_statusChangeDuringUpdateIsNotAnError(t *testing.T) {
	f := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{
			{Config: hbConfig(f, 300, 2),
				Check: resource.TestCheckResourceAttr(hbAddr, "status", "pending")},
			{
				PreConfig: func() {
					f.OnMonitorPatch = func(m *client.Monitor) { m.Status = "down" }
				},
				Config: hbConfig(f, 600, 3),
				Check:  resource.TestCheckResourceAttr(hbAddr, "status", "down"),
			},
		},
	})
}
