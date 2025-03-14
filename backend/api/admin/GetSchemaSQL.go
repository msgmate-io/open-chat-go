package admin

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"gorm.io/gorm"
)

type SQLSchemaResponse struct {
	SQL       string           `json:"sql"`
	Relations []SchemaRelation `json:"relations"`
}

type SchemaRelation struct {
	FromTable string `json:"from_table"`
	FromField string `json:"from_field"`
	ToTable   string `json:"to_table"`
	ToField   string `json:"to_field"`
}

func GetSchemaSQL(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	sql, relations, err := buildSchemaSQL(DB)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to build schema sql: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SQLSchemaResponse{SQL: sql, Relations: relations})
}

func buildSchemaSQL(DB *gorm.DB) (string, []SchemaRelation, error) {
	tableNames := make([]string, 0, len(database.Tabels))
	tableByCanonical := make(map[string]string)
	tableColumns := make(map[string][]string)
	createStatements := make([]string, 0, len(database.Tabels))
	relationStatements := make(map[string]struct{})
	relations := make([]SchemaRelation, 0)
	seenRelations := make(map[string]struct{})

	for _, model := range database.Tabels {
		stmt := &gorm.Statement{DB: DB}
		if err := stmt.Parse(model); err != nil {
			return "", nil, err
		}
		tableName := stmt.Schema.Table
		tableNames = append(tableNames, tableName)
		tableByCanonical[canonicalTableName(tableName)] = tableName

		columnTypes, err := DB.Migrator().ColumnTypes(model)
		if err != nil {
			return "", nil, err
		}

		lines := make([]string, 0, len(columnTypes))
		columns := make([]string, 0, len(columnTypes))
		for _, column := range columnTypes {
			columnName := column.Name()
			columns = append(columns, columnName)

			dbType := column.DatabaseTypeName()
			if dbType == "" {
				dbType = "TEXT"
			}

			line := fmt.Sprintf("  \"%s\" %s", columnName, dbType)
			if nullable, ok := column.Nullable(); ok && !nullable {
				line += " NOT NULL"
			}
			if defaultValue, ok := column.DefaultValue(); ok && defaultValue != "" {
				line += " DEFAULT " + defaultValue
			}
			if primary, ok := column.PrimaryKey(); ok && primary {
				line += " PRIMARY KEY"
			}
			lines = append(lines, line)
		}

		tableColumns[tableName] = columns
		createStatements = append(createStatements, fmt.Sprintf("CREATE TABLE \"%s\" (\n%s\n);", tableName, strings.Join(lines, ",\n")))

		for _, relationship := range stmt.Schema.Relationships.Relations {
			for _, reference := range relationship.References {
				if reference == nil || reference.ForeignKey == nil || reference.PrimaryKey == nil {
					continue
				}

				fromField := reference.ForeignKey.DBName
				toField := reference.PrimaryKey.DBName
				toTable := ""
				if reference.PrimaryKey.Schema != nil {
					toTable = reference.PrimaryKey.Schema.Table
				}
				if toTable == "" && relationship.FieldSchema != nil {
					toTable = relationship.FieldSchema.Table
				}
				if toTable == "" {
					continue
				}

				relationKey := fmt.Sprintf("%s.%s->%s.%s", tableName, fromField, toTable, toField)
				if _, exists := seenRelations[relationKey]; exists {
					continue
				}
				seenRelations[relationKey] = struct{}{}

				relations = append(relations, SchemaRelation{
					FromTable: tableName,
					FromField: fromField,
					ToTable:   toTable,
					ToField:   toField,
				})
			}
		}
	}

	sort.Strings(tableNames)

	for _, tableName := range tableNames {
		for _, columnName := range tableColumns[tableName] {
			if !strings.HasSuffix(columnName, "_id") || columnName == "id" {
				continue
			}
			base := strings.TrimSuffix(columnName, "_id")
			targetTable := tableByCanonical[canonicalTableName(base)]
			if targetTable == "" {
				targetTable = tableByCanonical[canonicalTableName(base+"s")]
			}
			if targetTable == "" {
				continue
			}

			relationSQL := fmt.Sprintf(
				"ALTER TABLE \"%s\" ADD CONSTRAINT \"fk_%s_%s\" FOREIGN KEY (\"%s\") REFERENCES \"%s\"(\"id\");",
				tableName,
				tableName,
				columnName,
				columnName,
				targetTable,
			)
			relationStatements[relationSQL] = struct{}{}

			relationKey := fmt.Sprintf("%s.%s->%s.%s", tableName, columnName, targetTable, "id")
			if _, exists := seenRelations[relationKey]; !exists {
				seenRelations[relationKey] = struct{}{}
				relations = append(relations, SchemaRelation{
					FromTable: tableName,
					FromField: columnName,
					ToTable:   targetTable,
					ToField:   "id",
				})
			}
		}
	}

	relationSQLList := make([]string, 0, len(relationStatements))
	for relationSQL := range relationStatements {
		relationSQLList = append(relationSQLList, relationSQL)
	}
	sort.Strings(relationSQLList)
	sort.Slice(relations, func(i, j int) bool {
		left := relations[i]
		right := relations[j]
		if left.FromTable != right.FromTable {
			return left.FromTable < right.FromTable
		}
		if left.FromField != right.FromField {
			return left.FromField < right.FromField
		}
		if left.ToTable != right.ToTable {
			return left.ToTable < right.ToTable
		}
		return left.ToField < right.ToField
	})

	allStatements := append([]string{"-- Generated schema overview"}, createStatements...)
	if len(relationSQLList) > 0 {
		allStatements = append(allStatements, "", "-- Inferred relationships", strings.Join(relationSQLList, "\n"))
	}

	return strings.Join(allStatements, "\n\n"), relations, nil
}

func canonicalTableName(value string) string {
	return strings.ReplaceAll(strings.ToLower(value), "_", "")
}
