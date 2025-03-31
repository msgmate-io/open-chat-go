package admin

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type goTestItem struct {
	Name        string `json:"name"`
	Package     string `json:"package"`
	FilePath    string `json:"file_path"`
	SourceURL   string `json:"source_url"`
	Line        int    `json:"line"`
	DocTag      string `json:"doc_tag,omitempty"`
	Description string `json:"description,omitempty"`
}

type goTestsOverviewResponse struct {
	GeneratedAt string       `json:"generated_at"`
	Tests       []goTestItem `json:"tests"`
}

// @doc:open-chat-go-tests-overview
// This endpoint indexes Go test functions by scanning *_test.go files and
// returns test names, package paths, source links, and optional doc tags.
//
// If a test comment includes an @doc:<tag>, that tag is exposed as doc_tag and
// the matching description is resolved from the existing tagged docs registry.
func GetGoTestsOverview(w http.ResponseWriter, r *http.Request) {
	items, err := collectGoTestsOverview()
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to build tests overview: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(goTestsOverviewResponse{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Tests:       items,
	})
}

func collectGoTestsOverview() ([]goTestItem, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return nil, fmt.Errorf("unable to resolve source path")
	}

	backendDir := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	repoRoot := filepath.Clean(filepath.Join(backendDir, ".."))
	repoSourceBaseURL := "https://github.com/msgmate-io/open-chat-go/blob/main"
	docIndex := loadTaggedDocs()

	fset := token.NewFileSet()
	items := make([]goTestItem, 0)

	err := filepath.WalkDir(backendDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}

		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == "node_modules" || base == "vendor" || base == "go" || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.Contains(filepath.ToSlash(path), "/pkg/mod/") {
			return nil
		}

		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}

		parsed, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return nil
		}

		relPath, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return nil
		}
		relPath = filepath.ToSlash(relPath)

		pkgPath := strings.TrimPrefix(filepath.Dir(relPath), "./")
		if pkgPath == "." {
			pkgPath = ""
		}

		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil || !isGoTestFunction(fn) {
				continue
			}

			line := fset.Position(fn.Pos()).Line
			sourceURL := fmt.Sprintf("%s/%s#L%d", repoSourceBaseURL, relPath, line)

			var docTag string
			if fn.Doc != nil {
				if matches := docTagRegex.FindStringSubmatch(fn.Doc.Text()); len(matches) > 1 {
					docTag = strings.TrimSpace(matches[1])
				}
			}

			description := ""
			if docTag != "" {
				if doc, exists := docIndex[docTag]; exists {
					description = strings.TrimSpace(doc.Content)
				}
			}

			if description == "" && fn.Doc != nil {
				raw := strings.TrimSpace(docTagRegex.ReplaceAllString(fn.Doc.Text(), ""))
				description = strings.ReplaceAll(raw, "\n", " ")
				description = strings.Join(strings.Fields(description), " ")
			}

			items = append(items, goTestItem{
				Name:        fn.Name.Name,
				Package:     pkgPath,
				FilePath:    relPath,
				SourceURL:   sourceURL,
				Line:        line,
				DocTag:      docTag,
				Description: description,
			})
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Package != items[j].Package {
			return items[i].Package < items[j].Package
		}
		if items[i].FilePath != items[j].FilePath {
			return items[i].FilePath < items[j].FilePath
		}
		return items[i].Name < items[j].Name
	})

	return items, nil
}

func isGoTestFunction(fn *ast.FuncDecl) bool {
	if fn.Recv != nil {
		return false
	}
	if !strings.HasPrefix(fn.Name.Name, "Test") {
		return false
	}
	if fn.Type == nil || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
		return false
	}
	param := fn.Type.Params.List[0]
	starExpr, ok := param.Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	selector, ok := starExpr.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	xIdent, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	return xIdent.Name == "testing" && selector.Sel != nil && selector.Sel.Name == "T"
}
