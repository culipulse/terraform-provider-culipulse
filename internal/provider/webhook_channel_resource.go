package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/culipulse/terraform-provider-culipulse/internal/client"
	"github.com/culipulse/terraform-provider-culipulse/internal/urlhost"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &webhookChannelResource{}
	_ resource.ResourceWithConfigure   = &webhookChannelResource{}
	_ resource.ResourceWithImportState = &webhookChannelResource{}
)

type webhookChannelResource struct{ client *client.Client }

type webhookChannelModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	URL           types.String `tfsdk:"url"`
	URLHost       types.String `tfsdk:"url_host"`
	SigningSecret types.String `tfsdk:"signing_secret"`
}

func NewWebhookChannelResource() resource.Resource { return &webhookChannelResource{} }

// checkWebhookURLHostMatches is F4: the URL can't be read back (import.sh), so right after
// `terraform import` the first URL Terraform sets is only ever RECORDED, never re-verified
// with a test delivery. Comparing the WHATWG host (hostname + non-default port,
// case-insensitive) — the same value channel-service.ts:126 stores as `url_host` from a JS
// `new URL(...)`'s `.host` getter — catches a typo, or the right hostname on the wrong port,
// before it's silently accepted. K1: comparing only url.Hostname() (dropping the port
// entirely) let a configured url on the wrong port match a channel it doesn't actually send
// to. apiHost is nil for a channel type that predates url_host or on very old data; there is
// nothing to compare against, so it's allowed through.
func checkWebhookURLHostMatches(configuredURL string, apiHost *string) diag.Diagnostics {
	var diags diag.Diagnostics
	if apiHost == nil {
		return diags
	}
	u, err := url.Parse(configuredURL)
	if err != nil || u.Hostname() == "" {
		return diags // the url schema validator already requires https://...; unreachable in practice
	}
	configuredHost := urlhost.WHATWGHost(u)
	if !strings.EqualFold(configuredHost, *apiHost) {
		diags.AddAttributeError(path.Root("url"), "URL doesn't match the channel", fmt.Sprintf(
			"this channel sends to %s, but the configuration says %s — fix the url, or remove and recreate the channel.",
			*apiHost, configuredHost))
	}
	return diags
}

func applyWebhookChannel(ch *client.Channel, m *webhookChannelModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if ch.Type != "webhook" {
		diags.AddError("Wrong channel type", fmt.Sprintf(
			"Channel %s is a %s channel, not a webhook. Slack and Telegram channels are connected in the console; look them up with the culipulse_channel data source.",
			ch.ID, ch.Type))
		return diags
	}
	m.ID = types.StringValue(ch.ID)
	m.Name = types.StringValue(ch.Name)
	m.URLHost = optString(ch.URLHost)
	if m.SigningSecret.IsUnknown() {
		m.SigningSecret = types.StringNull() // not recoverable after import
	}
	return diags
}

func (r *webhookChannelResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_webhook_channel"
}

func (r *webhookChannelResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (r *webhookChannelResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keepStr := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "Sends alerts as signed JSON POST requests to your own endpoint. Which monitors alert here is set " +
			"with `culipulse_channel_routing`; a new channel receives alerts for every monitor until you route it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "Channel id.", PlanModifiers: keepStr},
			"name": schema.StringAttribute{Required: true, Description: "Name shown in the console.",
				// A8.2: the server trims the name (channel-service.ts:57,186); a value with
				// leading/trailing whitespace would never match what's read back.
				Validators: []validator.String{stringvalidator.LengthAtLeast(1), noSurroundingWhitespace()}},
			"url": schema.StringAttribute{Required: true, Sensitive: true,
				Description: "The https:// endpoint that receives alerts. CuliPulse sends a test delivery before saving the channel, so the endpoint must accept a POST and answer 2xx. The URL can't be read back, so right after `terraform import` it is unknown: setting it for the first time only records it, after checking it points at the same host and port the channel already sends to (CuliPulse doesn't send a new test delivery, only compares host and port, and no new channel is created). Any later change replaces the channel and rotates the signing secret.",
				Validators:  []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^https://`), "must start with https://")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(
					func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
						// After `terraform import` the URL is unknown (null in state); setting it
						// only records it. Replacing would rotate the signing secret.
						resp.RequiresReplace = !req.StateValue.IsNull()
					},
					"Replaces the channel when the URL changes, except right after an import.",
					"Replaces the channel when the URL changes, except right after an import.",
				)}},
			"url_host": schema.StringAttribute{Computed: true, PlanModifiers: keepStr,
				Description: "Host and (non-default) port of `url`, as shown in the console."},
			"signing_secret": schema.StringAttribute{Computed: true, Sensitive: true, PlanModifiers: keepStr,
				Description: "Secret used to sign each delivery so your endpoint can verify it came from CuliPulse. Only available when Terraform created the channel (null after an import)."},
		},
	}
}

func (r *webhookChannelResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan webhookChannelModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	res, err := r.client.CreateChannel(ctx, client.ChannelCreate{Type: "webhook", Name: plan.Name.ValueString(), URL: plan.URL.ValueString()})
	if err != nil {
		detail := err.Error()
		var ae *client.APIError
		if errors.As(err, &ae) && strings.Contains(ae.Message, "test delivery failed") {
			detail += "\n\nCuliPulse sends a test POST to the URL before saving the channel. Make sure the endpoint is public, accepts POST, and answers with a 2xx status."
		}
		resp.Diagnostics.AddError("Error creating webhook channel", detail)
		return
	}
	// Record the id and signing secret first (F7) so a failing read-back doesn't orphan the
	// channel without ever having persisted the one secret Terraform will never see again.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), res.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("signing_secret"), res.SigningSecret)...)
	plan.SigningSecret = types.StringValue(res.SigningSecret)
	ch, err := r.client.GetChannel(ctx, res.ID)
	if err != nil {
		resp.Diagnostics.AddError("Error reading webhook channel after create", err.Error())
		return
	}
	resp.Diagnostics.Append(applyWebhookChannel(ch, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *webhookChannelResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state webhookChannelModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ch, err := r.client.GetChannel(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading webhook channel", err.Error())
		return
	}
	resp.Diagnostics.Append(applyWebhookChannel(ch, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *webhookChannelResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state webhookChannelModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.Name.Equal(state.Name) {
		name := plan.Name.ValueString()
		if err := r.client.UpdateChannel(ctx, plan.ID.ValueString(), client.ChannelPatch{Name: &name}); err != nil {
			resp.Diagnostics.AddError("Error updating webhook channel", err.Error())
			return
		}
	}
	// A url change only reaches Update right after an import (see the url plan modifier):
	// the API can't change it, so it is just recorded — after checking (F4) it points at the
	// same host the channel already sends to, since CuliPulse never re-verifies it here.
	ch, err := r.client.GetChannel(ctx, plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading webhook channel after update", err.Error())
		return
	}
	if state.URL.IsNull() && known(plan.URL) {
		resp.Diagnostics.Append(checkWebhookURLHostMatches(plan.URL.ValueString(), ch.URLHost)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	resp.Diagnostics.Append(applyWebhookChannel(ch, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *webhookChannelResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state webhookChannelModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteChannel(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting webhook channel", err.Error())
	}
}

func (r *webhookChannelResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
