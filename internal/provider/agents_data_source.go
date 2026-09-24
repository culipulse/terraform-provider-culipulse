package provider

import (
	"context"
	"sort"

	"github.com/culipulse/terraform-provider-culipulse/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &agentsDataSource{}
	_ datasource.DataSourceWithConfigure = &agentsDataSource{}
)

type agentsDataSource struct{ client *client.Client }

type agentsModel struct {
	Region types.String `tfsdk:"region"`
	Kind   types.String `tfsdk:"kind"`
	IDs    types.List   `tfsdk:"ids"`
	Agents []agentModel `tfsdk:"agents"`
}

type agentModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	Kind       types.String `tfsdk:"kind"`
	Region     types.String `tfsdk:"region"`
	Version    types.String `tfsdk:"version"`
	LastSeenAt types.Int64  `tfsdk:"last_seen_at"`
}

func NewAgentsDataSource() datasource.DataSource { return &agentsDataSource{} }

func (d *agentsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agents"
}

func (d *agentsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (d *agentsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists the agents (checkpoints) your monitors can check from: CuliPulse's shared agents and your own. Use `ids` to fill a monitor's `agent_ids`.",
		Attributes: map[string]schema.Attribute{
			"region": schema.StringAttribute{
				Optional:    true,
				Description: "Only return agents in this region. Shared CuliPulse agents use `sg`, `eu`, `na`, `au`; your own agents use whatever region you gave them.",
			},
			"kind": schema.StringAttribute{
				Optional:    true,
				Description: "`first_party` for CuliPulse's shared agents, `tenant` for agents you run yourself.",
				Validators:  []validator.String{stringvalidator.OneOf("first_party", "tenant")},
			},
			"ids": schema.ListAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "IDs of the matching agents, sorted.",
			},
			"agents": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The matching agents, sorted by id.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":           schema.StringAttribute{Computed: true, Description: "Agent id."},
					"name":         schema.StringAttribute{Computed: true, Description: "Agent name."},
					"kind":         schema.StringAttribute{Computed: true, Description: "`first_party` or `tenant`."},
					"region":       schema.StringAttribute{Computed: true, Description: "Region label."},
					"version":      schema.StringAttribute{Computed: true, Description: "Agent software version."},
					"last_seen_at": schema.Int64Attribute{Computed: true, Description: "When the agent last checked in (Unix seconds)."},
				}},
			},
		},
	}
}

func (d *agentsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state agentsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	agents, err := d.client.ListAgents(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing agents", err.Error())
		return
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	// /v1/agents takes no filters (src/public-api.ts:478-512), so filter here.
	ids := []string{}
	state.Agents = []agentModel{}
	for _, a := range agents {
		region := ""
		if a.Region != nil {
			region = *a.Region
		}
		if known(state.Region) && region != state.Region.ValueString() {
			continue
		}
		if known(state.Kind) && a.Kind != state.Kind.ValueString() {
			continue
		}
		ids = append(ids, a.ID)
		m := agentModel{
			ID: types.StringValue(a.ID), Name: types.StringValue(a.Name), Kind: types.StringValue(a.Kind),
			Region: optString(a.Region), Version: optString(a.Version), LastSeenAt: types.Int64Null(),
		}
		if a.LastSeenAt != nil {
			m.LastSeenAt = types.Int64Value(*a.LastSeenAt)
		}
		state.Agents = append(state.Agents, m)
	}
	list, diags := types.ListValueFrom(ctx, types.StringType, ids)
	resp.Diagnostics.Append(diags...)
	state.IDs = list
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
