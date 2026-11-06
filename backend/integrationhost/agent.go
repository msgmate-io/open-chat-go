package integrationhost

import (
	"context"

	"backend/api/msgmate"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

// agentAdapter implements the HostVersion 5 Agent surface: it bridges an
// integration's turn request onto the core's generic tool loop
// (msgmate.RunAgentTurn), so a compiled-in coding integration owns its prompt,
// tools and turn hooks while the core keeps provider streaming, persistence,
// streaming frames, confirmations, compaction and the generation registry.
type agentAdapter struct{}

func (agentAdapter) Run(ctx context.Context, req integrationinterface.AgentRequest) (integrationinterface.AgentResult, error) {
	result, err := msgmate.RunAgentTurn(ctx, msgmate.AgentTurnSpec{
		SystemPrompt: req.SystemPrompt,
		Tools:        req.Tools,
		ToolInit:     req.ToolInit,
		Hooks:        adaptAgentHooks(req.Hooks),
	})
	if err != nil {
		return integrationinterface.AgentResult{}, err
	}
	return integrationinterface.AgentResult{
		MessageUUID: result.MessageUUID,
		Text:        result.Text,
		Finished:    result.Finished,
	}, nil
}

// adaptAgentHooks maps the stable hook struct onto the core's local one, leaving
// a hook nil when the integration did not declare it so the core keeps its
// default behaviour.
func adaptAgentHooks(h integrationinterface.AgentHooks) msgmate.AgentTurnHooks {
	out := msgmate.AgentTurnHooks{}
	if h.PreCallInit != nil {
		fn := h.PreCallInit
		out.PreCallInit = func(in msgmate.AgentInitContext) map[string]map[string]interface{} {
			return fn(integrationinterface.AgentInitContext{
				Tools:  in.Tools,
				Init:   in.Init,
				Tags:   in.Tags,
				Config: in.Config,
			})
		}
	}
	if h.NormalizeToolCall != nil {
		out.NormalizeToolCall = h.NormalizeToolCall
	}
	if h.ToolFailureGuidance != nil {
		out.ToolFailureGuidance = h.ToolFailureGuidance
	}
	if h.ToolBudgetExempt != nil {
		out.ToolBudgetExempt = h.ToolBudgetExempt
	}
	if h.ToolSchemaExtend != nil {
		out.ToolSchemaExtend = h.ToolSchemaExtend
	}
	return out
}
