package benchmark

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AggregateTSVToJSON maps per-model result directories (named with the harness
// slug convention provider_model_free) to {modelID: {test: status}} rows.
//
// Each result directory contains one or more *.tsv files emitted by the
// private-sandbox runner; every line is "<test>\t<status>" and the last
// occurrence of a test within a model wins. Directories without result files
// are skipped, and an empty tree yields an empty map (never an error).
//
// The model identifier is reconstructed from the directory slug by converting
// underscores between the provider and model segments back to "/" and the
// trailing "_free" back to ":free". It is a pure function over the local
// benchmark-results tree; it performs no network or sandbox access, matching
// the design rules in types.go.
func AggregateTSVToJSON(resultsRoot string) (map[string]map[string]string, error) {
	rows := map[string]map[string]string{}

	modelDirs, err := os.ReadDir(resultsRoot)
	if err != nil {
		return nil, fmt.Errorf("benchmark: read results root %s: %w", resultsRoot, err)
	}

	for _, dirEntry := range modelDirs {
		if !dirEntry.IsDir() {
			continue
		}
		slug := dirEntry.Name()
		modelID := modelIDFromSlug(slug)

		statusByTest := map[string]string{}
		tsvFiles, err := filepath.Glob(filepath.Join(resultsRoot, slug, "*.tsv"))
		if err != nil {
			return nil, fmt.Errorf("benchmark: glob %s: %w", slug, err)
		}
		sort.Strings(tsvFiles)
		for _, tsv := range tsvFiles {
			lines, err := readTSVLines(tsv)
			if err != nil {
				return nil, err
			}
			for _, line := range lines {
				fields := strings.SplitN(line, "\t", 2)
				if len(fields) != 2 {
					continue
				}
				test := strings.TrimSpace(fields[0])
				status := strings.TrimSpace(fields[1])
				if test == "" || status == "" {
					continue
				}
				// Later files/rows override earlier ones so a retried or
				// finalized status wins over an intermediate one.
				statusByTest[test] = status
			}
		}
		if len(statusByTest) > 0 {
			rows[modelID] = statusByTest
		}
	}

	return rows, nil
}

// modelIDFromSlug reverses the harness slug convention. The slug is built from
// "provider/model:free" by replacing "/" with "_" and ":" with "_", so we
// restore the identifier by replacing the first "_" with "/" and the trailing
// "_free" with ":free".
func modelIDFromSlug(slug string) string {
	id := slug
	if strings.HasSuffix(id, "_free") {
		id = strings.TrimSuffix(id, "_free") + ":free"
	}
	if i := strings.IndexByte(id, '_'); i >= 0 {
		id = id[:i] + "/" + id[i+1:]
	}
	return id
}

func readTSVLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("benchmark: read %s: %w", path, err)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, nil
}
