package admin

import (
	"backend/database"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"gorm.io/gorm"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
)

type FieldInfo struct {
	Name       string `json:"name"`
	NameRaw    string `json:"name_raw"`
	Type       string `json:"type"`
	IsPrimary  bool   `json:"is_primary"`
	IsNullable bool   `json:"is_nullable"`
	Tag        string `json:"tag"`
}

type TableInfo struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	SourceURL   string      `json:"source_url,omitempty"`
	Fields      []FieldInfo `json:"fields"`
}

type modelDocMeta struct {
	Description string
	SourceURL   string
}

var (
	modelDescriptionOnce  sync.Once
	modelDescriptionCache map[string]modelDocMeta
)

func loadModelDescriptions(DB *gorm.DB) map[string]modelDocMeta {
	modelDescriptionOnce.Do(func() {
		modelDescriptionCache = make(map[string]modelDocMeta)
		fset := token.NewFileSet()
		_, thisFile, _, ok := runtime.Caller(0)
		if !ok {
			return
		}

		databaseDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "database")
		backendDir := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
		repoRoot := filepath.Clean(filepath.Join(backendDir, ".."))
		repoSourceBaseURL := "https://github.com/msgmate-io/open-chat-go/blob/main"
		databaseDir = filepath.Clean(databaseDir)
		if stat, err := os.Stat(databaseDir); err != nil || !stat.IsDir() {
			return
		}

		pattern := filepath.Join(databaseDir, "*.go")
		files, err := filepath.Glob(pattern)
		if err != nil {
			return
		}

		metaByType := make(map[string]modelDocMeta)
		for _, filePath := range files {
			parsed, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
			if err != nil {
				continue
			}

			for _, decl := range parsed.Decls {
				genDecl, ok := decl.(*ast.GenDecl)
				if !ok || genDecl.Tok != token.TYPE {
					continue
				}

				for _, spec := range genDecl.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if _, ok := typeSpec.Type.(*ast.StructType); !ok {
						continue
					}

					position := fset.Position(typeSpec.Pos())
					repoRelativePath, err := filepath.Rel(repoRoot, position.Filename)
					if err != nil {
						continue
					}
					repoRelativePath = filepath.ToSlash(repoRelativePath)

					commentGroup := typeSpec.Doc
					if commentGroup == nil {
						commentGroup = genDecl.Doc
					}

					description := ""
					if commentGroup != nil {
						description = strings.TrimSpace(commentGroup.Text())
					}

					metaByType[typeSpec.Name.Name] = modelDocMeta{
						Description: description,
						SourceURL:   fmt.Sprintf("%s/%s#L%d", repoSourceBaseURL, repoRelativePath, position.Line),
					}
				}
			}
		}

		for _, model := range database.Tabels {
			stmt := &gorm.Statement{DB: DB}
			if err := stmt.Parse(model); err != nil || stmt.Schema == nil {
				continue
			}

			t := reflect.TypeOf(model)
			if t.Kind() == reflect.Ptr {
				t = t.Elem()
			}
			if t.Kind() != reflect.Struct {
				continue
			}

			if meta, ok := metaByType[t.Name()]; ok {
				modelDescriptionCache[stmt.Schema.Table] = meta
			}
		}
	})

	return modelDescriptionCache
}

type TableInfoConfig struct {
	IncludeFields   []string
	Preloads        []string
	PreloadMappings map[string]string // Maps preload field names to JSON keys
	JsonFields      []string          // Fields containing JSON data that should be parsed
}

var tableConfigurations = map[string]TableInfoConfig{
	"users": {
		IncludeFields: []string{"ID", "CreatedAt", "UpdatedAt", "DeletedAt", "Name", "Email", "Password", "Role"},
	},
	"messages": {
		IncludeFields: []string{"UUID", "ID", "CreatedAt", "DeletedAt", "SenderId", "ReceiverId", "DataType", "ChatId", "Text", "Reasoning", "MetaData"},
	},
	"chats": {
		IncludeFields: []string{"ID", "CreatedAt", "UpdatedAt", "DeletedAt", "User1Id", "User2Id", "LatestMessageId", "SharedConfigId", "ChatType"},
		Preloads:      []string{"LatestMessage", "SharedConfig", "User1", "User2"},
		PreloadMappings: map[string]string{
			"LatestMessage": "latest_message",
			"SharedConfig":  "shared_config",
			"User1":         "user1",
			"User2":         "user2",
		},
	},
	"shared_chat_configs": {
		IncludeFields: []string{"ID", "CreatedAt", "UpdatedAt", "DeletedAt", "ChatId", "ConfigData"},
		Preloads:      []string{"Chat"},
		PreloadMappings: map[string]string{
			"Chat": "chat",
		},
		JsonFields: []string{"ConfigData"},
	},
	"shared_chat_instances": {
		IncludeFields: []string{"ID", "CreatedAt", "UpdatedAt", "DeletedAt", "ChatId", "OwningUserId", "ChatShareUUID"},
		Preloads:      []string{"Chat", "OwningUser"},
		PreloadMappings: map[string]string{
			"Chat":       "chat",
			"OwningUser": "owning_user",
		},
	},
	"permissions": {
		IncludeFields: []string{"UUID", "ID", "CreatedAt", "UpdatedAt", "DeletedAt", "UserId", "Permission"},
		Preloads:      []string{"User"},
		PreloadMappings: map[string]string{
			"User": "user",
		},
	},
	"access_tokens": {
		IncludeFields: []string{"UUID", "ID", "CreatedAt", "UpdatedAt", "DeletedAt", "UserId", "Name", "TokenPrefix", "LastUsedAt", "ExpiresAt", "RevokedAt"},
		Preloads:      []string{"User"},
		PreloadMappings: map[string]string{
			"User": "user",
		},
	},
	"registration_requests": {
		IncludeFields: []string{"UUID", "ID", "CreatedAt", "UpdatedAt", "DeletedAt", "Name", "Email", "Status", "ReviewNote", "ReviewedAt", "ReviewedByUserId", "ApprovedUserId"},
		Preloads:      []string{"ReviewedByUser", "ApprovedUser"},
		PreloadMappings: map[string]string{
			"ReviewedByUser": "reviewed_by_user",
			"ApprovedUser":   "approved_user",
		},
	},
}
