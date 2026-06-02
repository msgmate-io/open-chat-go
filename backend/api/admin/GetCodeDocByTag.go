package admin

import (
	"backend/server/util"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

type TaggedDocResponse struct {
	Tag       string `json:"tag"`
	Content   string `json:"content"`
	SourceURL string `json:"source_url"`
}

type taggedDoc struct {
	Content   string
	SourceURL string
}

var (
	taggedDocsOnce sync.Once
	taggedDocs     map[string]taggedDoc
)

var docTagRegex = regexp.MustCompile(`@doc:([a-zA-Z0-9._-]+)`)

func loadTaggedDocs() map[string]taggedDoc {
	taggedDocsOnce.Do(func() {
		taggedDocs = make(map[string]taggedDoc)

		_, thisFile, _, ok := runtime.Caller(0)
		if !ok {
			return
		}

		backendDir := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
		repoRoot := filepath.Clean(filepath.Join(backendDir, ".."))
		repoSourceBaseURL := "https://github.com/msgmate-io/open-chat-go/blob/main"

		fset := token.NewFileSet()
		_ = filepath.WalkDir(backendDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				base := d.Name()
				if base == "vendor" || strings.HasPrefix(base, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) != ".go" {
				return nil
			}

			parsed, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if parseErr != nil {
				return nil
			}

			for _, group := range parsed.Comments {
				if group == nil {
					continue
				}
				text := strings.TrimSpace(group.Text())
				if text == "" {
					continue
				}

				matches := docTagRegex.FindAllStringSubmatch(text, -1)
				if len(matches) == 0 {
					continue
				}

				content := docTagRegex.ReplaceAllString(text, "")
				content = strings.TrimSpace(content)
				if content == "" {
					continue
				}

				position := fset.Position(group.Pos())
				relPath, relErr := filepath.Rel(repoRoot, position.Filename)
				if relErr != nil {
					continue
				}
				relPath = filepath.ToSlash(relPath)
				sourceURL := fmt.Sprintf("%s/%s#L%d", repoSourceBaseURL, relPath, position.Line)

				for _, match := range matches {
					if len(match) < 2 {
						continue
					}
					tag := strings.TrimSpace(match[1])
					if tag == "" {
						continue
					}
					taggedDocs[tag] = taggedDoc{Content: content, SourceURL: sourceURL}
				}
			}

			return nil
		})
	})

	return taggedDocs
}

func GetCodeDocByTag(w http.ResponseWriter, r *http.Request) {
	_, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	tag := strings.TrimSpace(r.PathValue("tag"))
	if tag == "" {
		http.Error(w, "Tag is required", http.StatusBadRequest)
		return
	}

	doc, ok := loadTaggedDocs()[tag]
	if !ok {
		http.Error(w, "Doc tag not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(TaggedDocResponse{Tag: tag, Content: doc.Content, SourceURL: doc.SourceURL})
}
