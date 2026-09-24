package provider

import (
	"context"
	"fmt"

	"github.com/culipulse/terraform-provider-culipulse/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &channelDataSource{}
	_ datasource.DataSourceWithConfigure = &channelDataSource{}
)

type channelDataSource struct{ client *client.Client }

type channelDataModel struct {
	Name    types.String `tfsdk:"name"`
	Type    types.String `tfsdk:"type"`
	ID      types.String `tfsdk:"id"`
	Status  types.String `tfsdk:"status"`
	Enabled types.Bool   `tfsdk:"enabled"`
}

func NewChannelDataSource() datasource.DataSource { return &channelDataSource{} }

func (d *channelDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_channel"
}

func (d *channelDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (d *channelDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up a notification channel by name, typically a Slack or Telegram channel you connected in the console, so you can route alerts to it.",
		Attributes: map[string]schema.Attribute{
			// A9.3: Slack channels are auto-named "Slack · #<channel>" (src/slack-oauth.ts:61-62).
			"name": schema.StringAttribute{Required: true, Description: "Channel name, exactly as shown in the console, e.g. `Slack · #ops-alerts`.",
				// A8.2: the server trims channel names, so a lookup value with leading/trailing
				// whitespace could never match a real channel.
				Validators: []validator.String{stringvalidator.LengthAtLeast(1), noSurroundingWhitespace()}},
			"type": schema.StringAttribute{Optional: true, Description: "`telegram`, `slack` or `webhook`. Needed only when two channels share a name.",
				Validators: []validator.String{stringvalidator.OneOf("telegram", "slack", "webhook")}},
			"id":      schema.StringAttribute{Computed: true, Description: "Channel id."},
			"status":  schema.StringAttribute{Computed: true, Description: "`connected`, or `pending` while it is still being set up."},
			"enabled": schema.BoolAttribute{Computed: true, Description: "Whether the channel currently sends alerts."},
		},
	}
}

func (d *channelDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m channelDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	chs, err := d.client.ListChannels(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing channels", err.Error())
		return
	}
	var matches []client.Channel
	for _, ch := range chs {
		if ch.Name == m.Name.ValueString() && (!known(m.Type) || ch.Type == m.Type.ValueString()) {
			matches = append(matches, ch)
		}
	}
	switch len(matches) {
	case 0:
		resp.Diagnostics.AddError("Channel not found", fmt.Sprintf(
			"No channel named %q. Connect it in the CuliPulse console (Notifications) first.", m.Name.ValueString()))
		return
	case 1:
	default:
		resp.Diagnostics.AddError("Ambiguous channel name", fmt.Sprintf(
			"%d channels are named %q. Set type to pick one.", len(matches), m.Name.ValueString()))
		return
	}
	ch := matches[0]
	m.ID = types.StringValue(ch.ID) // m.Type stays as configured: it is Optional, not Computed
	m.Status = types.StringValue(ch.Status)
	m.Enabled = types.BoolValue(ch.Enabled)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
