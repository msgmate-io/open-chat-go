package tools

import (
	"backend/api/msgmate"
	"backend/database"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type ToolListItem struct {
	Name                           string                 `json:"name"`
	FunctionName                   string                 `json:"function_name"`
	Description                    string                 `json:"description"`
	Type                           string                 `json:"type"`
	SourcePath                     string                 `json:"source_path,omitempty"`
	SourceLine                     int                    `json:"source_line,omitempty"`
	SourceURL                      string                 `json:"source_url,omitempty"`
	RequiresInit                   bool                   `json:"requires_init,omitempty"`
	RequiresConfirmation           bool                   `json:"requires_confirmation,omitempty"`
	StopOnFirstConfirmableToolCall bool                   `json:"stop_on_first_confirmable_tool_call,omitempty"`
	ConfirmationBlockMessage       string                 `json:"confirmation_block_message,omitempty"`
	Parameters                     map[string]interface{} `json:"parameters,omitempty"`
	Required                       []string               `json:"required,omitempty"`
	CallSchema                     map[string]interface{} `json:"call_schema,omitempty"`
	InitSchema                     map[string]interface{} `json:"init_schema,omitempty"`
}

type toolStaticMetadata struct {
	Description string
	SourcePath  string
	SourceLine  int
	SourceURL   string
	InitSchema  map[string]interface{}
}

var (
	toolMetadataOnce sync.Once
	toolMetadataMap  map[string]toolStaticMetadata
)

type toolsFilters struct {
	Types []string `json:"types"`
}

type ToolsListResponse struct {
	Page       int            `json:"page"`
	PageSize   int            `json:"page_size"`
	TotalRows  int64          `json:"total_rows"`
	TotalPages int            `json:"total_pages"`
	Rows       []ToolListItem `json:"rows"`
	Filters    toolsFilters   `json:"filters"`
}

// List returns the platform tool catalog for guests and authenticated users.
//
//	@Summary		List tools
//	@Description	List callable tools with pagination and filters. Admin-only tools are only visible to admin users.
//	@Tags			tools
//	@Produce		json
//	@Param			page query int false "Page number" minimum(1)
//	@Param			page_size query int false "Page size" minimum(1) maximum(100)
//	@Param			type query string false "Filter by tool type"
//	@Param			q query string false "Search by tool name or description"
//	@Param			requires_init query string false "Authenticated only: true/false filter"
//	@Param			requires_confirmation query string false "Authenticated only: true/false filter"
//	@Success		200 {object} ToolsListResponse
//	@Router			/api/v1/tools/list [get]
func (h *ToolsHandler) List(w http.ResponseWriter, r *http.Request) {
	toolMetadataOnce.Do(func() {
		toolMetadataMap = loadToolMetadataFromSource()
	})

	user, _ := r.Context().Value("user").(*database.User)
	isAuthenticated := user != nil
	isAdmin := user != nil && user.IsAdmin

	page := 1
	pageSize := 12
	if pageParam := strings.TrimSpace(r.URL.Query().Get("page")); pageParam != "" {
		if parsed, err := strconv.Atoi(pageParam); err == nil && parsed > 0 {
			page = parsed
		}
	}
	if pageSizeParam := strings.TrimSpace(r.URL.Query().Get("page_size")); pageSizeParam != "" {
		if parsed, err := strconv.Atoi(pageSizeParam); err == nil && parsed > 0 && parsed <= 100 {
			pageSize = parsed
		}
	}

	typeFilter := strings.TrimSpace(r.URL.Query().Get("type"))
	queryFilter := strings.TrimSpace(r.URL.Query().Get("q"))
	requiresInitFilter := strings.TrimSpace(r.URL.Query().Get("requires_init"))
	requiresConfirmationFilter := strings.TrimSpace(r.URL.Query().Get("requires_confirmation"))

	rows := make([]ToolListItem, 0, len(msgmate.AllTools))
	typesSet := make(map[string]struct{})

	for _, tool := range msgmate.AllTools {
		if tool.GetAdminOnly() && !isAdmin {
			continue
		}

		typeValue := strings.TrimSpace(tool.GetToolType())
		if typeValue != "" {
			typesSet[typeValue] = struct{}{}
		}

		if typeFilter != "" && !strings.EqualFold(typeValue, typeFilter) {
			continue
		}

		requiresInit := tool.GetRequiresInit()
		requiresConfirmation := tool.GetRequiresConfirmation()
		if isAuthenticated && requiresInitFilter != "" {
			switch strings.ToLower(requiresInitFilter) {
			case "true", "1", "yes":
				if !requiresInit {
					continue
				}
			case "false", "0", "no":
				if requiresInit {
					continue
				}
			}
		}
		if isAuthenticated && requiresConfirmationFilter != "" {
			switch strings.ToLower(requiresConfirmationFilter) {
			case "true", "1", "yes":
				if !requiresConfirmation {
					continue
				}
			case "false", "0", "no":
				if requiresConfirmation {
					continue
				}
			}
		}

		if queryFilter != "" {
			q := strings.ToLower(queryFilter)
			name := strings.ToLower(tool.GetToolName())
			description := strings.ToLower(tool.GetToolDescription())
			if !strings.Contains(name, q) && !strings.Contains(description, q) {
				continue
			}
		}

		item := ToolListItem{
			Name:         tool.GetToolName(),
			FunctionName: tool.GetToolFunctionName(),
			Description:  tool.GetToolDescription(),
			Type:         typeValue,
		}

		if metadata, ok := toolMetadataMap[item.Name]; ok {
			if strings.TrimSpace(metadata.Description) != "" {
				item.Description = metadata.Description
			}
			item.SourcePath = metadata.SourcePath
			item.SourceLine = metadata.SourceLine
			item.SourceURL = metadata.SourceURL
			if metadata.InitSchema != nil {
				item.InitSchema = metadata.InitSchema
			}
		}

		if item.SourcePath == "" || item.SourceLine == 0 {
			if file, line := runtimeLocationForTool(tool); file != "" {
				item.SourcePath = file
				item.SourceLine = line
				item.SourceURL = buildRepoSourceURL(file, line)
			}
		}

		if isAuthenticated {
			item.RequiresInit = requiresInit
			item.RequiresConfirmation = requiresConfirmation
			item.StopOnFirstConfirmableToolCall = tool.GetStopOnFirstConfirmableToolCall()
			item.ConfirmationBlockMessage = strings.TrimSpace(tool.GetConfirmationBlockMessage())
			item.Parameters = tool.GetToolParameters()
			item.CallSchema = map[string]interface{}{
				"type":       "object",
				"properties": tool.GetToolParameters(),
			}
			if baseTool, ok := tool.(*msgmate.BaseTool); ok {
				item.Required = append([]string(nil), baseTool.GetRequiredParams()...)
				item.CallSchema["required"] = append([]string(nil), baseTool.GetRequiredParams()...)
			}
			if item.InitSchema == nil && requiresInit {
				item.InitSchema = map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
					"required":   []string{},
				}
			}
		}

		rows = append(rows, item)
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Type == rows[j].Type {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Type < rows[j].Type
	})

	types := make([]string, 0, len(typesSet))
	for t := range typesSet {
		types = append(types, t)
	}
	sort.Strings(types)

	totalRows := int64(len(rows))
	totalPages := 0
	if totalRows > 0 {
		totalPages = int((totalRows + int64(pageSize) - 1) / int64(pageSize))
	}
	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}
	if offset > len(rows) {
		offset = len(rows)
	}
	end := offset + pageSize
	if end > len(rows) {
		end = len(rows)
	}

	response := ToolsListResponse{
		Page:       page,
		PageSize:   pageSize,
		TotalRows:  totalRows,
		TotalPages: totalPages,
		Rows:       rows[offset:end],
		Filters:    toolsFilters{Types: types},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func runtimeLocationForTool(tool msgmate.Tool) (string, int) {
	rv := reflect.ValueOf(tool)
	method := rv.MethodByName("RunTool")
	if !method.IsValid() {
		return "", 0
	}
	pc := method.Pointer()
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return "", 0
	}
	file, line := fn.FileLine(pc)
	return file, line
}

func loadToolMetadataFromSource() map[string]toolStaticMetadata {
	result := map[string]toolStaticMetadata{}

	cwd, err := os.Getwd()
	if err != nil {
		return result
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, ".."))
	if strings.HasSuffix(filepath.ToSlash(cwd), "/backend") {
		repoRoot = filepath.Clean(filepath.Join(cwd, ".."))
	} else if strings.HasSuffix(filepath.ToSlash(cwd), "/open-chat-go") {
		repoRoot = cwd
	}

	msgmateRootCandidates := []string{
		filepath.Join(cwd, "api", "msgmate"),
		filepath.Join(cwd, "backend", "api", "msgmate"),
	}

	var msgmateRoot string
	for _, candidate := range msgmateRootCandidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			msgmateRoot = candidate
			break
		}
	}
	if msgmateRoot == "" {
		return result
	}

	sourceDirs := []string{msgmateRoot, filepath.Join(msgmateRoot, "tools")}

	fset := token.NewFileSet()
	type schemaCtx struct {
		structSchemas map[string]map[string]interface{}
	}

	for _, sourceDir := range sourceDirs {
		pkgs, err := parser.ParseDir(fset, sourceDir, nil, parser.ParseComments)
		if err != nil {
			continue
		}

		for _, pkg := range pkgs {
			for filePath, file := range pkg.Files {
				ctx := schemaCtx{structSchemas: map[string]map[string]interface{}{}}
				for _, decl := range file.Decls {
					gd, ok := decl.(*ast.GenDecl)
					if !ok || gd.Tok != token.TYPE {
						continue
					}
					for _, spec := range gd.Specs {
						ts, ok := spec.(*ast.TypeSpec)
						if !ok {
							continue
						}
						st, ok := ts.Type.(*ast.StructType)
						if !ok {
							continue
						}
						ctx.structSchemas[ts.Name.Name] = structToSchema(st)
					}
				}

				for _, decl := range file.Decls {
					gd, ok := decl.(*ast.GenDecl)
					if ok && gd.Tok == token.VAR {
						for _, spec := range gd.Specs {
							vs, ok := spec.(*ast.ValueSpec)
							if !ok {
								continue
							}
							for i, name := range vs.Names {
								if !strings.HasSuffix(name.Name, "ToolDef") {
									continue
								}
								if i >= len(vs.Values) {
									continue
								}
								cl, ok := vs.Values[i].(*ast.CompositeLit)
								if !ok {
									continue
								}
								toolName := ""
								for _, elt := range cl.Elts {
									kv, ok := elt.(*ast.KeyValueExpr)
									if !ok {
										continue
									}
									key, ok := kv.Key.(*ast.Ident)
									if !ok || key.Name != "Name" {
										continue
									}
									if lit, ok := kv.Value.(*ast.BasicLit); ok {
										toolName = strings.Trim(lit.Value, "\"")
									}
								}
								if toolName == "" {
									continue
								}
								doc := strings.TrimSpace(commentText(vs.Doc, gd.Doc))
								line := fset.Position(vs.Pos()).Line
								meta := result[toolName]
								if doc != "" {
									meta.Description = doc
								}
								meta.SourcePath = filePath
								meta.SourceLine = line
								meta.SourceURL = buildRepoSourceURLFromRoot(repoRoot, filePath, line)
								initTypeName := strings.TrimSuffix(name.Name, "Def") + "Init"
								if schema, ok := ctx.structSchemas[initTypeName]; ok {
									meta.InitSchema = schema
								}
								result[toolName] = meta
							}
						}
					}

					fd, ok := decl.(*ast.FuncDecl)
					if !ok || !strings.HasPrefix(fd.Name.Name, "New") || fd.Body == nil {
						continue
					}
					toolName := ""
					ast.Inspect(fd.Body, func(n ast.Node) bool {
						cl, ok := n.(*ast.CompositeLit)
						if !ok {
							return true
						}
						ident, ok := cl.Type.(*ast.Ident)
						if !ok || ident.Name != "BaseTool" {
							return true
						}
						for _, elt := range cl.Elts {
							kv, ok := elt.(*ast.KeyValueExpr)
							if !ok {
								continue
							}
							key, ok := kv.Key.(*ast.Ident)
							if !ok || key.Name != "ToolName" {
								continue
							}
							if lit, ok := kv.Value.(*ast.BasicLit); ok {
								toolName = strings.Trim(lit.Value, "\"")
								return false
							}
						}
						return true
					})
					if toolName == "" {
						continue
					}
					meta := result[toolName]
					if strings.TrimSpace(meta.Description) == "" {
						meta.Description = strings.TrimSpace(commentText(fd.Doc))
					}
					meta.SourcePath = filePath
					meta.SourceLine = fset.Position(fd.Pos()).Line
					meta.SourceURL = buildRepoSourceURLFromRoot(repoRoot, filePath, meta.SourceLine)
					result[toolName] = meta
				}
			}
		}
	}

	return result
}

func buildRepoSourceURL(filePath string, line int) string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, ".."))
	if strings.HasSuffix(filepath.ToSlash(cwd), "/open-chat-go") {
		repoRoot = cwd
	}
	return buildRepoSourceURLFromRoot(repoRoot, filePath, line)
}

func buildRepoSourceURLFromRoot(repoRoot, filePath string, line int) string {
	const repoSourceBaseURL = "https://github.com/msgmate-io/open-chat-go/blob/main"
	if line <= 0 {
		line = 1
	}
	relPath, err := filepath.Rel(repoRoot, filePath)
	if err != nil {
		return ""
	}
	relPath = filepath.ToSlash(relPath)
	return repoSourceBaseURL + "/" + relPath + "#L" + strconv.Itoa(line)
}

func commentText(groups ...*ast.CommentGroup) string {
	for _, g := range groups {
		if g == nil {
			continue
		}
		text := strings.TrimSpace(g.Text())
		if text != "" {
			return text
		}
	}
	return ""
}

func structToSchema(st *ast.StructType) map[string]interface{} {
	properties := map[string]interface{}{}
	required := make([]string, 0)

	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			continue
		}
		name := field.Names[0].Name
		jsonName := strings.ToLower(name)
		if field.Tag != nil {
			tag := strings.Trim(field.Tag.Value, "`")
			for _, part := range strings.Split(tag, " ") {
				if strings.HasPrefix(part, "json:") {
					value := strings.TrimPrefix(part, "json:")
					value = strings.Trim(value, "\"")
					if value == "-" {
						jsonName = ""
						break
					}
					pieces := strings.Split(value, ",")
					if pieces[0] != "" {
						jsonName = pieces[0]
					}
					optional := false
					for _, p := range pieces[1:] {
						if p == "omitempty" {
							optional = true
						}
					}
					if !optional {
						required = append(required, jsonName)
					}
				}
			}
		}
		if jsonName == "" {
			continue
		}
		properties[jsonName] = map[string]interface{}{"type": astExprToJSONType(field.Type)}
	}

	schema := map[string]interface{}{
		"type":       "object",
		"properties": properties,
		"required":   required,
	}
	return schema
}

func astExprToJSONType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		switch t.Name {
		case "string":
			return "string"
		case "bool":
			return "boolean"
		case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
			return "integer"
		case "float32", "float64":
			return "number"
		default:
			return "object"
		}
	case *ast.ArrayType:
		return "array"
	case *ast.MapType:
		return "object"
	case *ast.StarExpr:
		return astExprToJSONType(t.X)
	default:
		return "object"
	}
}
