package main

import (
	"backend/cmd"
	"backend/integrations"
	"backend/runtimecfg"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"syscall"

	ufcli "github.com/urfave/cli/v3"

	goyaml "go.yaml.in/yaml/v3"
)

type openChatConfigBootstrap struct {
	Spec        string
	OverrideEnv bool
}

type openChatConfig struct {
	Env          map[string]interface{}            `json:"env"`
	Integrations map[string]map[string]interface{} `json:"integrations"`
	Bootstrap    *openChatBootstrapConfig          `json:"bootstrap,omitempty"`
	// Anchors is consumed by authoring tools (YAML aliases); it is never
	// used by the runtime and is ignored here.
	Anchors map[string]interface{} `json:"anchors,omitempty"`
}

type openChatBootstrapConfig struct {
	Users    json.RawMessage                  `json:"users,omitempty"`
	Bots     json.RawMessage                  `json:"bots,omitempty"`
	SSH      *openChatSSHBootstrapConfig      `json:"ssh,omitempty"`
	Opencode *openChatOpencodeBootstrapConfig `json:"opencode,omitempty"`
}

type openChatOpencodeBootstrapConfig struct {
	Owner    string          `json:"owner,omitempty"`
	Owners   []string        `json:"owners,omitempty"`
	Projects json.RawMessage `json:"projects,omitempty"`
}

type openChatSSHBootstrapConfig struct {
	Owner        string          `json:"owner,omitempty"`
	Owners       []string        `json:"owners,omitempty"`
	Keys         json.RawMessage `json:"keys,omitempty"`
	Servers      json.RawMessage `json:"servers,omitempty"`
	KeyGrants    json.RawMessage `json:"key_grants,omitempty"`
	ServerGrants json.RawMessage `json:"server_grants,omitempty"`
}

func parseOpenChatConfigBootstrap(args []string) openChatConfigBootstrap {
	cfg := openChatConfigBootstrap{}

	for i := 1; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--config":
			if i+1 < len(args) {
				cfg.Spec = strings.TrimSpace(args[i+1])
				i++
			}
		case strings.HasPrefix(arg, "--config="):
			cfg.Spec = strings.TrimSpace(strings.TrimPrefix(arg, "--config="))
		case arg == "--config-override-env":
			cfg.OverrideEnv = true
		case strings.HasPrefix(arg, "--config-override-env="):
			v := strings.TrimSpace(strings.TrimPrefix(arg, "--config-override-env="))
			cfg.OverrideEnv = !(v == "" || strings.EqualFold(v, "false") || v == "0" || strings.EqualFold(v, "no"))
		}
	}

	if strings.TrimSpace(cfg.Spec) == "" {
		cfg.Spec = strings.TrimSpace(os.Getenv("OPEN_CHAT_CONFIG"))
	}
	if !cfg.OverrideEnv {
		overrideEnv := strings.TrimSpace(os.Getenv("OPEN_CHAT_CONFIG_OVERRIDE_ENV"))
		if strings.EqualFold(overrideEnv, "true") || overrideEnv == "1" || strings.EqualFold(overrideEnv, "yes") {
			cfg.OverrideEnv = true
		}
	}

	if strings.TrimSpace(cfg.Spec) == "" {
		if file := firstExistingPath([]string{".open-chat.json", "open-chat.json", ".open-chat.yaml", "open-chat.yaml", ".open-chat.yml", "open-chat.yml"}); file != "" {
			cfg.Spec = file
		}
	}

	return cfg
}

func firstExistingPath(paths []string) string {
	for _, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		if info, err := os.Stat(trimmed); err == nil && !info.IsDir() {
			return trimmed
		}
	}
	return ""
}

func resolveConfigSource(raw string) ([]byte, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, "", nil
	}
	if strings.HasPrefix(trimmed, "{") {
		return []byte(trimmed), "inline --config JSON", nil
	}
	if strings.ContainsAny(trimmed, "\n\r") {
		// A file path can never span lines, so a spec containing line breaks
		// must be an inline config document (e.g. the whole YAML config passed
		// through the OPEN_CHAT_CONFIG env var).
		return []byte(trimmed), "inline --config YAML", nil
	}
	content, err := os.ReadFile(trimmed)
	if err != nil {
		// Extremely long single-line specs may be a single-line inline
		// document misused as a path; try parsing it as inline YAML/JSON
		// before failing on the unreadable path.
		if isPathTooLongError(err) {
			return []byte(trimmed), "inline --config YAML", nil
		}
		return nil, "", fmt.Errorf("failed reading config path %q: %w", trimmed, err)
	}
	return content, trimmed, nil
}

func isPathTooLongError(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.ENAMETOOLONG
	}
	if unwrapped, ok := err.(*os.PathError); ok {
		return unwrapped.Err == syscall.ENAMETOOLONG
	}
	return strings.Contains(err.Error(), "file name too long")
}

func loadOpenChatConfig(raw []byte, source string) (openChatConfig, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return openChatConfig{}, nil
	}

	if cfg, err := decodeOpenChatConfigJSON(raw, source); err == nil {
		return cfg, nil
	} else if !isOpenChatConfigSyntaxError(err) {
		return openChatConfig{}, err
	}

	// Not JSON: treat the document as YAML (anchors/aliases and multi-line
	// strings included), convert it to the canonical JSON shape and apply the
	// same strict schema as before.
	intermediate := map[string]interface{}{}
	if err := goyaml.Unmarshal(raw, &intermediate); err != nil {
		return openChatConfig{}, fmt.Errorf("invalid open-chat config (%s): not valid JSON or YAML: %w", source, err)
	}
	if err := resolveOpenChatConfigYamlRefs(intermediate); err != nil {
		return openChatConfig{}, fmt.Errorf("invalid open-chat config (%s): %w", source, err)
	}
	converted, err := json.Marshal(intermediate)
	if err != nil {
		return openChatConfig{}, fmt.Errorf("invalid open-chat config (%s): %w", source, err)
	}
	return decodeOpenChatConfigJSON(converted, source)
}

// openChatYamlRefPrefix marks a YAML config value as a reference into the
// trailing `anchors` block: "$anchors.<name>". This keeps long artifacts
// (kubeconfigs, SSH private keys, ...) out of the functional part of the
// config — standard YAML aliases cannot be forward-referenced, so the loader
// resolves these markers itself after parsing.
const openChatYamlRefPrefix = "$anchors."

// resolveOpenChatConfigYamlRefs removes the trailing `anchors` block from the
// decoded document and substitutes every "$anchors.<name>" string value with
// the matching anchored value. Nested references inside the anchors block
// itself are resolved first (bounded passes).
func resolveOpenChatConfigYamlRefs(doc map[string]interface{}) error {
	anchorsRaw, ok := doc["anchors"]
	hasRefs, err := docHasRefMarker(doc)
	if err != nil {
		return err
	}
	if !ok || anchorsRaw == nil {
		if hasRefs {
			return fmt.Errorf("a $anchors. reference was used but no top-level `anchors` block exists")
		}
		return nil
	}
	if !hasRefs {
		// No "$anchors." markers anywhere: keep the top-level `anchors` block
		// intact (carried through for authoring tools, e.g. YAML-style alias
		// documents) and let the schema handle the rest.
		return nil
	}
	anchors, ok := anchorsRaw.(map[string]interface{})
	if !ok {
		return fmt.Errorf("top-level `anchors` must be a mapping of name -> value")
	}
	delete(doc, "anchors")

	resolve := func(value interface{}) (interface{}, bool, error) {
		str, isStr := value.(string)
		if !isStr || !strings.HasPrefix(str, openChatYamlRefPrefix) {
			return nil, false, nil
		}
		name := strings.TrimSpace(strings.TrimPrefix(str, openChatYamlRefPrefix))
		resolved, ok := anchors[name]
		if !ok {
			return nil, false, fmt.Errorf("reference %q points to an unknown anchor (known: %v)", str, anchorNames(anchors))
		}
		return resolved, true, nil
	}

	// Anchors may reference other anchors; resolve with a bounded number of
	// passes so self/cyclic references fail instead of looping forever.
	lastErr := error(nil)
	for pass := 0; pass <= len(anchors); pass++ {
		changed := false
		for name, value := range anchors {
			resolved, isRef, err := resolve(value)
			if err != nil {
				lastErr = err
				continue
			}
			if isRef {
				anchors[name] = resolved
				changed = true
			}
		}
		if lastErr != nil {
			return lastErr
		}
		if !changed {
			break
		}
		if pass == len(anchors) {
			return fmt.Errorf("cyclic reference chain inside top-level `anchors`")
		}
	}

	if err := resolveRefsInValue(doc, resolve); err != nil {
		return err
	}
	// Multi-hop references could leave an intermediate ref in place if it was
	// substituted before its target anchor finished resolving; verify none are
	// left over at the end.
	found, err := docHasRefMarker(doc)
	if err != nil {
		return err
	}
	if found {
		return fmt.Errorf("a $anchors. reference was not resolved")
	}
	return nil
}

// docHasRefMarker reports whether any string value in the document carries an
// "$anchors." reference marker.
func docHasRefMarker(value interface{}) (bool, error) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for _, item := range typed {
			found, err := docHasRefMarker(item)
			if err != nil || found {
				return found, err
			}
		}
	case []interface{}:
		for _, item := range typed {
			found, err := docHasRefMarker(item)
			if err != nil || found {
				return found, err
			}
		}
	case string:
		if strings.HasPrefix(typed, openChatYamlRefPrefix) {
			return true, nil
		}
	}
	return false, nil
}

func resolveRefsInValue(value interface{}, resolve func(interface{}) (interface{}, bool, error)) error {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, item := range typed {
			if resolved, isRef, err := resolve(item); err != nil {
				return err
			} else if isRef {
				typed[key] = resolved
			} else if err := resolveRefsInValue(item, resolve); err != nil {
				return err
			}
		}
	case []interface{}:
		for idx, item := range typed {
			if resolved, isRef, err := resolve(item); err != nil {
				return err
			} else if isRef {
				typed[idx] = resolved
			} else if err := resolveRefsInValue(item, resolve); err != nil {
				return err
			}
		}
	}
	return nil
}

func anchorNames(anchors map[string]interface{}) []string {
	names := make([]string, 0, len(anchors))
	for name := range anchors {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func decodeOpenChatConfigJSON(raw []byte, source string) (openChatConfig, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	cfg := openChatConfig{}
	if err := decoder.Decode(&cfg); err != nil {
		return openChatConfig{}, fmt.Errorf("invalid open-chat config (%s): %w", source, err)
	}

	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return openChatConfig{}, fmt.Errorf("invalid open-chat config (%s): unexpected trailing JSON", source)
		}
		return openChatConfig{}, fmt.Errorf("invalid open-chat config (%s): %w", source, err)
	}

	return cfg, nil
}

// isOpenChatConfigSyntaxError reports whether a decode error is a pure
// document-syntax problem (justifying the YAML fallback) rather than a real
// schema violation that must fail hard.
func isOpenChatConfigSyntaxError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, marker := range []string{
		"invalid character",
		"unexpected end of JSON input",
		"unterminated",
		"looking for beginning of value",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func normalizeTokenForEnv(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	upper := strings.ToUpper(trimmed)
	b := strings.Builder{}
	lastUnderscore := false
	for _, r := range upper {
		isAlpha := r >= 'A' && r <= 'Z'
		isDigit := r >= '0' && r <= '9'
		if isAlpha || isDigit {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteRune('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func deriveIntegrationEnvKey(integrationName, key string) string {
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(key)), "OCI_") {
		return strings.ToUpper(strings.TrimSpace(key))
	}
	integrationPart := normalizeTokenForEnv(integrationName)
	keyPart := normalizeTokenForEnv(key)
	if integrationPart == "" || keyPart == "" {
		return ""
	}
	return "OCI_" + integrationPart + "_" + keyPart
}

func flattenConfigEnv(cfg openChatConfig) (map[string]string, error) {
	integrations.EnsureLoaded()

	updates := map[string]string{}
	for key, value := range cfg.Env {
		normalized := strings.TrimSpace(key)
		if normalized == "" {
			continue
		}
		updates[normalized] = fmt.Sprintf("%v", value)
	}

	runtimeEnvByIntegration := map[string]map[string]struct{}{}
	for _, decl := range integrations.RuntimeEnvDeclarations() {
		integrationName := strings.ToLower(strings.TrimSpace(decl.IntegrationName))
		if _, ok := runtimeEnvByIntegration[integrationName]; !ok {
			runtimeEnvByIntegration[integrationName] = map[string]struct{}{}
		}
		runtimeEnvByIntegration[integrationName][strings.ToUpper(strings.TrimSpace(decl.Key))] = struct{}{}
	}

	aliasByIntegration := map[string]map[string]string{}
	for _, decl := range integrations.RuntimeConfigAliasDeclarations() {
		integrationName := strings.ToLower(strings.TrimSpace(decl.IntegrationName))
		if _, ok := aliasByIntegration[integrationName]; !ok {
			aliasByIntegration[integrationName] = map[string]string{}
		}
		aliasByIntegration[integrationName][strings.ToLower(strings.TrimSpace(decl.JSONKey))] = strings.ToUpper(strings.TrimSpace(decl.EnvKey))
	}

	for integrationNameRaw, values := range cfg.Integrations {
		integrationName := strings.ToLower(strings.TrimSpace(integrationNameRaw))
		if integrationName == "" {
			continue
		}
		declaredEnv, ok := runtimeEnvByIntegration[integrationName]
		if !ok {
			return nil, fmt.Errorf("config integrations.%s: integration has no declared runtime env vars or is unknown", integrationNameRaw)
		}

		aliases := aliasByIntegration[integrationName]
		for key, value := range values {
			trimmedKey := strings.TrimSpace(key)
			if trimmedKey == "" {
				continue
			}

			directEnvKey := strings.ToUpper(trimmedKey)
			envKey := ""
			if _, exists := declaredEnv[directEnvKey]; exists {
				envKey = directEnvKey
			} else if alias, exists := aliases[strings.ToLower(trimmedKey)]; exists {
				envKey = alias
			} else {
				derived := deriveIntegrationEnvKey(integrationName, trimmedKey)
				if _, exists := declaredEnv[derived]; exists {
					envKey = derived
				}
			}

			if envKey == "" {
				return nil, fmt.Errorf("config integrations.%s.%s does not map to a declared runtime env var", integrationNameRaw, key)
			}
			updates[envKey] = stringifyConfigValue(value)
		}
	}

	return updates, nil
}

// stringifyConfigValue renders a config value for an env var. Scalar values are
// rendered like before; objects/arrays are JSON-encoded so integrations can
// declare list/structured runtime env vars (eg bootstrap specs) via their
// open-chat.json integrations section.
func stringifyConfigValue(value interface{}) string {
	switch value.(type) {
	case string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, nil:
		return fmt.Sprintf("%v", value)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprintf("%v", value)
		}
		return string(encoded)
	}
}

func normalizeOwners(raw []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(raw))
	for _, owner := range raw {
		trimmed := strings.TrimSpace(owner)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func toOpenChatBootstrapRuntime(cfg openChatConfig) runtimecfg.OpenChatBootstrap {
	out := runtimecfg.OpenChatBootstrap{}
	if cfg.Bootstrap == nil {
		return out
	}

	if len(bytes.TrimSpace(cfg.Bootstrap.Users)) > 0 {
		out.UserSpecs = append(out.UserSpecs, string(bytes.TrimSpace(cfg.Bootstrap.Users)))
	}

	if len(bytes.TrimSpace(cfg.Bootstrap.Bots)) > 0 {
		out.BotSpecs = append(out.BotSpecs, string(bytes.TrimSpace(cfg.Bootstrap.Bots)))
	}

	if cfg.Bootstrap.SSH != nil {
		owners := append([]string{}, cfg.Bootstrap.SSH.Owners...)
		if strings.TrimSpace(cfg.Bootstrap.SSH.Owner) != "" {
			owners = append(owners, cfg.Bootstrap.SSH.Owner)
		}
		out.SSHDefaultOwners = normalizeOwners(owners)

		if len(bytes.TrimSpace(cfg.Bootstrap.SSH.Keys)) > 0 {
			out.SSHKeySpecs = append(out.SSHKeySpecs, string(bytes.TrimSpace(cfg.Bootstrap.SSH.Keys)))
		}
		if len(bytes.TrimSpace(cfg.Bootstrap.SSH.Servers)) > 0 {
			out.SSHServerSpecs = append(out.SSHServerSpecs, string(bytes.TrimSpace(cfg.Bootstrap.SSH.Servers)))
		}
		if len(bytes.TrimSpace(cfg.Bootstrap.SSH.KeyGrants)) > 0 {
			out.SSHKeyGrantSpecs = append(out.SSHKeyGrantSpecs, string(bytes.TrimSpace(cfg.Bootstrap.SSH.KeyGrants)))
		}
		if len(bytes.TrimSpace(cfg.Bootstrap.SSH.ServerGrants)) > 0 {
			out.SSHServerGrantSpecs = append(out.SSHServerGrantSpecs, string(bytes.TrimSpace(cfg.Bootstrap.SSH.ServerGrants)))
		}
	}

	if cfg.Bootstrap.Opencode != nil {
		owners := append([]string{}, cfg.Bootstrap.Opencode.Owners...)
		if strings.TrimSpace(cfg.Bootstrap.Opencode.Owner) != "" {
			owners = append(owners, cfg.Bootstrap.Opencode.Owner)
		}
		out.OpencodeDefaultOwners = normalizeOwners(owners)

		if len(bytes.TrimSpace(cfg.Bootstrap.Opencode.Projects)) > 0 {
			out.OpencodeProjectSpecs = append(out.OpencodeProjectSpecs, string(bytes.TrimSpace(cfg.Bootstrap.Opencode.Projects)))
		}
	}

	return out
}

func applyEnvUpdates(updates map[string]string, overrideEnv bool) error {
	for key, value := range updates {
		normalized := strings.TrimSpace(key)
		if normalized == "" {
			continue
		}
		if !overrideEnv {
			if _, exists := os.LookupEnv(normalized); exists {
				continue
			}
		}
		if err := os.Setenv(normalized, value); err != nil {
			return fmt.Errorf("failed setting env %q from config: %w", normalized, err)
		}
	}
	return nil
}

func applyOpenChatConfigBootstrap(args []string) error {
	bootstrap := parseOpenChatConfigBootstrap(args)
	raw, source, err := resolveConfigSource(bootstrap.Spec)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}

	cfg, err := loadOpenChatConfig(raw, source)
	if err != nil {
		return err
	}
	updates, err := flattenConfigEnv(cfg)
	if err != nil {
		return err
	}
	if err := applyEnvUpdates(updates, bootstrap.OverrideEnv); err != nil {
		return err
	}
	runtimecfg.SetOpenChatBootstrap(toOpenChatBootstrapRuntime(cfg))

	return nil
}

//	@title							Open Chat API
//	@version						1.0
//	@description					API for Open Chat application
//
//	@tag.name						chats
//	@tag.description				Chats hold a collection of messages and files or meta-data, they are central to how open-chat works and are used to hold information for interactions and integratins
//
// 	@tag.name						messages
//	@tag.description				Messages are the atomic data point of open-chat, they may hold any sort of supported information, they may also reference information in external locations. Messages are collected in a chat. Messages can have only one creator/sender but are received by all chat members.
//
//	@tag.name						users
//	@tag.description				Everything user management related, users are also used to abstract access permissions. Chats have users as participants, only users share each others contact may create a shared chat.
//
//	@tag.name					bots
//	@tag.description				Owner-scoped automated bot management and interaction creation.
//
//	@securityDefinitions.apikey	SessionAuth
//	@in								cookie
//	@name							session_id
//	@description					Session cookie obtained from login endpoint

func main() {
	if len(os.Args) == 1 {
		os.Args = append(os.Args, "--help")
	}

	if err := applyOpenChatConfigBootstrap(os.Args); err != nil {
		log.Fatal(err)
	}

	rootCmd := &ufcli.Command{
		Name:  "open-chat",
		Usage: "Open Chat command line interface",
		Flags: []ufcli.Flag{
			&ufcli.StringFlag{
				Name:    "config",
				Usage:   "Inline open-chat JSON config object or filesystem path to open-chat config JSON",
				Sources: ufcli.EnvVars("OPEN_CHAT_CONFIG"),
			},
			&ufcli.BoolFlag{
				Name:    "config-override-env",
				Usage:   "Allow open-chat config values to overwrite already-set environment variables",
				Value:   false,
				Sources: ufcli.EnvVars("OPEN_CHAT_CONFIG_OVERRIDE_ENV"),
			},
		},
		Commands: []*ufcli.Command{
			cmd.RunCli(),
			cmd.ServerCli(),
			cmd.WorkerCli(),
			cmd.ClientCli(),
		},
	}

	if err := rootCmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
