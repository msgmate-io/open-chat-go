package admin

import (
	"backend/database"
	"backend/runtimecfg"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

type docsSnapshotRelation struct {
	FromTable string `json:"fromTable"`
	FromField string `json:"fromField"`
	ToTable   string `json:"toTable"`
	ToField   string `json:"toField"`
}

type modelsSnapshotPayload struct {
	Tables      []TableInfo            `json:"tables"`
	Relations   []docsSnapshotRelation `json:"relations"`
	SQL         string                 `json:"sql"`
	GeneratedAt string                 `json:"generated_at"`
}

type snapshotStatsResponse struct {
	Snapshot     string `json:"snapshot"`
	TablesCount  int    `json:"tables_count"`
	Relations    int    `json:"relations_count"`
	TagsCount    int    `json:"tags_count"`
	OutputPath   string `json:"output_path"`
	AbsolutePath string `json:"absolute_path,omitempty"`
	WrittenBytes int    `json:"written_bytes,omitempty"`
	FileMtime    string `json:"file_mtime,omitempty"`
	GeneratedAt  string `json:"generated_at"`
	Instructions string `json:"instructions,omitempty"`
}

type writeSnapshotRequest struct {
	OutputPath string      `json:"output_path"`
	Data       interface{} `json:"data"`
}

type snapshotEntry struct {
	build      func(*gorm.DB) (interface{}, error)
	outputPath func() (string, string, error)
}

type serverCommandSnapshotPayload struct {
	ServerDoc struct {
		Tag       string `json:"tag"`
		Content   string `json:"content"`
		SourceURL string `json:"source_url"`
	} `json:"server_doc"`
	ProviderDoc struct {
		Tag       string `json:"tag"`
		Content   string `json:"content"`
		SourceURL string `json:"source_url"`
	} `json:"provider_doc"`
	GeneratedAt string `json:"generated_at"`
}

func isDebugRuntime() bool {
	values := runtimecfg.GetAll()
	if debug, ok := values["DEBUG"]; ok {
		return strings.EqualFold(strings.TrimSpace(debug.Value), "true")
	}
	return strings.EqualFold(strings.TrimSpace(os.Getenv("DEBUG")), "true")
}

func docsSnapshotsGuard(w http.ResponseWriter, r *http.Request) (*gorm.DB, error) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return nil, err
	}
	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return nil, fmt.Errorf("forbidden")
	}
	if !isDebugRuntime() {
		http.Error(w, "Snapshot refresh is development-only", http.StatusForbidden)
		return nil, fmt.Errorf("not in debug runtime")
	}
	return DB, nil
}

func modelsSnapshotPath() (string, string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", "", fmt.Errorf("unable to resolve source path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	abs := filepath.Join(repoRoot, "frontend", "components", "docs", "models-overview.static.json")
	rel := filepath.ToSlash(filepath.Join("frontend", "components", "docs", "models-overview.static.json"))
	return abs, rel, nil
}

func serverCommandSnapshotPath() (string, string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", "", fmt.Errorf("unable to resolve source path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	uiAbs := filepath.Join(repoRoot, "frontend", "packages", "ui", "src", "components", "docs", "server-command.static.json")
	uiRel := filepath.ToSlash(filepath.Join("frontend", "packages", "ui", "src", "components", "docs", "server-command.static.json"))
	return uiAbs, uiRel, nil
}

func getSnapshotEntry(snapshot string) (snapshotEntry, bool) {
	entries := map[string]snapshotEntry{
		"models-overview": {
			build: func(DB *gorm.DB) (interface{}, error) {
				return buildModelsSnapshot(DB)
			},
			outputPath: modelsSnapshotPath,
		},
		"server-command": {
			build: func(_ *gorm.DB) (interface{}, error) {
				return buildServerCommandSnapshot(), nil
			},
			outputPath: serverCommandSnapshotPath,
		},
	}
	entry, ok := entries[snapshot]
	return entry, ok
}

func snapshotCounts(payload interface{}) (tables int, relations int, tags int, generatedAt string) {
	switch value := payload.(type) {
	case modelsSnapshotPayload:
		return len(value.Tables), len(value.Relations), 0, value.GeneratedAt
	case serverCommandSnapshotPayload:
		tagsCount := 0
		if value.ServerDoc.Content != "" {
			tagsCount++
		}
		if value.ProviderDoc.Content != "" {
			tagsCount++
		}
		return 0, 0, tagsCount, value.GeneratedAt
	default:
		return 0, 0, 0, time.Now().UTC().Format(time.RFC3339)
	}
}

func buildModelsSnapshot(DB *gorm.DB) (modelsSnapshotPayload, error) {
	sql, relations, err := buildSchemaSQL(DB)
	if err != nil {
		return modelsSnapshotPayload{}, err
	}

	docMeta := loadModelDescriptions(DB)
	tables := make([]TableInfo, 0, len(database.Tabels))
	seen := make(map[string]struct{})

	for _, model := range database.Tabels {
		stmt := &gorm.Statement{DB: DB}
		if parseErr := stmt.Parse(model); parseErr != nil || stmt.Schema == nil {
			continue
		}
		tableName := stmt.Schema.Table
		if _, exists := seen[tableName]; exists {
			continue
		}
		seen[tableName] = struct{}{}

		fields := make([]FieldInfo, 0, len(stmt.Schema.Fields))
		for _, field := range stmt.Schema.Fields {
			if field == nil {
				continue
			}
			fieldType := string(field.DataType)
			if fieldType == "" {
				fieldType = "unknown"
			}
			fields = append(fields, FieldInfo{
				Name:       field.Name,
				NameRaw:    field.DBName,
				Type:       fieldType,
				IsPrimary:  field.PrimaryKey,
				IsNullable: !field.NotNull,
				Tag:        string(field.TagSettings["JSON"]),
			})
		}

		meta := docMeta[tableName]
		tables = append(tables, TableInfo{Name: tableName, Description: meta.Description, SourceURL: meta.SourceURL, Fields: fields})
	}

	sort.Slice(tables, func(i, j int) bool { return tables[i].Name < tables[j].Name })

	outRelations := make([]docsSnapshotRelation, 0, len(relations))
	for _, relation := range relations {
		outRelations = append(outRelations, docsSnapshotRelation{
			FromTable: relation.FromTable,
			FromField: relation.FromField,
			ToTable:   relation.ToTable,
			ToField:   relation.ToField,
		})
	}

	return modelsSnapshotPayload{
		Tables:      tables,
		Relations:   outRelations,
		SQL:         sql,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func GetModelsSnapshotStats(w http.ResponseWriter, r *http.Request) {
	DB, err := docsSnapshotsGuard(w, r)
	if err != nil {
		return
	}

	payload, err := buildModelsSnapshot(DB)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to build snapshot: %v", err), http.StatusInternalServerError)
		return
	}

	_, relPath, err := modelsSnapshotPath()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snapshotStatsResponse{
		Snapshot:     "models-overview",
		TablesCount:  len(payload.Tables),
		Relations:    len(payload.Relations),
		OutputPath:   relPath,
		GeneratedAt:  payload.GeneratedAt,
		Instructions: "Call POST /api/v1/admin/docs/snapshots/models/refresh to write this snapshot to disk.",
	})
}

func RefreshModelsSnapshot(w http.ResponseWriter, r *http.Request) {
	DB, err := docsSnapshotsGuard(w, r)
	if err != nil {
		return
	}

	payload, err := buildModelsSnapshot(DB)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to build snapshot: %v", err), http.StatusInternalServerError)
		return
	}

	absPath, relPath, err := modelsSnapshotPath()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if mkErr := os.MkdirAll(filepath.Dir(absPath), 0o755); mkErr != nil {
		http.Error(w, fmt.Sprintf("failed to create snapshot directory: %v", mkErr), http.StatusInternalServerError)
		return
	}

	jsonBytes, marshalErr := json.MarshalIndent(payload, "", "  ")
	if marshalErr != nil {
		http.Error(w, fmt.Sprintf("failed to marshal snapshot: %v", marshalErr), http.StatusInternalServerError)
		return
	}

	if writeErr := os.WriteFile(absPath, append(jsonBytes, byte('\n')), 0o644); writeErr != nil {
		http.Error(w, fmt.Sprintf("failed to write snapshot file: %v", writeErr), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snapshotStatsResponse{
		Snapshot:    "models-overview",
		TablesCount: len(payload.Tables),
		Relations:   len(payload.Relations),
		OutputPath:  relPath,
		GeneratedAt: payload.GeneratedAt,
	})
}

func buildServerCommandSnapshot() serverCommandSnapshotPayload {
	docs := loadTaggedDocs()
	payload := serverCommandSnapshotPayload{GeneratedAt: time.Now().UTC().Format(time.RFC3339)}

	if serverDoc, ok := docs["open-chat-server-command-options"]; ok {
		payload.ServerDoc.Tag = "open-chat-server-command-options"
		payload.ServerDoc.Content = serverDoc.Content
		payload.ServerDoc.SourceURL = serverDoc.SourceURL
	}

	if providerDoc, ok := docs["open-chat-provider-env-vars"]; ok {
		payload.ProviderDoc.Tag = "open-chat-provider-env-vars"
		payload.ProviderDoc.Content = providerDoc.Content
		payload.ProviderDoc.SourceURL = providerDoc.SourceURL
	}

	return payload
}

func GetServerCommandSnapshotStats(w http.ResponseWriter, r *http.Request) {
	_, err := docsSnapshotsGuard(w, r)
	if err != nil {
		return
	}

	payload := buildServerCommandSnapshot()
	_, relPath, err := serverCommandSnapshotPath()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	tagsCount := 0
	if payload.ServerDoc.Content != "" {
		tagsCount++
	}
	if payload.ProviderDoc.Content != "" {
		tagsCount++
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snapshotStatsResponse{
		Snapshot:     "server-command",
		TagsCount:    tagsCount,
		OutputPath:   relPath,
		GeneratedAt:  payload.GeneratedAt,
		Instructions: "Call POST /api/v1/admin/docs/snapshots/server-command/refresh to write this snapshot to disk.",
	})
}

func RefreshServerCommandSnapshot(w http.ResponseWriter, r *http.Request) {
	_, err := docsSnapshotsGuard(w, r)
	if err != nil {
		return
	}

	payload := buildServerCommandSnapshot()
	absPath, relPath, err := serverCommandSnapshotPath()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if mkErr := os.MkdirAll(filepath.Dir(absPath), 0o755); mkErr != nil {
		http.Error(w, fmt.Sprintf("failed to create snapshot directory: %v", mkErr), http.StatusInternalServerError)
		return
	}

	jsonBytes, marshalErr := json.MarshalIndent(payload, "", "  ")
	if marshalErr != nil {
		http.Error(w, fmt.Sprintf("failed to marshal snapshot: %v", marshalErr), http.StatusInternalServerError)
		return
	}

	if writeErr := os.WriteFile(absPath, append(jsonBytes, byte('\n')), 0o644); writeErr != nil {
		http.Error(w, fmt.Sprintf("failed to write snapshot file: %v", writeErr), http.StatusInternalServerError)
		return
	}

	tagsCount := 0
	if payload.ServerDoc.Content != "" {
		tagsCount++
	}
	if payload.ProviderDoc.Content != "" {
		tagsCount++
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snapshotStatsResponse{
		Snapshot:    "server-command",
		TagsCount:   tagsCount,
		OutputPath:  relPath,
		GeneratedAt: payload.GeneratedAt,
	})
}

func GetModelsSnapshotData(w http.ResponseWriter, r *http.Request) {
	DB, err := docsSnapshotsGuard(w, r)
	if err != nil {
		return
	}
	payload, err := buildModelsSnapshot(DB)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to build snapshot: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(payload)
}

func GetServerCommandSnapshotData(w http.ResponseWriter, r *http.Request) {
	_, err := docsSnapshotsGuard(w, r)
	if err != nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(buildServerCommandSnapshot())
}

func GetDocsSnapshotDataByTag(w http.ResponseWriter, r *http.Request) {
	DB, err := docsSnapshotsGuard(w, r)
	if err != nil {
		return
	}

	snapshot := strings.TrimSpace(r.PathValue("snapshot"))
	entry, ok := getSnapshotEntry(snapshot)
	if !ok {
		http.Error(w, "Unknown snapshot", http.StatusNotFound)
		return
	}

	payload, err := entry.build(DB)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to build snapshot: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(payload)
}

func WriteDocsSnapshot(w http.ResponseWriter, r *http.Request) {
	_, err := docsSnapshotsGuard(w, r)
	if err != nil {
		return
	}

	var req writeSnapshotRequest
	if decodeErr := json.NewDecoder(r.Body).Decode(&req); decodeErr != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	relPath := filepath.ToSlash(strings.TrimSpace(req.OutputPath))
	if relPath == "" || !strings.HasSuffix(relPath, ".json") {
		http.Error(w, "output_path must be a .json path", http.StatusBadRequest)
		return
	}

	allowed := strings.HasPrefix(relPath, "frontend/components/docs/") || strings.HasPrefix(relPath, "frontend/packages/ui/src/components/docs/")
	if !allowed {
		http.Error(w, "output_path not allowed", http.StatusBadRequest)
		return
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		http.Error(w, "unable to resolve source path", http.StatusInternalServerError)
		return
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	absPath := filepath.Join(repoRoot, filepath.FromSlash(relPath))

	if mkErr := os.MkdirAll(filepath.Dir(absPath), 0o755); mkErr != nil {
		http.Error(w, fmt.Sprintf("failed to create snapshot directory: %v", mkErr), http.StatusInternalServerError)
		return
	}

	jsonBytes, marshalErr := json.MarshalIndent(req.Data, "", "  ")
	if marshalErr != nil {
		http.Error(w, fmt.Sprintf("failed to marshal snapshot: %v", marshalErr), http.StatusInternalServerError)
		return
	}

	if writeErr := os.WriteFile(absPath, append(jsonBytes, byte('\n')), 0o644); writeErr != nil {
		http.Error(w, fmt.Sprintf("failed to write snapshot: %v", writeErr), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"output_path": relPath, "status": "ok"})
}

func GetDocsSnapshotStatsByTag(w http.ResponseWriter, r *http.Request) {
	DB, err := docsSnapshotsGuard(w, r)
	if err != nil {
		return
	}

	snapshot := strings.TrimSpace(r.PathValue("snapshot"))
	entry, ok := getSnapshotEntry(snapshot)
	if !ok {
		http.Error(w, "Unknown snapshot", http.StatusNotFound)
		return
	}

	payload, err := entry.build(DB)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to build snapshot: %v", err), http.StatusInternalServerError)
		return
	}

	absPath, relPath, err := entry.outputPath()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fileMtime := ""
	if info, statErr := os.Stat(absPath); statErr == nil {
		fileMtime = info.ModTime().UTC().Format(time.RFC3339)
	}

	tablesCount, relationsCount, tagsCount, generatedAt := snapshotCounts(payload)
	resp := snapshotStatsResponse{
		Snapshot:     snapshot,
		TablesCount:  tablesCount,
		Relations:    relationsCount,
		TagsCount:    tagsCount,
		OutputPath:   relPath,
		AbsolutePath: absPath,
		FileMtime:    fileMtime,
		GeneratedAt:  generatedAt,
		Instructions: fmt.Sprintf("Call POST /api/v1/admin/docs/snapshots/%s/refresh to write this snapshot to disk.", snapshot),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func RefreshDocsSnapshotByTag(w http.ResponseWriter, r *http.Request) {
	DB, err := docsSnapshotsGuard(w, r)
	if err != nil {
		return
	}

	snapshot := strings.TrimSpace(r.PathValue("snapshot"))
	entry, ok := getSnapshotEntry(snapshot)
	if !ok {
		http.Error(w, "Unknown snapshot", http.StatusNotFound)
		return
	}

	payload, err := entry.build(DB)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to build snapshot: %v", err), http.StatusInternalServerError)
		return
	}

	absPath, relPath, err := entry.outputPath()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if mkErr := os.MkdirAll(filepath.Dir(absPath), 0o755); mkErr != nil {
		http.Error(w, fmt.Sprintf("failed to create snapshot directory: %v", mkErr), http.StatusInternalServerError)
		return
	}

	jsonBytes, marshalErr := json.MarshalIndent(payload, "", "  ")
	if marshalErr != nil {
		http.Error(w, fmt.Sprintf("failed to marshal snapshot: %v", marshalErr), http.StatusInternalServerError)
		return
	}

	if writeErr := os.WriteFile(absPath, append(jsonBytes, byte('\n')), 0o644); writeErr != nil {
		http.Error(w, fmt.Sprintf("failed to write snapshot: %v", writeErr), http.StatusInternalServerError)
		return
	}

	tablesCount, relationsCount, tagsCount, generatedAt := snapshotCounts(payload)
	fileMtime := ""
	if info, statErr := os.Stat(absPath); statErr == nil {
		fileMtime = info.ModTime().UTC().Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snapshotStatsResponse{
		Snapshot:    snapshot,
		TablesCount: tablesCount,
		Relations:   relationsCount,
		TagsCount:   tagsCount,
		OutputPath:  relPath,
		AbsolutePath: absPath,
		WrittenBytes: len(jsonBytes) + 1,
		FileMtime:    fileMtime,
		GeneratedAt: generatedAt,
	})
}
