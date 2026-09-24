package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/culipulse/terraform-provider-culipulse/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/float64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &httpMonitorResource{}
	_ resource.ResourceWithConfigure      = &httpMonitorResource{}
	_ resource.ResourceWithImportState    = &httpMonitorResource{}
	_ resource.ResourceWithValidateConfig = &httpMonitorResource{}
)

type httpMonitorResource struct{ client *client.Client }

func NewHTTPMonitorResource() resource.Resource { return &httpMonitorResource{} }

var headerNameRE = regexp.MustCompile(`^[A-Za-z0-9-]+$`) // check-spec.ts:68

func (r *httpMonitorResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_http_monitor"
}

func (r *httpMonitorResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (r *httpMonitorResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keepInt := []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}
	keepStr := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "Checks a website or API over HTTP(S) from one or more agents and alerts when it goes down. " +
			"This resource owns the monitor's whole request setup (headers, auth, body, assertions): anything " +
			"set in the console that isn't in your configuration is removed on the next apply.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "Monitor id.", PlanModifiers: keepStr},
			"name": schema.StringAttribute{Required: true, Description: "Name shown in the console and in alerts.",
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"url": schema.StringAttribute{Required: true, Description: "The http:// or https:// address to check.",
				Validators: []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^https?://`), "must start with http:// or https://")}},
			"interval_seconds": schema.Int64Attribute{Required: true,
				Description: "How often to check, in seconds. Your plan sets the minimum (Free: 300).",
				Validators:  []validator.Int64{int64validator.AtLeast(1)}},
			"timeout_ms": schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: keepInt,
				Description: "How long to wait for an answer, in milliseconds. At most 10000 and less than the interval. " +
					"The default (10000) applies when the monitor is created; removing this from your configuration " +
					"afterward keeps the current value — set it explicitly to change it.",
				Validators: []validator.Int64{int64validator.Between(1, 10000)}},
			"method": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: keepStr,
				Description: "HTTP method: GET, HEAD, POST, PUT, PATCH, DELETE or OPTIONS. The default (GET) applies when " +
					"the monitor is created; removing this from your configuration afterward keeps the current value — " +
					"set it explicitly to change it.",
				Validators: []validator.String{stringvalidator.OneOf("GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS")}},
			"expected_status": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: keepStr,
				Description: "Which status codes count as up, e.g. `200`, `2xx` or `200, 301` (case-insensitive). The " +
					"default (`2xx`) applies when the monitor is created; removing this from your configuration " +
					"afterward keeps the current value — set it explicitly to change it.",
				// K2: expectedStatusValidator mirrors classify.ts's isValidExpectedStatus token by
				// token (split on ",", trim, drop empty tokens, ≥1 left, each a 3-digit code or a
				// case-insensitive "xx" wildcard) instead of one whole-string regex — the regex
				// this replaced was close but stricter than the server: it rejected values like a
				// trailing comma ("200,") or an empty token in the middle ("200,,301") that the
				// server happily accepts.
				Validators: []validator.String{expectedStatusValid()}},
			"body_match": schema.StringAttribute{Optional: true,
				Description: "Text the response body must contain.",
				Validators:  []validator.String{stringvalidator.LengthAtLeast(1)}},
			"follow_redirects": schema.BoolAttribute{Optional: true, Computed: true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
				Description:   "Follow redirects before judging the answer. Default true."},
			"down_after_failures": schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: keepInt,
				Description: "Failed checks in a row before the monitor is marked down (1-5). The default (2) applies " +
					"when the monitor is created; removing this from your configuration afterward keeps the current " +
					"value — set it explicitly to change it.",
				Validators: []validator.Int64{int64validator.Between(1, 5)}},
			"up_after_successes": schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: keepInt,
				Description: "Successful checks in a row before a down monitor is marked up again (1-5). The default (1) " +
					"applies when the monitor is created; removing this from your configuration afterward keeps the " +
					"current value — set it explicitly to change it.",
				Validators: []validator.Int64{int64validator.Between(1, 5)}},
			"down_min_sources": schema.Int64Attribute{Optional: true, Computed: true,
				PlanModifiers: []planmodifier.Int64{quorumFollowsAgents{}},
				Description: "How many agents must see the failure before the monitor is marked down (1 to the number of agents). " +
					"The default (2 when there are at least two agents, else 1) applies when the monitor is created and is " +
					"picked again whenever `agent_ids` changes; removing this from your configuration otherwise keeps the " +
					"current value — set it explicitly to change it.",
				Validators: []validator.Int64{int64validator.AtLeast(1)}},
			"sla_target": schema.Float64Attribute{Optional: true,
				Description: "Uptime goal in percent, e.g. 99.9. Used by SLA reports.",
				Validators:  []validator.Float64{float64validator.Between(0.001, 100)}},
			"agent_ids": schema.SetAttribute{Required: true, ElementType: types.StringType,
				Description: "Agents that run the check. Use the `culipulse_agents` data source to look them up.",
				Validators:  []validator.Set{setvalidator.SizeAtLeast(1)}},
			"headers": schema.MapAttribute{Optional: true, ElementType: types.StringType,
				Description: "Request headers sent with every check. Visible in the console.",
				Validators: []validator.Map{mapvalidator.SizeAtLeast(1),
					mapvalidator.KeysAre(stringvalidator.RegexMatches(headerNameRE, "letters, digits and dashes only"))}},
			"secret_headers": schema.MapAttribute{Optional: true, Sensitive: true, ElementType: types.StringType,
				Description: "Request headers whose values are stored encrypted and never shown again, e.g. API keys. " +
					"CuliPulse can't return them, so changes made outside Terraform are not detected.",
				Validators: []validator.Map{mapvalidator.SizeAtLeast(1),
					mapvalidator.KeysAre(stringvalidator.RegexMatches(headerNameRE, "letters, digits and dashes only"))}},
			"basic_auth_username": schema.StringAttribute{Optional: true,
				Description: "Username for HTTP basic auth. Needs `basic_auth_password`.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
					stringvalidator.AlsoRequires(path.MatchRoot("basic_auth_password")),
					stringvalidator.ConflictsWith(path.MatchRoot("bearer_token")),
				}},
			"basic_auth_password": schema.StringAttribute{Optional: true, Sensitive: true,
				Description: "Password for HTTP basic auth. Stored encrypted; changes made outside Terraform are not detected.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
					stringvalidator.AlsoRequires(path.MatchRoot("basic_auth_username")),
				}},
			"bearer_token": schema.StringAttribute{Optional: true, Sensitive: true,
				Description: "Sent as `Authorization: Bearer <token>`. Stored encrypted; changes made outside Terraform are not detected.",
				// A6.6: a type-only auth object ({"type":"bearer"}, no token) means "keep the
				// stored secret" server-side (check-spec.ts:108-112), and the client's omitempty
				// turns bearer_token = "" into exactly that object — so an empty string here
				// would silently do nothing rather than clear/set the token. Reject it at plan time.
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"request_body": schema.StringAttribute{Optional: true,
				Description: "Body sent with the request (for POST/PUT/PATCH checks).",
				Validators:  []validator.String{stringvalidator.LengthAtLeast(1)}},
			"content_type": schema.StringAttribute{Optional: true,
				Description: "Content-Type for `request_body`, e.g. `application/json`.",
				Validators:  []validator.String{stringvalidator.LengthAtLeast(1)}},
			"assertions": schema.ListNestedAttribute{Optional: true,
				Description: "Extra conditions the response must meet for the monitor to be up.",
				Validators:  []validator.List{listvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"source": schema.StringAttribute{Required: true,
						Description: "What to check: `text_body`, `json_body`, `header`, `response_time` or `cert_expiry`.",
						Validators:  []validator.String{stringvalidator.OneOf("text_body", "json_body", "header", "response_time", "cert_expiry")}},
					"op": schema.StringAttribute{Required: true,
						Description: "Comparison, e.g. `contains`, `equals`, `lt`, `gt`, `exists`, `matches`. Which ones are allowed depends on `source`."},
					"value": schema.StringAttribute{Optional: true,
						Description: "Value to compare against (milliseconds for `response_time`, days for `cert_expiry`). " +
							"Required for every op except `exists`, and not allowed with `exists`.",
						Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
					"path": schema.StringAttribute{Optional: true,
						Description: "JSON path, for `json_body`, e.g. `$.status`. Required when `source = \"json_body\"`, not allowed otherwise.",
						Validators:  []validator.String{stringvalidator.LengthAtLeast(1)}},
					"name": schema.StringAttribute{Optional: true,
						Description: "Header name, for `header`. Required when `source = \"header\"`, not allowed otherwise.",
						Validators:  []validator.String{stringvalidator.LengthAtLeast(1)}},
				}},
			},
			// A6.1: NO UseStateForUnknown here. status is written asynchronously by probes
			// (a MonitorDO can flip it between an apply's PATCH and its follow-up GET), so it
			// must plan as unknown on every apply; keeping the prior state value would make the
			// framework treat a genuine server-side change as "Provider produced inconsistent
			// result after apply".
			"status": schema.StringAttribute{Computed: true,
				Description: "Current state: `up`, `down`, `degraded`, `paused` or `pending`."},
		},
	}
}

// ValidateConfig enforces the assertion shape the server normalizes away silently
// (check-spec.ts:125-168), so a config field that would be dropped on read-back fails at plan
// time with a clear message instead of surfacing later as "inconsistent result after apply".
// A6.5.
func (r *httpMonitorResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg httpMonitorModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || !known(cfg.Assertions) {
		return
	}
	var items []assertionModel
	resp.Diagnostics.Append(cfg.Assertions.ElementsAs(ctx, &items, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for i, a := range items {
		if !known(a.Source) || !known(a.Op) {
			continue // still unknown (interpolated); nothing to validate yet
		}
		source := a.Source.ValueString()
		op := a.Op.ValueString()
		base := path.Root("assertions").AtListIndex(i)

		// Fix round 1, finding 1: "required" checks below use IsNull(), NOT known()/!known().
		// An UNKNOWN value (every variable during `terraform validate`; a reference to a
		// not-yet-created resource's computed attribute during plan) is not yet decided — it
		// may well resolve to something valid — so it must never be reported as "missing". Only
		// a value the config explicitly leaves out (null) is actually missing. The "forbidden"
		// checks below intentionally keep using known(): an unknown value MIGHT resolve to
		// something, so we don't yet know it's wrongly set either, and stay silent until it is.
		switch {
		case op == "exists" && known(a.Value):
			resp.Diagnostics.AddAttributeError(base.AtName("value"), "Invalid assertion",
				fmt.Sprintf("assertions[%d]: `value` is not allowed when op = \"exists\" (\"exists\" only checks presence). Remove `value`.", i))
		case op != "exists" && a.Value.IsNull():
			resp.Diagnostics.AddAttributeError(base.AtName("value"), "Invalid assertion",
				fmt.Sprintf("assertions[%d]: `value` is required for op %q. Every op except \"exists\" needs something to compare against.", i, op))
		}

		if source == "json_body" {
			if a.Path.IsNull() {
				resp.Diagnostics.AddAttributeError(base.AtName("path"), "Invalid assertion",
					fmt.Sprintf("assertions[%d]: `path` is required when source = \"json_body\", e.g. `$.status`.", i))
			}
		} else if known(a.Path) {
			resp.Diagnostics.AddAttributeError(base.AtName("path"), "Invalid assertion",
				fmt.Sprintf("assertions[%d]: `path` only applies when source = \"json_body\"; remove it here.", i))
		}

		if source == "header" {
			if a.Name.IsNull() {
				resp.Diagnostics.AddAttributeError(base.AtName("name"), "Invalid assertion",
					fmt.Sprintf("assertions[%d]: `name` is required when source = \"header\" (the header to check).", i))
			}
		} else if known(a.Name) {
			resp.Diagnostics.AddAttributeError(base.AtName("name"), "Invalid assertion",
				fmt.Sprintf("assertions[%d]: `name` only applies when source = \"header\"; remove it here.", i))
		}
	}
}

func (r *httpMonitorResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan httpMonitorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	w, diags := httpMonitorWrite(ctx, plan, true)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := r.client.CreateMonitor(ctx, w)
	if err != nil {
		resp.Diagnostics.AddError("Error creating http monitor", err.Error())
		return
	}
	// Record the id first so a failing read-back doesn't orphan the monitor.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	api, err := r.client.GetMonitor(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading http monitor after create", err.Error())
		return
	}
	resp.Diagnostics.Append(applyHTTPMonitor(ctx, api, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if msg, ok := secretDriftWarning(api, plan); ok {
		resp.Diagnostics.AddWarning("Secrets not in your configuration", msg)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *httpMonitorResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state httpMonitorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	api, err := r.client.GetMonitor(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading http monitor", err.Error())
		return
	}
	resp.Diagnostics.Append(applyHTTPMonitor(ctx, api, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if msg, ok := secretDriftWarning(api, state); ok {
		resp.Diagnostics.AddWarning("Secrets not in your configuration", msg)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *httpMonitorResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan httpMonitorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	w, diags := httpMonitorWrite(ctx, plan, false)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.UpdateMonitor(ctx, plan.ID.ValueString(), w); err != nil {
		resp.Diagnostics.AddError("Error updating http monitor", err.Error())
		return
	}
	api, err := r.client.GetMonitor(ctx, plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading http monitor after update", err.Error())
		return
	}
	resp.Diagnostics.Append(applyHTTPMonitor(ctx, api, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if msg, ok := secretDriftWarning(api, plan); ok {
		resp.Diagnostics.AddWarning("Secrets not in your configuration", msg)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *httpMonitorResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state httpMonitorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteMonitor(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting http monitor", err.Error())
	}
}

func (r *httpMonitorResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
