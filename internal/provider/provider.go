// Package provider implements the CuliPulse Terraform/OpenTofu provider on the Plugin
// Framework.
package provider

import (
	"context"
	"os"

	"github.com/culipulse/terraform-provider-culipulse/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = &culipulseProvider{}

type culipulseProvider struct{ version string }

type providerModel struct {
	APIToken types.String `tfsdk:"api_token"`
	Endpoint types.String `tfsdk:"endpoint"`
}

func New(version string) func() provider.Provider {
	return func() provider.Provider { return &culipulseProvider{version: version} }
}

func (p *culipulseProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "culipulse"
	resp.Version = p.version
}

func (p *culipulseProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage CuliPulse monitors and alert routing as code.",
		Attributes: map[string]schema.Attribute{
			"api_token": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "CuliPulse API token (starts with `cpk_`). It needs the `write` scope to create or change anything. Defaults to the `CULIPULSE_API_TOKEN` environment variable.",
			},
			"endpoint": schema.StringAttribute{
				Optional:    true,
				Description: "API base URL. Defaults to the `CULIPULSE_ENDPOINT` environment variable, then `https://culipulse.dev/v1`.",
			},
		},
	}
}

func (p *culipulseProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if cfg.APIToken.IsUnknown() || cfg.Endpoint.IsUnknown() {
		resp.Diagnostics.AddError("Provider configuration is not known yet",
			"api_token and endpoint must be known when Terraform plans; don't derive them from resources created in the same apply.")
		return
	}
	c, err := client.New(client.Config{
		Endpoint:           firstNonEmpty(cfg.Endpoint.ValueString(), os.Getenv("CULIPULSE_ENDPOINT"), client.DefaultEndpoint),
		Token:              firstNonEmpty(cfg.APIToken.ValueString(), os.Getenv("CULIPULSE_API_TOKEN")),
		UserAgent:          "terraform-provider-culipulse/" + p.version,
		AccessClientID:     os.Getenv("CF_ACCESS_CLIENT_ID"),
		AccessClientSecret: os.Getenv("CF_ACCESS_CLIENT_SECRET"),
	})
	if err != nil {
		resp.Diagnostics.AddError("Invalid CuliPulse provider configuration", err.Error())
		return
	}
	resp.ResourceData = c
	resp.DataSourceData = c
}

func (p *culipulseProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewHTTPMonitorResource,
		NewHeartbeatMonitorResource,
		NewWebhookChannelResource,
		NewChannelRoutingResource,
	}
}

func (p *culipulseProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewAgentsDataSource,
		NewChannelDataSource,
	}
}
