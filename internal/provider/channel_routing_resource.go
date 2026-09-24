package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/culipulse/terraform-provider-culipulse/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &channelRoutingResource{}
	_ resource.ResourceWithConfigure      = &channelRoutingResource{}
	_ resource.ResourceWithImportState    = &channelRoutingResource{}
	_ resource.ResourceWithValidateConfig = &channelRoutingResource{}
)

type channelRoutingResource struct{ client *client.Client }

type channelRoutingModel struct {
	ID          types.String `tfsdk:"id"`
	ChannelID   types.String `tfsdk:"channel_id"`
	AllMonitors types.Bool   `tfsdk:"all_monitors"`
	MonitorIDs  types.Set    `tfsdk:"monitor_ids"`
}

func NewChannelRoutingResource() resource.Resource { return &channelRoutingResource{} }

func (r *channelRoutingResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_channel_routing"
}

func (r *channelRoutingResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (r *channelRoutingResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Chooses which monitors send alerts to a notification channel (webhook, Slack or Telegram). " +
			"Use one per channel. Removing it sends every monitor's alerts to the channel again, which is the default.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "Same as `channel_id`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"channel_id": schema.StringAttribute{Required: true, Description: "The channel to route alerts to.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"all_monitors": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "Send alerts from every monitor, including ones created later. Default false."},
			"monitor_ids": schema.SetAttribute{Optional: true, ElementType: types.StringType,
				Description: "Monitors whose alerts go to this channel. Leave out when `all_monitors` is true. " +
					"Set it to an explicit empty list (`[]`) to intentionally route no monitors to this channel " +
					"(mute it) while keeping `all_monitors = false`; omitting both `all_monitors` and " +
					"`monitor_ids` is rejected because it doesn't say what you want.",
			},
		},
	}
}

func (r *channelRoutingResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m channelRoutingModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Unknown values (e.g. derived from a resource not yet applied) can't be judged yet;
	// defer to a later validation pass instead of false-alarming here.
	if m.AllMonitors.IsUnknown() || m.MonitorIDs.IsUnknown() {
		return
	}
	if m.AllMonitors.ValueBool() && !m.MonitorIDs.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("monitor_ids"), "Conflicting routing",
			"all_monitors = true already sends every monitor's alerts to this channel. Remove monitor_ids or set all_monitors = false.")
		return
	}
	// A9.2: an explicit empty list (monitor_ids = []) is an intentional mute and is allowed.
	// Only the fully-omitted case (monitor_ids is null, i.e. not set at all) is ambiguous.
	if !m.AllMonitors.ValueBool() && m.MonitorIDs.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("monitor_ids"), "Routing not specified",
			"Set all_monitors = true to send every monitor's alerts to this channel, or set monitor_ids "+
				"to the list of monitors whose alerts should go here (use monitor_ids = [] to route none).")
	}
}

func applyRouting(ctx context.Context, ch *client.Channel, m *channelRoutingModel) diag.Diagnostics {
	m.ID = types.StringValue(ch.ID)
	m.ChannelID = types.StringValue(ch.ID)
	m.AllMonitors = types.BoolValue(ch.AllMonitors)
	// monitor_ids is null only when all_monitors is true (the only case where it can be
	// omitted from config — see ValidateConfig). With all_monitors = false, an explicit
	// empty list is a valid, intentional state and must read back as an empty set, not
	// null, or Terraform sees "was [], but now null" as an inconsistent result.
	if ch.AllMonitors {
		m.MonitorIDs = types.SetNull(types.StringType)
		return nil
	}
	if len(ch.MonitorIDs) == 0 {
		m.MonitorIDs = types.SetValueMust(types.StringType, nil)
		return nil
	}
	ids := append([]string{}, ch.MonitorIDs...)
	sort.Strings(ids)
	v, d := types.SetValueFrom(ctx, types.StringType, ids)
	m.MonitorIDs = v
	return d
}

// write sends the full routing, reads it back, and reports ids the API dropped.
func (r *channelRoutingResource) write(ctx context.Context, m *channelRoutingModel) diag.Diagnostics {
	var diags diag.Diagnostics
	all := m.AllMonitors.ValueBool()
	ids := []string{}
	if !all && known(m.MonitorIDs) {
		diags.Append(m.MonitorIDs.ElementsAs(ctx, &ids, false)...)
	}
	sort.Strings(ids)
	channelID := m.ChannelID.ValueString()
	if err := r.client.UpdateChannel(ctx, channelID, client.ChannelPatch{AllMonitors: &all, MonitorIDs: &ids}); err != nil {
		diags.AddError("Error updating channel routing", err.Error())
		return diags
	}
	ch, err := r.client.GetChannel(ctx, channelID)
	if err != nil {
		diags.AddError("Error reading channel routing", err.Error())
		return diags
	}
	got := map[string]bool{}
	for _, id := range ch.MonitorIDs {
		got[id] = true
	}
	var missing []string
	for _, id := range ids {
		if !got[id] {
			missing = append(missing, id)
		}
	}
	diags.Append(applyRouting(ctx, ch, m)...)
	if len(missing) > 0 {
		diags.AddAttributeError(path.Root("monitor_ids"), "Unknown monitor ids", fmt.Sprintf(
			"These monitor ids were not found in this CuliPulse account, so their alerts can't be routed: %s",
			strings.Join(missing, ", ")))
	}
	return diags
}

func (r *channelRoutingResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan channelRoutingModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.write(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *channelRoutingResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state channelRoutingModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ch, err := r.client.GetChannel(ctx, state.ChannelID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading channel routing", err.Error())
		return
	}
	resp.Diagnostics.Append(applyRouting(ctx, ch, &state)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *channelRoutingResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan channelRoutingModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.write(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *channelRoutingResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state channelRoutingModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	all, ids := true, []string{}
	err := r.client.UpdateChannel(ctx, state.ChannelID.ValueString(), client.ChannelPatch{AllMonitors: &all, MonitorIDs: &ids})
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error resetting channel routing", err.Error())
	}
}

func (r *channelRoutingResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("channel_id"), req.ID)...)
}
