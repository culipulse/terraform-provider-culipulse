package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/culipulse/terraform-provider-culipulse/internal/client"
	"github.com/culipulse/terraform-provider-culipulse/internal/fakeapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const httpAddr = "culipulse_http_monitor.m"

func expectAction(addr string, a plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, a)}}
}

func TestHTTPMonitor_lifecycle(t *testing.T) {
	f := fakeapi.New(t)
	create := fakeConfig(f, `
resource "culipulse_http_monitor" "m" {
  name             = "api"
  url              = "https://example.com/health"
  interval_seconds = 300
  agent_ids        = ["agt_sg"]
  headers          = { "X-Env" = "test" }
  secret_headers   = { "X-Key" = "s3cret" }
  bearer_token     = "tok"
  assertions = [
    { source = "json_body", op = "equals", path = "$.ok", value = "true" },
  ]
}
`)
	update := fakeConfig(f, `
resource "culipulse_http_monitor" "m" {
  name             = "api-renamed"
  url              = "https://example.com/health"
  interval_seconds = 300
  agent_ids        = ["agt_sg", "agt_eu"]
  headers          = { "X-Env" = "test" }
  bearer_token     = "tok"
  body_match       = "ok"
  sla_target       = 99.9
  assertions = [
    { source = "json_body", op = "equals", path = "$.ok", value = "true" },
  ]
}
`)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{
			{
				// The framework re-plans after every apply step and fails on a non-empty plan,
				// so this step also proves there is no perpetual diff.
				Config: create,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(httpAddr, "id"),
					resource.TestCheckResourceAttr(httpAddr, "method", "GET"),
					resource.TestCheckResourceAttr(httpAddr, "expected_status", "2xx"),
					resource.TestCheckResourceAttr(httpAddr, "timeout_ms", "10000"),
					resource.TestCheckResourceAttr(httpAddr, "follow_redirects", "true"),
					resource.TestCheckResourceAttr(httpAddr, "down_after_failures", "2"),
					resource.TestCheckResourceAttr(httpAddr, "up_after_successes", "1"),
					resource.TestCheckResourceAttr(httpAddr, "down_min_sources", "1"),
					resource.TestCheckResourceAttr(httpAddr, "status", "pending"),
					resource.TestCheckResourceAttr(httpAddr, "headers.X-Env", "test"),
					resource.TestCheckResourceAttr(httpAddr, "assertions.0.path", "$.ok"),
				),
			},
			{
				Config:           update,
				ConfigPlanChecks: expectAction(httpAddr, plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(httpAddr, "name", "api-renamed"),
					resource.TestCheckResourceAttr(httpAddr, "body_match", "ok"),
					resource.TestCheckResourceAttr(httpAddr, "sla_target", "99.9"),
					// agent_ids grew 1→2 with down_min_sources unset: the server default applies.
					resource.TestCheckResourceAttr(httpAddr, "down_min_sources", "2"),
					func(*terraform.State) error {
						cs := f.StoredCheckSpec("api-renamed")
						if cs.Request == nil || cs.Request.Auth == nil || cs.Request.Auth.Token != "tok" {
							return fmt.Errorf("bearer auth lost on update: %+v", cs.Request)
						}
						if cs.Request.Headers["X-Env"] != "test" {
							return fmt.Errorf("headers lost on update: %+v", cs.Request.Headers)
						}
						if len(cs.Request.SecretHeaders) != 0 {
							return fmt.Errorf("removed secret header still stored: %v", cs.Request.SecretHeaders)
						}
						if len(cs.Assertions) != 1 {
							return fmt.Errorf("assertions lost on update: %+v", cs.Assertions)
						}
						return nil
					},
				),
			},
			{
				ResourceName:            httpAddr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"secret_headers", "bearer_token", "basic_auth_username", "basic_auth_password"},
			},
			{
				PreConfig:        func() { f.DeleteMonitorNamed("api-renamed") },
				Config:           update,
				ConfigPlanChecks: expectAction(httpAddr, plancheck.ResourceActionCreate),
			},
		},
	})
}

func httpQuorumConfig(f *fakeapi.Server, name, agents, extra string) string {
	return fakeConfig(f, fmt.Sprintf(`
resource "culipulse_http_monitor" "m" {
  name             = %q
  url              = "https://example.com"
  interval_seconds = 300
  agent_ids        = %s
%s
}
`, name, agents, extra))
}

// Review Focus #2.
func TestHTTPMonitor_quorumFollowsShrinkingAgents(t *testing.T) {
	f := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{
			{Config: httpQuorumConfig(f, "q", `["agt_sg", "agt_eu"]`, ""),
				Check: resource.TestCheckResourceAttr(httpAddr, "down_min_sources", "2")},
			{Config: httpQuorumConfig(f, "q", `["agt_sg"]`, ""),
				Check: resource.TestCheckResourceAttr(httpAddr, "down_min_sources", "1")},
		},
	})
}

func TestHTTPMonitor_explicitQuorumIsSentOnEveryUpdate(t *testing.T) {
	f := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{
			{Config: httpQuorumConfig(f, "q1", `["agt_sg", "agt_eu"]`, "  down_min_sources = 1")},
			{
				Config: httpQuorumConfig(f, "q2", `["agt_sg", "agt_eu"]`, "  down_min_sources = 1"),
				Check: func(*terraform.State) error {
					if got := f.StoredMonitor("q2").DownMinSources; got != 1 {
						return fmt.Errorf("rename-only update reset down_min_sources to %d", got)
					}
					return nil
				},
			},
		},
	})
}

func TestHTTPMonitor_clearingOptionalFields(t *testing.T) {
	f := fakeapi.New(t)
	set := `  body_match   = "ok"
  sla_target   = 99
  request_body = "{}"
  content_type = "application/json"`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{
			{Config: httpQuorumConfig(f, "c", `["agt_sg"]`, set)},
			{
				Config: httpQuorumConfig(f, "c", `["agt_sg"]`, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(httpAddr, "body_match"),
					resource.TestCheckNoResourceAttr(httpAddr, "sla_target"),
					resource.TestCheckNoResourceAttr(httpAddr, "request_body"),
					func(*terraform.State) error {
						m := f.StoredMonitor("c")
						if m.BodyMatch != nil || m.SLATarget != nil {
							return fmt.Errorf("not cleared on server: body_match=%v sla=%v", m.BodyMatch, m.SLATarget)
						}
						if f.StoredCheckSpec("c").Request != nil {
							return fmt.Errorf("request settings not cleared: %+v", f.StoredCheckSpec("c").Request)
						}
						return nil
					},
				),
			},
		},
	})
}

// Review Focus #1.
func TestHTTPMonitor_importRejectsOtherTypes(t *testing.T) {
	f := fakeapi.New(t)
	id := f.SeedMonitor("heartbeat", "cron")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{{
			Config:        httpQuorumConfig(f, "cron", `["agt_sg"]`, ""),
			ResourceName:  httpAddr,
			ImportState:   true,
			ImportStateId: id,
			// A6.3: tolerate Terraform's diagnostic line wrapping between words.
			ExpectError: regexp.MustCompile(`(?s)is\s+a\s+heartbeat\s+monitor`),
		}},
	})
}

// F2: importing a monitor that already holds secrets CuliPulse can't return (a secret header,
// bearer auth, a secret body) must not error and must not lose the plain-language fields —
// applyHTTPMonitor's warning path (secretDriftWarning, unit-tested directly in
// http_monitor_mapping_test.go) runs during this Read without panicking or blocking the import.
// terraform-plugin-testing has no assertion for a Warning diagnostic's text, so this proves the
// wiring end-to-end (no crash, normal fields still read); the warning's own logic is pinned by
// the pure-function tests.
func TestHTTPMonitor_importWithUnmodeledSecretsStillReadsCleanly(t *testing.T) {
	f := fakeapi.New(t)
	id := f.SeedMonitor("http", "api-with-secrets") // SeedMonitor's target is https://seed.example.com
	f.SeedMonitorSecrets("api-with-secrets", map[string]string{"X-Key": "s3cret"}, "bearer", true)
	cfg := httpQuorumConfig(f, "api-with-secrets", `["agt_sg"]`, "")
	cfg = strings.Replace(cfg, "https://example.com", "https://seed.example.com", 1)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg, ResourceName: httpAddr,
				ImportState: true, ImportStateId: id, ImportStatePersist: true,
			},
			{
				// A follow-up plan-only step refreshes (re-runs Read) against the already-imported
				// monitor — proving secretDriftWarning keeps running on every Read without erroring
				// (an empty plan here also proves it's a Warning, not an Error: an Error would have
				// failed this step).
				Config: cfg, PlanOnly: true,
				Check: resource.TestCheckResourceAttr(httpAddr, "name", "api-with-secrets"),
			},
		},
	})
}

func TestHTTPMonitor_validation(t *testing.T) {
	f := fakeapi.New(t)
	cases := map[string]string{
		`lowercase method`: `  method = "get"`,
		`expected_status`:  `  expected_status = "20x"`, // F6: one x is not a valid wildcard; still rejected
		// K2: still rejected under classify.ts's isValidExpectedStatus (no non-empty token
		// survives the split/trim, or a token isn't a 3-digit code / "xx" wildcard).
		`expected_status empty`:             `  expected_status = ""`,
		`expected_status comma only`:        `  expected_status = ","`,
		`expected_status spaced comma only`: `  expected_status = " , "`,
		`expected_status 4-digit code`:      `  expected_status = "2000"`,
		`expected_status non-numeric`:       `  expected_status = "abc"`,
		`down_after_failures`:               `  down_after_failures = 9`,
		`both auth kinds`:                   "  bearer_token = \"t\"\n  basic_auth_username = \"u\"\n  basic_auth_password = \"p\"",
		`empty headers map`:                 `  headers = {}`,
		`bad header name`:                   `  headers = { "bad name" = "x" }`,
		`username needs password`:           `  basic_auth_username = "u"`,
		// A6.6: empty string turns into a type-only auth object server-side, which means "keep
		// the stored secret" — not "clear it" — so it must be rejected at plan time.
		`empty bearer token`: `  bearer_token = ""`,
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProviderFactories(),
				Steps: []resource.TestStep{{
					Config: httpQuorumConfig(f, "v", `["agt_sg"]`, extra),
					// Framework validator summaries — proves the plan-time validator fired,
					// not some unrelated failure. A6.3: tolerate line wrapping.
					ExpectError: regexp.MustCompile(`(?s)(Invalid\s+Attribute|Missing\s+Attribute)`),
				}},
			})
		})
	}
}

// F6: expected_status must accept what classify.ts's isValidExpectedStatus accepts — a
// case-insensitive "xx" wildcard and spaces after commas in a list — since the provider's
// validator used to reject values (like "2XX") the real server accepts.
func TestHTTPMonitor_expectedStatusAcceptsWhatTheServerAccepts(t *testing.T) {
	f := fakeapi.New(t)
	cases := map[string]struct{ name, value string }{
		"uppercase wildcard":       {"es-upper", "2XX"},
		"mixed-case wildcard":      {"es-mixed", "2Xx"},
		"spaced comma list":        {"es-spaced", "200, 301"},
		"exact code still accepts": {"es-exact", "200"},
		// K2: the provider's validator used to be a single whole-string regex that rejected a
		// trailing/leading/empty comma-separated token; classify.ts's isValidExpectedStatus drops
		// empty tokens and only requires at least one to survive. The server stores the string
		// verbatim (monitor-service.ts's create/update path does not trim or normalize it), so
		// these must round-trip through the fake unchanged, not just pass plan-time validation.
		"trailing comma":                 {"es-trail", "200,"},
		"leading comma":                  {"es-lead", ",200"},
		"empty token in the middle":      {"es-mid", "200,,301"},
		"wildcard and code with padding": {"es-pad", " 2XX , 301 "},
	}
	for tn, c := range cases {
		t.Run(tn, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProviderFactories(),
				Steps: []resource.TestStep{{
					Config: httpQuorumConfig(f, c.name, `["agt_sg"]`, fmt.Sprintf("  expected_status = %q", c.value)),
					Check:  resource.TestCheckResourceAttr(httpAddr, "expected_status", c.value),
				}},
			})
		})
	}
}

// A6.5: the server normalizes assertions away silently (check-spec.ts:125-168); a field that
// would be dropped on read-back must fail at plan time with a clear message instead of
// surfacing later as "Provider produced inconsistent result after apply".
func TestHTTPMonitor_assertionValidation(t *testing.T) {
	f := fakeapi.New(t)
	cases := map[string]string{
		// header + exists is server-valid (HEADER_OPS includes "exists", check-spec.ts:26); using
		// text_body + exists here would not be (TEXT_OPS excludes "exists", check-spec.ts:23).
		`value with exists`: `  assertions = [{ source = "header", op = "exists", name = "X-A", value = "x" }]`,
		`missing value`:     `  assertions = [{ source = "text_body", op = "contains" }]`,
		`path outside json_body`: `  assertions = [
    { source = "text_body", op = "contains", value = "x", path = "$.a" },
  ]`,
		`missing path on json_body`: `  assertions = [{ source = "json_body", op = "equals", value = "x" }]`,
		`name outside header`: `  assertions = [
    { source = "text_body", op = "contains", value = "x", name = "X-A" },
  ]`,
		`missing name on header`: `  assertions = [{ source = "header", op = "exists" }]`,
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProviderFactories(),
				Steps: []resource.TestStep{{
					Config:      httpQuorumConfig(f, "a", `["agt_sg"]`, extra),
					ExpectError: regexp.MustCompile(`(?s)Invalid\s+assertion`),
				}},
			})
		})
	}
}

// Fix round 1, finding 1: an UNKNOWN value (not yet resolved at plan time — every variable
// during `terraform validate`, or any reference to a not-yet-created resource's computed
// attribute during plan) must NOT be treated as "missing". `other`'s `id` is Computed and only
// known after `other` is actually created, so referencing it from `m`'s assertions makes that
// field unknown for `m`'s own plan — the config is still valid and the apply must succeed once
// the reference resolves.
func TestHTTPMonitor_assertionValidationToleratesUnknown(t *testing.T) {
	f := fakeapi.New(t)
	otherAndM := func(name, extra string) string {
		return fakeConfig(f, fmt.Sprintf(`
resource "culipulse_http_monitor" "other" {
  name             = "other-%s"
  url              = "https://example.com"
  interval_seconds = 300
  agent_ids        = ["agt_sg"]
}

resource "culipulse_http_monitor" "m" {
  name             = "m-%s"
  url              = "https://example.com"
  interval_seconds = 300
  agent_ids        = ["agt_sg"]
%s
}
`, name, name, extra))
	}
	t.Run("value unknown (op != exists)", func(t *testing.T) {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProviderFactories(),
			Steps: []resource.TestStep{{
				Config: otherAndM("value", `  assertions = [
    { source = "json_body", op = "equals", path = "$.ok", value = culipulse_http_monitor.other.id },
  ]`),
			}},
		})
	})
	t.Run("path unknown (json_body)", func(t *testing.T) {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: testProviderFactories(),
			Steps: []resource.TestStep{{
				Config: otherAndM("path", `  assertions = [
    { source = "json_body", op = "equals", path = culipulse_http_monitor.other.id, value = "true" },
  ]`),
			}},
		})
	})
}

// Fix round 1, finding 2: a quorumFollowsAgents implementation that always leaves
// down_min_sources unknown whenever config is null (instead of only when agent_ids also
// changed) would pass every other test in this file, yet would silently reset a
// previously-set quorum on any unrelated update — violating the global "every PATCH sends the
// planned down_min_sources" rule by sending nothing where a real value should have been resent.
func TestHTTPMonitor_explicitQuorumSurvivesRemovalFromConfig(t *testing.T) {
	f := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{
			{
				Config: httpQuorumConfig(f, "keep", `["agt_sg", "agt_eu"]`, "  down_min_sources = 1"),
				Check:  resource.TestCheckResourceAttr(httpAddr, "down_min_sources", "1"),
			},
			{
				// down_min_sources removed from config, agent_ids unchanged: must be a true no-op.
				Config:           httpQuorumConfig(f, "keep", `["agt_sg", "agt_eu"]`, ""),
				ConfigPlanChecks: expectAction(httpAddr, plancheck.ResourceActionNoop),
				Check:            resource.TestCheckResourceAttr(httpAddr, "down_min_sources", "1"),
			},
			{
				// An unrelated update (rename) must not reset the quorum the user set earlier.
				Config: httpQuorumConfig(f, "keep-renamed", `["agt_sg", "agt_eu"]`, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(httpAddr, "down_min_sources", "1"),
					func(*terraform.State) error {
						if got := f.StoredMonitor("keep-renamed").DownMinSources; got != 1 {
							return fmt.Errorf("unrelated update reset down_min_sources to %d", got)
						}
						return nil
					},
				),
			},
		},
	})
}

// A6.1: status is written asynchronously by probes (a MonitorDO can flip it between an
// apply's PATCH and its follow-up GET). If the schema kept the prior state value
// (UseStateForUnknown / "keepStr"), a genuine server-side change during Update would look
// like "Provider produced inconsistent result after apply" — a false positive, not a bug in
// the change itself. Using the A3.3 hook to flip status mid-PATCH proves the resource
// tolerates that instead of erroring.
func TestHTTPMonitor_statusChangeDuringUpdateIsNotAnError(t *testing.T) {
	f := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories(),
		Steps: []resource.TestStep{
			{Config: httpQuorumConfig(f, "s", `["agt_sg"]`, ""),
				Check: resource.TestCheckResourceAttr(httpAddr, "status", "pending")},
			{
				PreConfig: func() {
					f.OnMonitorPatch = func(m *client.Monitor) { m.Status = "down" }
				},
				Config: httpQuorumConfig(f, "s-renamed", `["agt_sg"]`, ""),
				Check:  resource.TestCheckResourceAttr(httpAddr, "status", "down"),
			},
		},
	})
}
