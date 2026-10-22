package nativecoder

import "strings"

// ci#819 part B: the general chat-start pre-call hook for init-requiring tools.
//
// A tool whose GetRequiresInit() is true cannot run until its per-chat init is
// stored (tool_init.<tool>). Today the *agent* has to notice that and call the
// initialiser itself - eg coder_select_workspace before the first coder_bash -
// which burns a turn on setup and hard-fails when the model forgets.
//
// The hook inverts it: at chat start the server resolves the init for every
// configured init-requiring tool that has a declarable default and seeds
// tool_init, so the tool is usable from the first call. Anything not
// resolvable is left untouched for the agent, never seeded empty.

// InitResolver declares how a chat-start hook satisfies one init-requiring tool.
type InitResolver struct {
	// Tool is the tool name, eg coderToolInitSelectWorkspace.
	Tool string
	// InitKey is the tool_init key to seed; defaults to Tool.
	InitKey string
	// Resolve derives the init payload. A nil payload with a nil error means
	// "leave it to the agent".
	Resolve func(PreCallInitContext) (map[string]interface{}, error)
}

// PreCallInitContext is everything a chat-start hook knows. Init and Config are
// read-only inputs; the hook returns the additions instead of mutating them.
type PreCallInitContext struct {
	Tools  []string
	Init   map[string]map[string]interface{}
	Tags   []string
	Config map[string]interface{}
}

// PreCallInitResult reports what the hook seeded and what it deliberately left
// to the agent.
type PreCallInitResult struct {
	// Seeded maps a tool_init key to the payload the caller must persist.
	Seeded map[string]map[string]interface{}
	// Applied lists the tools satisfied without an agent turn.
	Applied []string
	// Skipped lists the tools left to the agent, with the reason.
	Skipped map[string]string
}

// InitKeyFor returns the tool_init key the resolver seeds.
func (r InitResolver) InitKeyFor() string {
	if key := strings.TrimSpace(r.InitKey); key != "" {
		return key
	}
	return strings.TrimSpace(r.Tool)
}

// Configured reports whether the chat exposes the tool at all.
func (c PreCallInitContext) Configured(tool string) bool {
	for _, name := range c.Tools {
		if strings.TrimSpace(name) == tool {
			return true
		}
	}
	return false
}

// HasInit reports whether tool_init already carries a payload for the key, so a
// caller-provided selection always wins over the hook's default.
func (c PreCallInitContext) HasInit(key string) bool {
	for existing, payload := range c.Init {
		if strings.TrimSpace(existing) == key && len(payload) > 0 {
			return true
		}
	}
	return false
}

// RunPreCallInit applies the resolvers to a chat-start context. It is pure: the
// caller persists PreCallInitResult.Seeded into tool_init. A resolver error is
// reported as a skip, never as a hard failure - a chat must still start when a
// default cannot be derived.
func RunPreCallInit(ctx PreCallInitContext, resolvers []InitResolver) PreCallInitResult {
	result := PreCallInitResult{
		Seeded:  map[string]map[string]interface{}{},
		Skipped: map[string]string{},
	}
	for _, resolver := range resolvers {
		tool := strings.TrimSpace(resolver.Tool)
		if tool == "" {
			continue
		}
		if !ctx.Configured(tool) {
			result.Skipped[tool] = "tool not configured on this chat"
			continue
		}
		key := resolver.InitKeyFor()
		if ctx.HasInit(key) {
			result.Skipped[tool] = "tool_init already set for " + key
			continue
		}
		if resolver.Resolve == nil {
			result.Skipped[tool] = "no resolver"
			continue
		}
		payload, err := resolver.Resolve(ctx)
		if err != nil {
			result.Skipped[tool] = err.Error()
			continue
		}
		if len(payload) == 0 {
			result.Skipped[tool] = "no default resolvable"
			continue
		}
		result.Seeded[key] = payload
		result.Applied = append(result.Applied, tool)
	}
	return result
}

// DefaultPreCallInitResolvers returns the built-in chat-start hooks. It is the
// single place a new init-requiring tool registers its default.
func DefaultPreCallInitResolvers() []InitResolver {
	return []InitResolver{WorkspaceSelectionResolver()}
}

// WithPreCallInit applies the chat-start hooks to a dispatch config and returns
// the config the interaction should be created with: tool_init seeded for every
// init-requiring tool with a derivable default. It is the integration half of
// the hook - the caller passes the config it is about to store plus the dispatch
// tags, and gets back a config with the initialisers already satisfied.
//
// It is conservative and side-effect free: a config that already pins the init
// keeps it, a config whose tool is not exposed keeps it, a config with nothing
// derivable keeps it, and nothing is mutated in place (the returned map is the
// caller's map when nothing changed).
func WithPreCallInit(config map[string]interface{}, tools, tags []string, resolvers []InitResolver) map[string]interface{} {
	ctx := PreCallInitContext{
		Tools:  tools,
		Tags:   tags,
		Config: config,
		Init:   toolInitFromConfig(config),
	}
	result := RunPreCallInit(ctx, resolvers)
	if len(result.Seeded) == 0 {
		return config
	}

	existing, _ := config["tool_init"].(map[string]interface{})
	merged := make(map[string]interface{}, len(existing)+len(result.Seeded))
	for key, value := range existing {
		merged[key] = value
	}
	for tool, payload := range result.Seeded {
		merged[tool] = payload
	}

	out := make(map[string]interface{}, len(config)+1)
	for key, value := range config {
		out[key] = value
	}
	out["tool_init"] = merged
	return out
}

// toolInitFromConfig reads the per-chat tool init out of a dispatch config,
// tolerating both the typed and the JSON-decoded shape a config arrives in.
func toolInitFromConfig(config map[string]interface{}) map[string]map[string]interface{} {
	raw, ok := config["tool_init"].(map[string]interface{})
	if !ok || len(raw) == 0 {
		return nil
	}
	init := make(map[string]map[string]interface{}, len(raw))
	for tool, entry := range raw {
		switch value := entry.(type) {
		case map[string]interface{}:
			init[tool] = value
		}
	}
	return init
}

// WorkspaceSelectionResolver seeds the coder workspace from the dispatch tags
// (`repo:owner/name`, `branch:<ref>`) or an explicit `git_repo` config, so the
// agent never spends a turn on coder_select_workspace.
//
// The payload is the same input set the auto-sandbox path already binds
// (AutoSandboxGitRepoInput / Branch / Flag), so a seeded chat is provisioned
// exactly like one where the agent made the call itself.
func WorkspaceSelectionResolver() InitResolver {
	return InitResolver{
		Tool:    coderToolInitSelectWorkspace,
		InitKey: coderToolInitSelectWorkspace,
		Resolve: func(ctx PreCallInitContext) (map[string]interface{}, error) {
			payload := map[string]interface{}{}
			if repo := repoFromConfig(ctx.Config); repo != "" {
				payload[AutoSandboxGitRepoInput] = repo
			} else if repo := RepoValueFromTags(ctx.Tags); repo != "" {
				payload[AutoSandboxGitRepoInput] = repo
			}
			if branch := BranchValueFromTags(ctx.Tags); branch != "" {
				payload[AutoSandboxGitBranchInput] = branch
			}
			if len(payload) == 0 {
				return nil, nil
			}
			payload[AutoSandboxFlagInput] = true
			return payload, nil
		},
	}
}
