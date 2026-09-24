package provider

import (
	"context"
	"testing"

	"github.com/culipulse/terraform-provider-culipulse/internal/client"
	"github.com/culipulse/terraform-provider-culipulse/internal/fakeapi"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// F7: signing_secret must be persisted to state together with id, right after Create's POST
// succeeds — BEFORE the read-back GET that Create also does. If the read-back fails (a
// transient blip, the channel vanishing moments after creation, ...), a signing secret that
// only lived in the local `plan` variable would never reach Terraform state at all: the
// channel is orphaned (id recorded, so Delete can clean it up) but the one secret the API can
// never return again is lost for good.
//
// This calls the resource's Create method directly (bypassing terraform-plugin-testing, which
// has no way to force the read-back specifically to fail while the create itself succeeds) so
// the test can inspect resp.State exactly as the framework would persist it, even though
// Create ultimately returns an error.
func TestWebhookChannel_signingSecretPersistedBeforeFailingReadBack(t *testing.T) {
	ctx := context.Background()
	f := fakeapi.New(t)
	f.FailNextChannelGet = true // fails only the GET Create issues right after the POST

	c, err := client.New(client.Config{Endpoint: f.URL() + "/v1", Token: fakeapi.Token})
	if err != nil {
		t.Fatal(err)
	}
	r := &webhookChannelResource{client: c}

	schemaResp := &fwresource.SchemaResponse{}
	r.Schema(ctx, fwresource.SchemaRequest{}, schemaResp)
	sch := schemaResp.Schema
	nullVal := tftypes.NewValue(sch.Type().TerraformType(ctx), nil)

	plan := tfsdk.Plan{Schema: sch, Raw: nullVal}
	if diags := plan.Set(ctx, &webhookChannelModel{
		ID:            types.StringUnknown(),
		Name:          types.StringValue("flaky"),
		URL:           types.StringValue("https://hooks.example.com/a"),
		URLHost:       types.StringUnknown(),
		SigningSecret: types.StringUnknown(),
	}); diags.HasError() {
		t.Fatal(diags)
	}

	createResp := &fwresource.CreateResponse{State: tfsdk.State{Schema: sch, Raw: nullVal.Copy()}}
	r.Create(ctx, fwresource.CreateRequest{Plan: plan}, createResp)

	if !createResp.Diagnostics.HasError() {
		t.Fatal("want an error from the simulated read-back failure")
	}

	var got webhookChannelModel
	if diags := createResp.State.Get(ctx, &got); diags.HasError() {
		t.Fatal(diags)
	}
	if got.ID.IsNull() || got.ID.IsUnknown() {
		t.Fatal("id must be persisted even though the read-back failed")
	}
	if got.SigningSecret.IsNull() || got.SigningSecret.IsUnknown() {
		t.Fatalf("signing_secret must be persisted before the failing read-back, got %v", got.SigningSecret)
	}
}
