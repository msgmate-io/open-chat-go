package main

import (
	"backend/cmd"
	"backend/integrations"
	"backend/logging"
	"backend/runtimecfg"
	"backend/secretstore"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	ufcli "github.com/urfave/cli/v3"

	extiface "github.com/msgmate-io/go-integration-interface/integrationinterface"

	goyaml "go.yaml.in/yaml/v3"
	"golang.org/x/term"
)

type openChatConfigBootstrap struct {
	Spec        string
	OverrideEnv bool
}

type openChatConfig struct {
	Env          map[string]interface{}            `json:"env"`
	Integrations map[string]map[string]interface{} `json:"integrations"`
	Bootstrap    *openChatBootstrapConfig          `json:"bootstrap,omitempty"`
	// Secrets holds sealed values referenced from env/integrations via a
	// `$tvault` marker (or a `tvault://<ref>` string). The block key is the
	// block ref.
	Secrets map[string]secretstore.Block `json:"secrets,omitempty"`
	// Anchors is consumed by authoring tools (YAML aliases); it is never
	// used by the runtime and is ignored here.
	Anchors map[string]interface{} `json:"anchors,omitempty"`
}

type openChatBootstrapConfig struct {
	Users         json.RawMessage                       `json:"users,omitempty"`
	Bots          json.RawMessage                       `json:"bots,omitempty"`
	SSH           *openChatSSHBootstrapConfig           `json:"ssh,omitempty"`
	Opencode      *openChatOpencodeBootstrapConfig      `json:"opencode,omitempty"`
	DockerSandbox *openChatDockerSandboxBootstrapConfig `json:"docker_sandbox,omitempty"`
	Git           *openChatGitBootstrapConfig           `json:"git,omitempty"`
	MCP           *openChatMCPBootstrapConfig           `json:"mcp,omitempty"`
}

type openChatDockerSandboxBootstrapConfig struct {
	Owner     openChatOwnerList `json:"owner,omitempty"`
	Owners    []string          `json:"owners,omitempty"`
	Sandboxes json.RawMessage   `json:"sandboxes,omitempty"`
}

type openChatMCPBootstrapConfig struct {
	Owner   openChatOwnerList `json:"owner,omitempty"`
	Owners  []string          `json:"owners,omitempty"`
	Servers json.RawMessage   `json:"servers,omitempty"`
}

type openChatGitBootstrapConfig struct {
	Owner           openChatOwnerList `json:"owner,omitempty"`
	Owners          []string          `json:"owners,omitempty"`
	Tokens          json.RawMessage   `json:"tokens,omitempty"`
	Repositories    json.RawMessage   `json:"repositories,omitempty"`
	Workspaces      json.RawMessage   `json:"workspaces,omitempty"`
	WorkspaceGrants json.RawMessage   `json:"workspace_grants,omitempty"`
	Triggers        json.RawMessage   `json:"triggers,omitempty"`
}

// openChatOwnerList accepts either a single owner string (`owner: admin`) or a
// list of owners (`owner: [admin]`) so the documented git bootstrap examples
// keep working.
type openChatOwnerList []string

func (o *openChatOwnerList) UnmarshalJSON(raw []byte) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	if trimmed[0] == '[' {
		var list []string
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return err
		}
		*o = list
		return nil
	}
	var single string
	if err := json.Unmarshal(trimmed, &single); err != nil {
		return err
	}
	*o = []string{single}
	return nil
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
		// The strict JSON fast-path does not resolve "$anchors." references;
		// only the YAML path below does. JSON is a subset of YAML and tooling
		// (e.g. the CI config-delta publisher) emits minified JSON, so a JSON
		// config must resolve references exactly like its YAML equivalent:
		// fall through to the resolution path when the document still carries
		// a "$anchors." or "$file" marker instead of returning the raw
		// reference strings.
		if !bytes.Contains(raw, []byte(openChatYamlRefPrefix)) && !bytes.Contains(raw, []byte(openChatFileRefPrefix)) {
			return cfg, nil
		}
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
	if err := resolveOpenChatConfigLinkedFiles(intermediate, openChatConfigBaseDir(source)); err != nil {
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
		if !isStr || !strings.Contains(str, openChatYamlRefPrefix) {
			return nil, false, nil
		}
		// A value may contain one or more "$anchors.<name>" references, either
		// as the entire value (returned as-is, allowing non-string anchors) or
		// embedded inside a larger string (e.g. multi-line setup commands).
		out := str
		for {
			idx := strings.Index(out, openChatYamlRefPrefix)
			if idx < 0 {
				break
			}
			rest := out[idx+len(openChatYamlRefPrefix):]
			end := 0
			for end < len(rest) && isAnchorNameRune(rest[end]) {
				end++
			}
			name := rest[:end]
			resolved, ok := anchors[name]
			if !ok {
				return nil, false, fmt.Errorf("reference %q points to an unknown anchor (known: %v)", openChatYamlRefPrefix+name, anchorNames(anchors))
			}
			if idx == 0 && strings.TrimSpace(rest[end:]) == "" {
				return resolved, true, nil
			}
			resolvedStr, ok := resolved.(string)
			if !ok {
				return nil, false, fmt.Errorf("anchor %q must be a string to embed it in a larger value", name)
			}
			out = out[:idx] + resolvedStr + rest[end:]
		}
		return out, true, nil
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
		if strings.Contains(typed, openChatYamlRefPrefix) {
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

// isAnchorNameRune reports whether r may appear in an anchor name.
func isAnchorNameRune(r byte) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '_' || r == '-' || r == '.':
		return true
	default:
		return false
	}
}

// openChatFileRefPrefix marks a config value as a link to a text file whose
// content is read from disk: "$file.<path>". The path is resolved relative to
// the directory holding the config file (the working directory for an inline
// config document), so authors keep long, human-edited artifacts (markdown
// prompts, kubeconfigs, SSH keys) as real files next to their config instead of
// inlining them. The marker must be the entire value; a link that is missing,
// unreadable, binary or pointing outside the config directory fails the boot
// instead of reaching the runtime as a literal marker.
const openChatFileRefPrefix = "$file."

// resolveOpenChatConfigLinkedFiles substitutes every "$file.<path>" value in the
// decoded document with the content of the referenced text file. baseDir is the
// directory the relative paths resolve against.
func resolveOpenChatConfigLinkedFiles(doc map[string]interface{}, baseDir string) error {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = "."
	}
	resolve := func(value interface{}) (interface{}, bool, error) {
		str, isStr := value.(string)
		if !isStr {
			return nil, false, nil
		}
		idx := strings.Index(str, openChatFileRefPrefix)
		if idx < 0 {
			return nil, false, nil
		}
		if idx != 0 {
			return nil, false, fmt.Errorf("%s links must be the entire config value (found one inside %q)", openChatFileRefPrefix, str)
		}
		content, err := readOpenChatLinkedFile(strings.TrimSpace(strings.TrimPrefix(str, openChatFileRefPrefix)), baseDir)
		if err != nil {
			return nil, false, err
		}
		return content, true, nil
	}
	return resolveRefsInValue(doc, resolve)
}

// readOpenChatLinkedFile reads the text file referenced by a "$file." link,
// enforcing that it stays inside baseDir and really is a text file.
func readOpenChatLinkedFile(relPath, baseDir string) (string, error) {
	if relPath == "" {
		return "", fmt.Errorf("%s link is missing a file path", openChatFileRefPrefix)
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("%s%s must be a path relative to the config directory", openChatFileRefPrefix, relPath)
	}
	root := filepath.Clean(baseDir)
	full := filepath.Join(root, filepath.FromSlash(relPath))
	inside, err := filepath.Rel(root, full)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s%s points outside the config directory", openChatFileRefPrefix, relPath)
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", fmt.Errorf("%s%s could not be read: %w", openChatFileRefPrefix, relPath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s%s is a directory, not a text file", openChatFileRefPrefix, relPath)
	}
	content, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("%s%s could not be read: %w", openChatFileRefPrefix, relPath, err)
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return "", fmt.Errorf("%s%s is not a text file", openChatFileRefPrefix, relPath)
	}
	return string(content), nil
}

// openChatConfigBaseDir returns the directory "$file." links resolve against:
// the directory of the config file, or the working directory for inline config
// documents (which have no file to be relative to).
func openChatConfigBaseDir(source string) string {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" || strings.HasPrefix(trimmed, "inline") {
		return "."
	}
	return filepath.Dir(trimmed)
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

func flattenConfigEnv(cfg openChatConfig, secretValues map[string]string) (map[string]string, error) {
	integrations.EnsureLoaded()

	updates := map[string]string{}
	for key, value := range cfg.Env {
		normalized := strings.TrimSpace(key)
		if normalized == "" {
			continue
		}
		resolved, err := resolveEnvSecretValue(value, secretValues)
		if err != nil {
			return nil, fmt.Errorf("config env.%s: %w", key, err)
		}
		updates[normalized] = resolved
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
			resolved, err := resolveIntegrationSecretValue(value, secretValues)
			if err != nil {
				return nil, fmt.Errorf("config integrations.%s.%s: %w", integrationNameRaw, key, err)
			}
			updates[envKey] = resolved
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

// tvaultRefKey is the single-key marker object that references a sealed block
// from a config value, e.g. `OPENAI_API_KEY: { "$tvault": "<ref>" }`.
const tvaultRefKey = "$tvault"

// tvaultRefScheme is the string fallback form, e.g. `tvault://<ref>`.
const tvaultRefScheme = "tvault://"

// extractSecretRef reports whether a config value is a `$tvault` reference and
// returns the referenced block ref.
func extractSecretRef(value interface{}) (string, bool) {
	switch typed := value.(type) {
	case map[string]interface{}:
		if len(typed) != 1 {
			return "", false
		}
		ref, ok := typed[tvaultRefKey]
		if !ok {
			return "", false
		}
		text, ok := ref.(string)
		if !ok {
			return "", false
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return "", false
		}
		return text, true
	case string:
		if strings.HasPrefix(typed, tvaultRefScheme) {
			ref := strings.TrimSpace(strings.TrimPrefix(typed, tvaultRefScheme))
			if ref != "" {
				return ref, true
			}
		}
	}
	return "", false
}

func lookupSecretValue(ref string, secretValues map[string]string) (string, error) {
	plaintext, ok := secretValues[ref]
	if !ok {
		return "", fmt.Errorf("$tvault reference %q has no matching entry under `secrets`", ref)
	}
	return plaintext, nil
}

// resolveEnvSecretValue substitutes a `$tvault` reference for env values,
// preserving the historical fmt rendering for non-reference values.
func resolveEnvSecretValue(value interface{}, secretValues map[string]string) (string, error) {
	if ref, ok := extractSecretRef(value); ok {
		return lookupSecretValue(ref, secretValues)
	}
	return fmt.Sprintf("%v", value), nil
}

// resolveIntegrationSecretValue substitutes a `$tvault` reference for
// integration values, preserving the historical JSON rendering otherwise.
func resolveIntegrationSecretValue(value interface{}, secretValues map[string]string) (string, error) {
	if ref, ok := extractSecretRef(value); ok {
		return lookupSecretValue(ref, secretValues)
	}
	return stringifyConfigValue(value), nil
}

// resolveConfigSecretValues decrypts the config document's `secrets:` blocks
// using the boot password. It returns nil when the document has no blocks.
func resolveConfigSecretValues(cfg openChatConfig, args []string) (map[string]string, error) {
	if len(cfg.Secrets) == 0 {
		return nil, nil
	}
	password := ""
	if secretstore.ProviderRequiresPassword(secretstore.Active()) {
		resolved, err := resolveBootSecretPassword(args)
		if err != nil {
			return nil, err
		}
		password = resolved
	}
	return secretstore.ResolveBlocks(context.Background(), cfg.Secrets, password)
}

// resolveBootSecretPassword resolves the password used to unseal config blocks.
// Source order: --secret-password flag, OPEN_CHAT_SECRET_PASSWORD,
// --secret-password-file / OPEN_CHAT_SECRET_PASSWORD_FILE, then an interactive
// no-echo TTY prompt. It fails closed when a password is required but stdin is
// not a terminal.
func resolveBootSecretPassword(args []string) (string, error) {
	var flagPassword, flagPasswordFile string
	for i := 1; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--secret-password":
			if i+1 < len(args) {
				flagPassword = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--secret-password="):
			flagPassword = strings.TrimPrefix(arg, "--secret-password=")
		case arg == "--secret-password-file":
			if i+1 < len(args) {
				flagPasswordFile = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--secret-password-file="):
			flagPasswordFile = strings.TrimPrefix(arg, "--secret-password-file=")
		}
	}

	if password := strings.TrimSpace(flagPassword); password != "" {
		return password, nil
	}
	if password := strings.TrimSpace(os.Getenv("OPEN_CHAT_SECRET_PASSWORD")); password != "" {
		return password, nil
	}

	passwordFile := strings.TrimSpace(flagPasswordFile)
	if passwordFile == "" {
		passwordFile = strings.TrimSpace(os.Getenv("OPEN_CHAT_SECRET_PASSWORD_FILE"))
	}
	if passwordFile != "" {
		raw, err := os.ReadFile(passwordFile)
		if err != nil {
			// Do not include the file path in the error: it is derived from a
			// secret-named input and the returned error is logged, which trips
			// CodeQL's go/clear-text-logging. The wrapped error keeps the cause.
			return "", fmt.Errorf("failed reading secret password file: %w", err)
		}
		password := strings.TrimRight(string(raw), "\r\n")
		if strings.TrimSpace(password) == "" {
			return "", errors.New("secret password file is empty")
		}
		return password, nil
	}

	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "Secret password: ")
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("failed reading secret password: %w", err)
		}
		password := strings.TrimRight(string(raw), "\r\n")
		if strings.TrimSpace(password) == "" {
			return "", errors.New("no secret password was entered")
		}
		return password, nil
	}

	return "", errors.New("config defines secret blocks but no password was provided; set --secret-password, OPEN_CHAT_SECRET_PASSWORD or OPEN_CHAT_SECRET_PASSWORD_FILE")
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

	if cfg.Bootstrap.DockerSandbox != nil {
		owners := append([]string{}, cfg.Bootstrap.DockerSandbox.Owners...)
		owners = append(owners, cfg.Bootstrap.DockerSandbox.Owner...)
		out.DockerSandboxDefaultOwners = normalizeOwners(owners)

		if len(bytes.TrimSpace(cfg.Bootstrap.DockerSandbox.Sandboxes)) > 0 {
			out.DockerSandboxSpecs = append(out.DockerSandboxSpecs, string(bytes.TrimSpace(cfg.Bootstrap.DockerSandbox.Sandboxes)))
		}
	}

	if cfg.Bootstrap.Git != nil {
		owners := append([]string{}, cfg.Bootstrap.Git.Owners...)
		owners = append(owners, cfg.Bootstrap.Git.Owner...)
		out.GitDefaultOwners = normalizeOwners(owners)
		if len(bytes.TrimSpace(cfg.Bootstrap.Git.Tokens)) > 0 {
			out.GitTokenSpecs = append(out.GitTokenSpecs, string(bytes.TrimSpace(cfg.Bootstrap.Git.Tokens)))
		}
		if len(bytes.TrimSpace(cfg.Bootstrap.Git.Repositories)) > 0 {
			out.GitRepositorySpecs = append(out.GitRepositorySpecs, string(bytes.TrimSpace(cfg.Bootstrap.Git.Repositories)))
		}
		if len(bytes.TrimSpace(cfg.Bootstrap.Git.Workspaces)) > 0 {
			out.GitWorkspaceSpecs = append(out.GitWorkspaceSpecs, string(bytes.TrimSpace(cfg.Bootstrap.Git.Workspaces)))
		}
		if len(bytes.TrimSpace(cfg.Bootstrap.Git.WorkspaceGrants)) > 0 {
			out.GitWorkspaceGrantSpecs = append(out.GitWorkspaceGrantSpecs, string(bytes.TrimSpace(cfg.Bootstrap.Git.WorkspaceGrants)))
		}
		if len(bytes.TrimSpace(cfg.Bootstrap.Git.Triggers)) > 0 {
			out.GitTriggerSpecs = append(out.GitTriggerSpecs, string(bytes.TrimSpace(cfg.Bootstrap.Git.Triggers)))
		}
	}

	if cfg.Bootstrap.MCP != nil {
		owners := append([]string{}, cfg.Bootstrap.MCP.Owners...)
		owners = append(owners, cfg.Bootstrap.MCP.Owner...)
		out.MCPDefaultOwners = normalizeOwners(owners)

		if len(bytes.TrimSpace(cfg.Bootstrap.MCP.Servers)) > 0 {
			out.MCPServerSpecs = append(out.MCPServerSpecs, string(bytes.TrimSpace(cfg.Bootstrap.MCP.Servers)))
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
	secretValues, err := resolveConfigSecretValues(cfg, args)
	if err != nil {
		return err
	}
	updates, err := flattenConfigEnv(cfg, secretValues)
	if err != nil {
		return err
	}
	if err := applyEnvUpdates(updates, bootstrap.OverrideEnv); err != nil {
		return err
	}
	// Record the on-disk source so the admin settings API can persist changes
	// back to it. Inline config documents are not persistable.
	if trimmedSource := strings.TrimSpace(source); trimmedSource != "" && !strings.HasPrefix(trimmedSource, "inline") {
		runtimecfg.SetConfigSource(trimmedSource)
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

	// Install the leveled logger before any command starts so LOG_LEVEL /
	// LOG_FORMAT apply to startup logs as well.
	if err := logging.ConfigureFromEnv(); err != nil {
		log.Printf("Warning: %v (falling back to LOG_LEVEL=info)", err)
		_ = logging.Configure(logging.Options{Level: "info"})
	}

	rootCmd := &ufcli.Command{
		Name:                          "open-chat",
		Usage:                         "Open Chat command line interface",
		Version:                       cmd.BuildVersion(),
		CustomRootCommandHelpTemplate: cmd.RootHelpTemplate(),
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
			// Registered so the password advertised by resolveBootSecretPassword
			// (and the `config defines secret blocks` error) is a real flag rather
			// than one urfave/cli rejects as undefined. The value is read from the
			// raw args before command parsing; the flag only needs to exist here.
			&ufcli.StringFlag{
				Name:    "secret-password",
				Usage:   "password used to unseal config `secrets:` blocks at boot",
				Sources: ufcli.EnvVars("OPEN_CHAT_SECRET_PASSWORD"),
			},
			&ufcli.StringFlag{
				Name:    "secret-password-file",
				Usage:   "path to a file containing the boot secret password",
				Sources: ufcli.EnvVars("OPEN_CHAT_SECRET_PASSWORD_FILE"),
			},
		},
		Commands: integrationCLICommands(
			cmd.RunCli(),
			cmd.ConfigCli(),
			cmd.ServerCli(),
			cmd.WorkerCli(),
			cmd.InstallCli(),
			cmd.UninstallCli(),
			cmd.StatusCli(),
			cmd.SecretCli(),
			cmd.ServiceRunCli(),
		),
	}

	ufcli.VersionPrinter = cmd.PrintVersion

	if err := rootCmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}

// integrationCLICommands appends the top-level subcommands contributed by
// compiled-in integrations (e.g. `client` from go_client, `tui` from tui) to
// the built-in commands. Integrations own their command implementations; the
// core only wires whichever ones the build profile includes.
func integrationCLICommands(builtin ...*ufcli.Command) []*ufcli.Command {
	commands := append([]*ufcli.Command(nil), builtin...)
	for _, ref := range extiface.CLICommands() {
		scoped := ref.Command
		command := scoped.Command()
		if command == nil {
			continue
		}
		if strings.TrimSpace(command.Name) == "" {
			command.Name = scoped.Name
		}
		commands = append(commands, command)
	}
	return commands
}
