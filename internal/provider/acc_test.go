package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/culipulse/terraform-provider-culipulse/internal/client"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Acceptance tests (TF_ACC=1) run only against staging, as the isolated tenant
// tf-acc@culipulse.dev (plan solo, seeded by scripts/seed-tf-acc-tenant.mjs). Every object they
// create is named "tf-acc-*" so the sweepers in sweeper_test.go can clean up after a crash.
//
// What they re-check against the REAL worker (the unit tests check the same rules against the
// in-memory fake, so these guard the fake's fidelity for exactly these behaviors — not every
// behavior the fake mimics):
//   - http monitor: create → no-op re-plan (implicit after every step) → update → import,
//     with headers + secret header + bearer auth + an assertion; every PATCH carries the full
//     check_spec (bearer auth and the plain header survive a secret-header removal);
//     agent_ids [sg, eu] → [sg] with down_min_sources unset (quorum re-derived 2 → 1, Review
//     Focus #2); clearing sla_target and body_match; follow_redirects round-trips both ways
//     (sent as a JSON bool, stored as an integer column); an out-of-band delete plans a Create.
//   - heartbeat monitor: create, ping_url accepts a check-in, update keeps ping_url, import.
//   - webhook channel + routing + channel data source: signing secret, list routing,
//     all_monitors clears the leftover per-monitor rows, imports, an out-of-band channel delete
//     plans a Create (channel and routing), routing to an unknown monitor id fails naming it,
//     and a failing test delivery surfaces the plain-language hint.
// Not covered here (fake-only): 429 Retry-After handling, wrong-type import errors, the
// post-import url update of a webhook channel, basic auth, request_body/content_type.

func accPreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("CULIPULSE_API_TOKEN") == "" {
		t.Fatal("CULIPULSE_API_TOKEN must be set (source ~/.config/culipulse/tf-acc.env)")
	}
	if !strings.HasPrefix(os.Getenv("CULIPULSE_ENDPOINT"), "https://staging.culipulse.dev/") {
		t.Fatal("acceptance tests only run against staging: CULIPULSE_ENDPOINT=https://staging.culipulse.dev/v1")
	}
}

func accClient(t *testing.T) *client.Client {
	t.Helper()
	c, err := envClient()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// envClient is shared with the sweeper. It refuses anything but staging.
func envClient() (*client.Client, error) {
	ep := os.Getenv("CULIPULSE_ENDPOINT")
	if !strings.HasPrefix(ep, "https://staging.culipulse.dev/") {
		return nil, fmt.Errorf("refusing to run against %q: staging only", ep)
	}
	return client.New(client.Config{
		Endpoint: ep, Token: os.Getenv("CULIPULSE_API_TOKEN"), UserAgent: "terraform-provider-culipulse/acc",
		AccessClientID: os.Getenv("CF_ACCESS_CLIENT_ID"), AccessClientSecret: os.Getenv("CF_ACCESS_CLIENT_SECRET"),
	})
}

func accName(kind string) string { return "tf-acc-" + kind + "-" + acctest.RandString(6) }

// The two first-party shared agents on staging. Single-agent configs use ids[0] (A10.5) so a
// second agent enrolled in the same region can't change what the test exercises.
const accAgents = `
data "culipulse_agents" "sg" {
  kind   = "first_party"
  region = "sg"
}

data "culipulse_agents" "eu" {
  kind   = "first_party"
  region = "eu"
}
`

// accCheckDestroy confirms on the server that every monitor/channel in the pre-destroy state is gone.
func accCheckDestroy(t *testing.T) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		c := accClient(t)
		for addr, rs := range s.RootModule().Resources {
			var err error
			switch rs.Type {
			case "culipulse_http_monitor", "culipulse_heartbeat_monitor":
				_, err = c.GetMonitor(context.Background(), rs.Primary.ID)
			case "culipulse_webhook_channel":
				_, err = c.GetChannel(context.Background(), rs.Primary.ID)
			default:
				continue
			}
			if err == nil {
				return fmt.Errorf("%s (%s) still exists after destroy", addr, rs.Primary.ID)
			}
			if !client.IsNotFound(err) {
				return fmt.Errorf("%s: %w", addr, err)
			}
		}
		return nil
	}
}

func accMonitor(t *testing.T, s *terraform.State, addr string) (*client.Monitor, error) {
	rs, ok := s.RootModule().Resources[addr]
	if !ok {
		return nil, fmt.Errorf("%s not in state", addr)
	}
	return accClient(t).GetMonitor(context.Background(), rs.Primary.ID)
}

func checkRemoteRequest(t *testing.T, addr string, wantSecretNames []string, wantAuth string, wantHeaders int) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		m, err := accMonitor(t, s, addr)
		if err != nil {
			return err
		}
		var req client.RequestSpec
		if m.CheckSpec != nil && m.CheckSpec.Request != nil {
			req = *m.CheckSpec.Request
		}
		names := slices.Clone(req.SecretHeaderNames)
		slices.Sort(names)
		if !slices.Equal(names, wantSecretNames) {
			return fmt.Errorf("server secretHeaderNames = %v, want %v", names, wantSecretNames)
		}
		gotAuth := ""
		if req.Auth != nil {
			gotAuth = req.Auth.Type
		}
		if gotAuth != wantAuth {
			return fmt.Errorf("server auth type = %q, want %q", gotAuth, wantAuth)
		}
		if len(req.Headers) != wantHeaders {
			return fmt.Errorf("server headers = %v, want %d", req.Headers, wantHeaders)
		}
		return nil
	}
}

// checkRemoteMonitor asserts server-side fields the Terraform state alone can't prove.
func checkRemoteMonitor(t *testing.T, addr string, wantAgents int, wantQuorum int64, wantFollow int64, wantSLA *float64, wantBodyMatch *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		m, err := accMonitor(t, s, addr)
		if err != nil {
			return err
		}
		if len(m.AgentSources) != wantAgents {
			return fmt.Errorf("server agent_sources = %v, want %d", m.AgentSources, wantAgents)
		}
		if m.DownMinSources != wantQuorum {
			return fmt.Errorf("server down_min_sources = %d, want %d", m.DownMinSources, wantQuorum)
		}
		if m.FollowRedirects != wantFollow {
			return fmt.Errorf("server follow_redirects = %d, want %d", m.FollowRedirects, wantFollow)
		}
		switch {
		case wantSLA == nil && m.SLATarget != nil:
			return fmt.Errorf("server sla_target = %v, want cleared", *m.SLATarget)
		case wantSLA != nil && (m.SLATarget == nil || *m.SLATarget != *wantSLA):
			return fmt.Errorf("server sla_target = %v, want %v", m.SLATarget, *wantSLA)
		}
		got := ""
		if m.BodyMatch != nil {
			got = *m.BodyMatch
		}
		want := ""
		if wantBodyMatch != nil {
			want = *wantBodyMatch
		}
		if got != want {
			return fmt.Errorf("server body_match = %q, want %q", got, want)
		}
		return nil
	}
}

func captureID(addr string, dst *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[addr]
		if !ok {
			return fmt.Errorf("%s not in state", addr)
		}
		*dst = rs.Primary.ID
		return nil
	}
}

func expectCreate(addrs ...string) resource.ConfigPlanChecks {
	var cs []plancheck.PlanCheck
	for _, a := range addrs {
		cs = append(cs, plancheck.ExpectResourceAction(a, plancheck.ResourceActionCreate))
	}
	return resource.ConfigPlanChecks{PreApply: cs}
}

func f64(v float64) *float64  { return &v }
func accStr(v string) *string { return &v }

func TestAccHTTPMonitor(t *testing.T) {
	name := accName("http")
	cfg := func(n, agents, extra string) string {
		return accAgents + fmt.Sprintf(`
resource "culipulse_http_monitor" "m" {
  name             = %q
  url              = "https://example.com/"
  interval_seconds = 300
  agent_ids        = %s
  headers          = { "X-Env" = "acc" }
  bearer_token     = "acc-token"
  assertions = [
    { source = "response_time", op = "lt", value = "5000" },
  ]
%s
}
`, n, agents, extra)
	}
	const both = "[data.culipulse_agents.sg.ids[0], data.culipulse_agents.eu.ids[0]]"
	const sgOnly = "[data.culipulse_agents.sg.ids[0]]"
	var id string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: testProviderFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps: []resource.TestStep{
			{
				// Two agents, quorum left to the server (→ 2), secret header, sla + body_match set,
				// follow_redirects sent as JSON false.
				Config: cfg(name, both, `
  secret_headers   = { "X-Key" = "s3cret" }
  sla_target       = 99.5
  body_match       = "Example Domain"
  follow_redirects = false`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(httpAddr, "method", "GET"),
					resource.TestCheckResourceAttr(httpAddr, "expected_status", "2xx"),
					resource.TestCheckResourceAttr(httpAddr, "agent_ids.#", "2"),
					resource.TestCheckResourceAttr(httpAddr, "down_min_sources", "2"),
					resource.TestCheckResourceAttr(httpAddr, "follow_redirects", "false"),
					resource.TestCheckResourceAttr(httpAddr, "sla_target", "99.5"),
					resource.TestCheckResourceAttr(httpAddr, "body_match", "Example Domain"),
					checkRemoteRequest(t, httpAddr, []string{"X-Key"}, "bearer", 1),
					checkRemoteMonitor(t, httpAddr, 2, 2, 0, f64(99.5), accStr("Example Domain")),
				),
			},
			{
				// Review Focus #2 on the real server: shrink [sg, eu] → [sg] with down_min_sources
				// unset. Re-sending the stale quorum 2 would be a 400; the server must re-derive 1.
				// Also rename + drop the secret header: the PATCH carries the full check_spec, so
				// bearer auth and the plain header survive.
				Config: cfg(name+"-2", sgOnly, `
  sla_target       = 99.5
  body_match       = "Example Domain"
  follow_redirects = false`),
				ConfigPlanChecks: expectAction(httpAddr, plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(httpAddr, "name", name+"-2"),
					resource.TestCheckResourceAttr(httpAddr, "agent_ids.#", "1"),
					resource.TestCheckResourceAttr(httpAddr, "down_min_sources", "1"),
					checkRemoteRequest(t, httpAddr, nil, "bearer", 1),
					checkRemoteMonitor(t, httpAddr, 1, 1, 0, f64(99.5), accStr("Example Domain")),
				),
			},
			{
				// Clearing: remove sla_target and body_match from config; flip follow_redirects to
				// JSON true. The server must actually clear both fields.
				Config:           cfg(name+"-2", sgOnly, `  follow_redirects = true`),
				ConfigPlanChecks: expectAction(httpAddr, plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(httpAddr, "sla_target"),
					resource.TestCheckNoResourceAttr(httpAddr, "body_match"),
					resource.TestCheckResourceAttr(httpAddr, "follow_redirects", "true"),
					checkRemoteRequest(t, httpAddr, nil, "bearer", 1),
					checkRemoteMonitor(t, httpAddr, 1, 1, 1, nil, nil),
					captureID(httpAddr, &id),
				),
			},
			{
				ResourceName: httpAddr, ImportState: true, ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"secret_headers", "bearer_token", "basic_auth_username", "basic_auth_password", "status"},
			},
			{
				// Deleted out-of-band (e.g. in the console): refresh drops it and the plan re-creates it.
				PreConfig: func() {
					if err := accClient(t).DeleteMonitor(context.Background(), id); err != nil {
						t.Fatalf("out-of-band delete: %v", err)
					}
				},
				Config:           cfg(name+"-2", sgOnly, `  follow_redirects = true`),
				ConfigPlanChecks: expectAction(httpAddr, plancheck.ResourceActionCreate),
				Check: resource.ComposeAggregateTestCheckFunc(
					func(s *terraform.State) error {
						if got := s.RootModule().Resources[httpAddr].Primary.ID; got == id {
							return fmt.Errorf("monitor id unchanged (%s) after out-of-band delete", got)
						}
						return nil
					},
					checkRemoteRequest(t, httpAddr, nil, "bearer", 1),
				),
			},
		},
	})
}

func TestAccHeartbeatMonitor(t *testing.T) {
	name := accName("hb")
	cfg := func(interval int) string {
		return fmt.Sprintf(`
resource "culipulse_heartbeat_monitor" "hb" {
  name             = %q
  interval_seconds = %d
}
`, name, interval)
	}
	var ping string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: testProviderFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps: []resource.TestStep{
			{
				Config: cfg(300),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr(hbAddr, "ping_url", regexp.MustCompile(`^https://staging\.culipulse\.dev/ping/.+\..+`)),
					func(s *terraform.State) error {
						ping = s.RootModule().Resources[hbAddr].Primary.Attributes["ping_url"]
						// The URL must actually accept a check-in.
						req, _ := http.NewRequest(http.MethodGet, ping, nil)
						if id := os.Getenv("CF_ACCESS_CLIENT_ID"); id != "" {
							req.Header.Set("CF-Access-Client-Id", id)
							req.Header.Set("CF-Access-Client-Secret", os.Getenv("CF_ACCESS_CLIENT_SECRET"))
						}
						resp, err := http.DefaultClient.Do(req)
						if err != nil {
							return err
						}
						resp.Body.Close()
						if resp.StatusCode/100 != 2 {
							return fmt.Errorf("ping_url answered %d", resp.StatusCode)
						}
						return nil
					},
				),
			},
			{
				Config:           cfg(600),
				ConfigPlanChecks: expectAction(hbAddr, plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(hbAddr, "interval_seconds", "600"),
					func(s *terraform.State) error {
						if got := s.RootModule().Resources[hbAddr].Primary.Attributes["ping_url"]; got != ping {
							return fmt.Errorf("ping_url changed on update")
						}
						return nil
					},
				),
			},
			{ResourceName: hbAddr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"status"}},
		},
	})
}

func accWebhookURL() string {
	if u := os.Getenv("CULIPULSE_ACC_WEBHOOK_URL"); u != "" {
		return u
	}
	return "https://httpbin.org/post"
}

func TestAccWebhookChannelAndRouting(t *testing.T) {
	hook, mon := accName("hook"), accName("route")
	base := accAgents + fmt.Sprintf(`
resource "culipulse_webhook_channel" "h" {
  name = %q
  url  = %q
}

resource "culipulse_http_monitor" "a" {
  name             = %q
  url              = "https://example.com/"
  interval_seconds = 300
  agent_ids        = [data.culipulse_agents.sg.ids[0]]
}
`, hook, accWebhookURL(), mon)
	routed := base + `
resource "culipulse_channel_routing" "r" {
  channel_id  = culipulse_webhook_channel.h.id
  monitor_ids = [culipulse_http_monitor.a.id]
}

data "culipulse_channel" "lookup" {
  name       = culipulse_webhook_channel.h.name
  type       = "webhook"
  depends_on = [culipulse_webhook_channel.h]
}
`
	all := base + `
resource "culipulse_channel_routing" "r" {
  channel_id   = culipulse_webhook_channel.h.id
  all_monitors = true
}
`
	// A well-formed id that exists in no account: the API silently drops it (channel-service.ts
	// filters monitor_ids by tenant), so the provider must fail naming it.
	const ghost = "mon_00000000-0000-4000-8000-000000000000"
	unknown := base + fmt.Sprintf(`
resource "culipulse_channel_routing" "r" {
  channel_id  = culipulse_webhook_channel.h.id
  monitor_ids = [culipulse_http_monitor.a.id, %q]
}
`, ghost)
	var hookID string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: testProviderFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps: []resource.TestStep{
			{
				Config: routed,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr(hookAddr, "signing_secret", regexp.MustCompile(`^whsec_`)),
					resource.TestCheckResourceAttr(routeAddr, "monitor_ids.#", "1"),
					resource.TestCheckTypeSetElemAttrPair(routeAddr, "monitor_ids.*", "culipulse_http_monitor.a", "id"),
					resource.TestCheckResourceAttrPair("data.culipulse_channel.lookup", "id", hookAddr, "id"),
				),
			},
			{
				Config: all,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(routeAddr, "all_monitors", "true"),
					func(s *terraform.State) error {
						ch, err := accClient(t).GetChannel(context.Background(), s.RootModule().Resources[hookAddr].Primary.ID)
						if err != nil {
							return err
						}
						if !ch.AllMonitors || len(ch.MonitorIDs) != 0 {
							return fmt.Errorf("server routing all=%v ids=%v: leftover rows not cleared", ch.AllMonitors, ch.MonitorIDs)
						}
						return nil
					},
					captureID(hookAddr, &hookID),
				),
			},
			// Channel resources have no `status` attribute, so only the monitor imports ignore it (A10.4).
			{ResourceName: hookAddr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"url", "signing_secret"}},
			{ResourceName: routeAddr, ImportState: true, ImportStateVerify: true},
			{
				// Channel deleted out-of-band: both the channel and its routing drop out of state
				// on refresh and are planned as Creates (a Replace of the routing would try to
				// reset routing on a channel that no longer exists).
				PreConfig: func() {
					if err := accClient(t).DeleteChannel(context.Background(), hookID); err != nil {
						t.Fatalf("out-of-band delete: %v", err)
					}
				},
				Config:           routed,
				ConfigPlanChecks: expectCreate(hookAddr, routeAddr),
				Check: resource.ComposeAggregateTestCheckFunc(
					func(s *terraform.State) error {
						if got := s.RootModule().Resources[hookAddr].Primary.ID; got == hookID {
							return fmt.Errorf("channel id unchanged (%s) after out-of-band delete", got)
						}
						return nil
					},
					resource.TestCheckTypeSetElemAttrPair(routeAddr, "monitor_ids.*", "culipulse_http_monitor.a", "id"),
				),
			},
			{
				// Review Focus #4 on the real server.
				Config:      unknown,
				ExpectError: regexp.MustCompile(`(?s)not\s+found\s+in\s+this\s+CuliPulse\s+account.*` + regexp.QuoteMeta(ghost)),
			},
		},
	})
}

// A webhook whose endpoint answers non-2xx: the server's mandatory test delivery fails and
// nothing is created; the provider adds a plain-language hint (Task 8 branch the fake can't hit).
func TestAccWebhookChannel_testDeliveryFailureHint(t *testing.T) {
	url := os.Getenv("CULIPULSE_ACC_WEBHOOK_FAIL_URL")
	if url == "" {
		url = "https://httpbin.org/status/500"
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: testProviderFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
resource "culipulse_webhook_channel" "h" {
  name = %q
  url  = %q
}
`, accName("hookfail"), url),
			ExpectError: regexp.MustCompile(`(?s)test\s+delivery\s+failed.*answers\s+with\s+a\s+2xx`),
		}},
	})
}
