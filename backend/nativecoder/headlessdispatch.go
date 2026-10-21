package nativecoder

// This file owns the dispatch-level headless guarantee (ci#622 / ci#506).
//
// The per-role native-coder self-verification suite dispatches every role
// through the trusted agent-dispatch surface with the workflow-equivalent
// options `autonomous: true` and, for the headless roles, an
// `approve_confirmations: true` where the dispatcher stands in as the confirming
// user. Such an interaction has no human watching it, so it must never park:
// neither on the pre-start confirmation widget a bot's default_shared_config may
// request, nor on the per-action confirmation gate of the mutating coder tools
// (`coder_bash`/`coder_write`/`coder_edit`/`coder_git`).
//
// A parked headless interaction ends as `state=active finished=False` after its
// timeout and the role silently produces nothing - the `confirmation_parking`
// failure mode (ci#506) the selfcheck reports for the development-lead role,
// where the interaction never reaches the report it is dispatched to write.
//
// The guarantee is deliberately narrow: it only ever applies to a *dispatch*
// that explicitly asked for a headless run, a config that already states its own
// `coder_interaction` mode keeps that value, and a config with no
// `agent_dispatch` block (a hand-made chat, a non-coder bot, a fixed workspace)
// is returned untouched.

// DispatchRunsHeadless reports whether a chat shared config was created by a
// dispatch that asked for a run which must not pause for a user: an
// `agent_dispatch.autonomous: true`, or an `approve_confirmations: true` where
// the dispatcher stands in as the confirming user. A config with no dispatch
// block is never headless.
func DispatchRunsHeadless(config map[string]interface{}) bool {
	dispatch := dispatchMetaFromConfig(config)
	if dispatch == nil {
		return false
	}
	if dispatchAskedAutonomous(config) {
		return true
	}
	return boolishValue(dispatch[AgentDispatchApproveConfirmationsKey], false)
}

// HeadlessDispatchRequiresConfirmation decides the pre-start gate of a dispatched
// interaction (the confirmation widget `createBotInteraction` may create before
// the first bot reply is enqueued). An explicit request field always wins; the
// bot's `default_shared_config.require_confirmation` default is honored for every
// other interaction, but never for a dispatch that asked to run headless: that
// widget cannot be approved by anyone, so creating it only parks the role until
// its timeout (ci#506 confirmation_parking).
func HeadlessDispatchRequiresConfirmation(explicit bool, defaultRequiresConfirmation bool, config map[string]interface{}) bool {
	if explicit {
		return true
	}
	if !defaultRequiresConfirmation {
		return false
	}
	return !DispatchRunsHeadless(config)
}

// WithHeadlessDispatchNoPark returns the shared config of a headless dispatch
// with the per-action confirmation gate lifted: when the dispatch asked for a
// headless run and the caller did not state a `coder_interaction` mode itself,
// the chat states `coder_interaction: autonomous` - the key the coder
// integration reads - so the agent answers its own questions and runs the
// mutating tools without waiting for an approval nobody can give.
//
// A config that is not a headless dispatch, and one that already declares a
// mode, is returned unchanged (the caller's map, so a caller can cheaply keep
// the previous value).
func WithHeadlessDispatchNoPark(config map[string]interface{}) map[string]interface{} {
	if config == nil || !DispatchRunsHeadless(config) {
		return config
	}
	if _, present := config[CoderInteractionConfigKey]; present {
		// An already stated mode always wins, exactly like
		// ApplyCoderAutonomousConfig.
		return config
	}
	merged := make(map[string]interface{}, len(config)+1)
	for key, value := range config {
		merged[key] = value
	}
	merged[CoderInteractionConfigKey] = CoderInteractionAutonomous
	return merged
}
