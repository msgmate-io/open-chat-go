package integrationsettings

import (
	"errors"
	"fmt"
	"strings"

	"backend/runtimecfg"
)

// ConfigSetOp assigns a value at a dotted path. A path segment may narrow a
// list by `[field=value]` (the field may be dotted, e.g. `bot.name`).
type ConfigSetOp struct {
	Path  string      `json:"path"`
	Value interface{} `json:"value"`
}

// ConfigUpsertOp inserts or updates a mapping in the list located at Path,
// matched by the Key field. Existing items are shallow-merged so unrelated
// fields are preserved; new items are appended.
type ConfigUpsertOp struct {
	Path  string                 `json:"path"`
	Key   string                 `json:"key"`
	Value map[string]interface{} `json:"value"`
}

// ConfigDelta is the same non-secret mutation format the CI config-delta
// publisher (development/ci/scripts/apply-open-chat-config-delta.py) applies onto
// a live config document. It lets a single externally authenticated caller set
// one scalar (or upsert one list entry) without shipping the whole config.
type ConfigDelta struct {
	Set    []ConfigSetOp    `json:"set,omitempty"`
	Upsert []ConfigUpsertOp `json:"upsert,omitempty"`
}

// Empty reports whether the delta carries no operations.
func (d ConfigDelta) Empty() bool {
	return len(d.Set) == 0 && len(d.Upsert) == 0
}

// ApplyDelta applies every operation to root in place. The semantics mirror the
// CI config-delta publisher exactly so an inline mutation and a committed delta
// converge on the same document.
func ApplyDelta(root map[string]interface{}, delta ConfigDelta) error {
	if root == nil {
		return errors.New("config root is required")
	}
	for _, op := range delta.Set {
		if err := applySet(root, op); err != nil {
			return err
		}
	}
	for _, op := range delta.Upsert {
		if err := applyUpsert(root, op); err != nil {
			return err
		}
	}
	return nil
}

// SetConfigDelta loads the config document for source (or synthesizes one when
// the source is not persistable), applies the delta and persists the result to
// the local config source and the remote deployment-host mirror.
func SetConfigDelta(source string, delta ConfigDelta) (ApplyResult, error) {
	if delta.Empty() {
		return ApplyResult{}, errors.New("empty config delta")
	}

	defs := ConfigDefinitions()
	root, _, err := LoadConfigDocument(source)
	if err != nil {
		if !errors.Is(err, ErrNotPersistable) {
			return ApplyResult{}, err
		}
		root = BuildRuntimeConfigDocument(defs, runtimecfg.GetAll())
	}

	if err := ApplyDelta(root, delta); err != nil {
		return ApplyResult{}, err
	}

	return persistConfigRoot(source, root, defs)
}

func applySet(root map[string]interface{}, op ConfigSetOp) error {
	path := strings.TrimSpace(op.Path)
	if path == "" {
		return errors.New("set: path is required")
	}
	segments := splitPathSegments(path)
	parent := traversePath(root, segments[:len(segments)-1], true)
	if parent == nil {
		return fmt.Errorf("set: path not found: %s", path)
	}
	last, field, _ := parsePathSegment(segments[len(segments)-1])
	if field != "" {
		return fmt.Errorf("set: last path segment must be a plain key: %s", path)
	}
	parent[last] = op.Value
	return nil
}

func applyUpsert(root map[string]interface{}, op ConfigUpsertOp) error {
	path := strings.TrimSpace(op.Path)
	if path == "" {
		return errors.New("upsert: path is required")
	}
	key := strings.TrimSpace(op.Key)
	if key == "" {
		return fmt.Errorf("upsert: key is required: %s", path)
	}
	if op.Value == nil {
		return fmt.Errorf("upsert: value must be a mapping containing %q: %s", key, path)
	}
	if _, ok := op.Value[key]; !ok {
		return fmt.Errorf("upsert: value must be a mapping containing %q: %s", key, path)
	}

	segments := splitPathSegments(path)
	parent := traversePath(root, segments[:len(segments)-1], true)
	if parent == nil {
		return fmt.Errorf("upsert: could not resolve parent: %s", path)
	}
	last, field, _ := parsePathSegment(segments[len(segments)-1])
	if field != "" {
		return fmt.Errorf("upsert: last path segment must be a plain key: %s", path)
	}

	existing, has := parent[last]
	if !has || existing == nil {
		parent[last] = []interface{}{op.Value}
		return nil
	}
	list, ok := existing.([]interface{})
	if !ok {
		return fmt.Errorf("upsert: target is not a list: %s", path)
	}
	for i, item := range list {
		row, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if fmt.Sprintf("%v", row[key]) != fmt.Sprintf("%v", op.Value[key]) {
			continue
		}
		merged := make(map[string]interface{})
		for k, v := range row {
			merged[k] = v
		}
		for k, v := range op.Value {
			merged[k] = v
		}
		list[i] = merged
		return nil
	}
	parent[last] = append(list, op.Value)
	return nil
}

// splitPathSegments splits a dotted path on top-level dots, leaving dots inside
// `[...]` list selectors intact.
func splitPathSegments(path string) []string {
	segments := []string{}
	var buf strings.Builder
	depth := 0
	for _, ch := range path {
		switch ch {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		}
		if ch == '.' && depth == 0 {
			segments = append(segments, buf.String())
			buf.Reset()
			continue
		}
		buf.WriteRune(ch)
	}
	if buf.Len() > 0 {
		segments = append(segments, buf.String())
	}
	filtered := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment != "" {
			filtered = append(filtered, segment)
		}
	}
	return filtered
}

// parsePathSegment splits a segment into its key and an optional `[field=value]`
// list selector.
func parsePathSegment(segment string) (key string, field string, value string) {
	idx := strings.Index(segment, "[")
	if idx < 0 {
		return segment, "", ""
	}
	key = segment[:idx]
	cond := strings.TrimSuffix(segment[idx+1:], "]")
	field, value, _ = strings.Cut(cond, "=")
	return key, strings.TrimSpace(field), strings.TrimSpace(value)
}

// traversePath walks root along the given segments. When create is true, missing
// mapping keys are created. A nil return means the path could not be resolved.
// It mirrors the traversal in apply-open-chat-config-delta.py: a key step
// descends into the value (which a following `[field=value]` selector expects to
// be a list), and a selector step descends into the matched list item.
func traversePath(root map[string]interface{}, segments []string, create bool) map[string]interface{} {
	var node interface{} = root
	for _, segment := range segments {
		key, field, want := parsePathSegment(segment)
		if key != "" {
			asMap, ok := node.(map[string]interface{})
			if !ok {
				return nil
			}
			child, ok := asMap[key]
			if !ok {
				if !create {
					return nil
				}
				child = map[string]interface{}{}
				asMap[key] = child
			}
			node = child
		}
		if field != "" {
			list, ok := node.([]interface{})
			if !ok {
				return nil
			}
			match := digField(list, field, want)
			if match == nil {
				return nil
			}
			node = match
		}
	}
	result, ok := node.(map[string]interface{})
	if !ok {
		return nil
	}
	return result
}

// digField returns the first list item whose dotted field equals want.
func digField(list []interface{}, field string, want string) map[string]interface{} {
	parts := strings.Split(field, ".")
	for _, item := range list {
		row, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		var current interface{} = row
		found := true
		for _, part := range parts {
			asMap, ok := current.(map[string]interface{})
			if !ok {
				found = false
				break
			}
			current, ok = asMap[part]
			if !ok {
				found = false
				break
			}
		}
		if found && fmt.Sprintf("%v", current) == want {
			return row
		}
	}
	return nil
}
