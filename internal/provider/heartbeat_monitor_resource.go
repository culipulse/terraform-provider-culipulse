package provider

import (
	"context"
	"fmt"

	"github.com/culipulse/terraform-provider-culipulse/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/float64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &heartbeatMonitorResource{}
	_ resource.ResourceWithConfigure   = &heartbeatMonitorResource{}
	_ resource.ResourceWithImportState = &heartbeatMonitorResource{}
)

type heartbeatMonitorResource struct{ client *client.Client }

type heartbeatMonitorModel struct {
	ID                types.String  `tfsdk:"id"`
	Name              types.String  `tfsdk:"name"`
	IntervalSeconds   types.Int64   `tfsdk:"interval_seconds"`
	GraceSeconds      types.Int64   `tfsdk:"grace_seconds"`
	DownAfterFailures types.Int64   `tfsdk:"down_after_failures"`
	SLATarget         types.Float64 `tfsdk:"sla_target"`
	PingURL           types.String  `tfsdk:"ping_url"`
	Status            types.String  `tfsdk:"status"`
}

func NewHeartbeatMonitorResource() resource.Resource { return &heartbeatMonitorResource{} }

// heartbeatMonitorWrite never sends check_spec, agent_sources or down_min_sources: a
// heartbeat has no request settings and no agents/quorum, and the server keeps the ping
// token on PATCH regardless (monitor-service.ts:636-643).
func heartbeatMonitorWrite(m heartbeatMonitorModel, create bool) *client.MonitorWrite {
	w := &client.MonitorWrite{
		Name:              m.Name.ValueString(),
		IntervalSeconds:   m.IntervalSeconds.ValueInt64(),
		GraceSeconds:      int64Ptr(m.GraceSeconds),
		DownAfterFailures: int64Ptr(m.DownAfterFailures),
		SLATarget:         float64Ptr(m.SLATarget),
	}
	if create {
		w.Type = "heartbeat"
	}
	return w
}

func applyHeartbeatMonitor(api *client.Monitor, origin string, m *heartbeatMonitorModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if api.Type != "heartbeat" {
		diags.AddError("Wrong monitor type", fmt.Sprintf(
			"Monitor %s is %s monitor, not a heartbeat monitor. Manage it with the matching culipulse_%s_monitor resource, if the provider has one.",
			api.ID, monitorTypeArticle(api.Type), api.Type))
		return diags
	}
	m.ID = types.StringValue(api.ID)
	m.Name = types.StringValue(api.Name)
	m.IntervalSeconds = types.Int64Value(api.IntervalSeconds)
	m.GraceSeconds = types.Int64Null()
	if api.HeartbeatGraceSeconds != nil {
		m.GraceSeconds = types.Int64Value(*api.HeartbeatGraceSeconds)
	}
	m.DownAfterFailures = types.Int64Value(api.DownAfterFailures)
	m.SLATarget = optFloat64(api.SLATarget)
	m.Status = types.StringValue(api.Status)
	if api.CheckSpec != nil && api.CheckSpec.Heartbeat != nil && api.CheckSpec.Heartbeat.Token != "" {
		// Ping route: src/api.ts:1157-1175, served at the site root, not under /v1.
		m.PingURL = types.StringValue(origin + "/ping/" + api.CheckSpec.Heartbeat.Token)
	} else if m.PingURL.IsUnknown() {
		m.PingURL = types.StringNull()
	}
	return diags
}

func (r *heartbeatMonitorResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_heartbeat_monitor"
}

func (r *heartbeatMonitorResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (r *heartbeatMonitorResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keepInt := []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}
	keepStr := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "A dead man's switch for cron jobs, backups and other scheduled work: your job calls `ping_url` " +
			"when it finishes, and CuliPulse alerts when the call doesn't arrive on time. Heartbeat settings made " +
			"in the console that this resource doesn't manage (such as a maximum run time) are reset on the next update.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "Monitor id.", PlanModifiers: keepStr},
			"name": schema.StringAttribute{Required: true, Description: "Name shown in the console and in alerts.",
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"interval_seconds": schema.Int64Attribute{Required: true,
				Description: "How often your job is expected to ping, in seconds.",
				Validators:  []validator.Int64{int64validator.AtLeast(1)}},
			"grace_seconds": schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: keepInt,
				Description: "Extra time allowed after the expected ping before it counts as missed. The default (20% " +
					"of the interval, at least 60) applies when the monitor is created; removing this from your " +
					"configuration afterward keeps the current value — set it explicitly to change it.",
				Validators: []validator.Int64{int64validator.AtLeast(1)}},
			"down_after_failures": schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: keepInt,
				Description: "Missed pings in a row before the monitor is marked down (1–5). Default 2.",
				Validators:  []validator.Int64{int64validator.Between(1, 5)}},
			"sla_target": schema.Float64Attribute{Optional: true,
				Description: "Uptime goal in percent, e.g. 99.9. Used by SLA reports.",
				Validators:  []validator.Float64{float64validator.Between(0.001, 100)}},
			"ping_url": schema.StringAttribute{Computed: true, Sensitive: true, PlanModifiers: keepStr,
				Description: "The URL your job calls (GET, POST or HEAD) when it succeeds. Add `/fail` to report a failure, or `/start` when it begins. Anyone with this URL can report for the monitor, so treat it as a secret."},
			// A7.1: NO UseStateForUnknown here, same reasoning as A6.1 on culipulse_http_monitor's
			// status attribute: probes/check-ins write status asynchronously, so it must plan as
			// unknown on every apply. Keeping the prior state value would make the framework treat
			// a genuine server-side change during an apply as "Provider produced inconsistent
			// result after apply".
			"status": schema.StringAttribute{Computed: true,
				Description: "Current state: `up`, `down`, `paused` or `pending`."},
		},
	}
}

func (r *heartbeatMonitorResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan heartbeatMonitorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := r.client.CreateMonitor(ctx, heartbeatMonitorWrite(plan, true))
	if err != nil {
		resp.Diagnostics.AddError("Error creating heartbeat monitor", err.Error())
		return
	}
	// Record the id first so a failing read-back doesn't orphan the monitor.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	api, err := r.client.GetMonitor(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading heartbeat monitor after create", err.Error())
		return
	}
	resp.Diagnostics.Append(applyHeartbeatMonitor(api, r.client.Origin(), &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *heartbeatMonitorResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state heartbeatMonitorModel
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
		resp.Diagnostics.AddError("Error reading heartbeat monitor", err.Error())
		return
	}
	resp.Diagnostics.Append(applyHeartbeatMonitor(api, r.client.Origin(), &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *heartbeatMonitorResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan heartbeatMonitorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.UpdateMonitor(ctx, plan.ID.ValueString(), heartbeatMonitorWrite(plan, false)); err != nil {
		resp.Diagnostics.AddError("Error updating heartbeat monitor", err.Error())
		return
	}
	api, err := r.client.GetMonitor(ctx, plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading heartbeat monitor after update", err.Error())
		return
	}
	resp.Diagnostics.Append(applyHeartbeatMonitor(api, r.client.Origin(), &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *heartbeatMonitorResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state heartbeatMonitorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteMonitor(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting heartbeat monitor", err.Error())
	}
}

func (r *heartbeatMonitorResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
