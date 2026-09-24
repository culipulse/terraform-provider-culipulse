package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// quorumFollowsAgents keeps a server-defaulted down_min_sources stable across plans, EXCEPT
// when the user hasn't set it and agent_ids changes. Then the value stays unknown, the
// provider omits it, and the server recomputes the default for the new agent count
// (consensus.ts:46-52). Plain UseStateForUnknown would re-send the stale value: shrinking
// from 2 agents to 1 would send down_min_sources=2, which the API rejects (must be 1..N).
type quorumFollowsAgents struct{}

func (quorumFollowsAgents) Description(context.Context) string {
	return "Keeps the server-chosen quorum unless agent_ids changes."
}

func (m quorumFollowsAgents) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (quorumFollowsAgents) PlanModifyInt64(ctx context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	if !req.ConfigValue.IsNull() || req.StateValue.IsNull() || !req.PlanValue.IsUnknown() {
		return
	}
	var planAgents, stateAgents types.Set
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("agent_ids"), &planAgents)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("agent_ids"), &stateAgents)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if planAgents.Equal(stateAgents) {
		resp.PlanValue = req.StateValue
	}
}
