package integrations

import (
	"backend/database"
	backendintegrations "backend/integrations"
	"backend/server/util"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

type IntegrationAccessUserRow struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	IsAdmin bool   `json:"is_admin"`
}

type IntegrationAccessUsersResponse struct {
	Rows []IntegrationAccessUserRow `json:"rows"`
}

type IntegrationAccessRow struct {
	IntegrationName string `json:"integration_name"`
	AdminOnly       bool   `json:"admin_only"`
	UserAccessible  bool   `json:"user_accessible"`
	Assigned        bool   `json:"assigned"`
	Visible         bool   `json:"visible"`
}

type IntegrationAccessByUserResponse struct {
	User IntegrationAccessUserRow `json:"user"`
	Rows []IntegrationAccessRow   `json:"rows"`
}

func requireAdmin(DBUser *database.User, w http.ResponseWriter) bool {
	if DBUser == nil || !DBUser.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return false
	}
	return true
}

func (h *IntegrationsHandler) ListAccessUsers(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if !requireAdmin(user, w) {
		return
	}

	rows := []database.User{}
	if err := DB.Order("is_admin desc, name asc, email asc").Find(&rows).Error; err != nil {
		http.Error(w, "Failed to list users", http.StatusInternalServerError)
		return
	}
	out := make([]IntegrationAccessUserRow, 0, len(rows))
	for _, row := range rows {
		if row.IsAutomated {
			continue
		}
		out = append(out, IntegrationAccessUserRow{
			UUID:    row.UUID,
			Name:    row.Name,
			Email:   row.Email,
			IsAdmin: row.IsAdmin,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(IntegrationAccessUsersResponse{Rows: out})
}

func (h *IntegrationsHandler) GetAccessByUser(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if !requireAdmin(user, w) {
		return
	}

	targetUUID := strings.TrimSpace(r.PathValue("user_uuid"))
	if targetUUID == "" {
		http.Error(w, "user_uuid is required", http.StatusBadRequest)
		return
	}

	var target database.User
	if err := DB.First(&target, "uuid = ?", targetUUID).Error; err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	assignments, err := database.ListIntegrationAccessByUserID(DB, target.ID)
	if err != nil {
		http.Error(w, "failed to list assignments", http.StatusInternalServerError)
		return
	}
	assignedSet := map[string]struct{}{}
	for _, row := range assignments {
		assignedSet[row.IntegrationName] = struct{}{}
	}

	defs := backendintegrations.List()
	rows := make([]IntegrationAccessRow, 0, len(defs))
	for _, def := range defs {
		_, assigned := assignedSet[def.Name]
		visible, visErr := backendintegrations.IsVisibleToUser(DB, def, &target)
		if visErr != nil {
			http.Error(w, "failed to resolve access", http.StatusInternalServerError)
			return
		}
		rows = append(rows, IntegrationAccessRow{
			IntegrationName: def.Name,
			AdminOnly:       def.AdminOnly,
			UserAccessible:  def.UserAccessible,
			Assigned:        assigned,
			Visible:         visible,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].IntegrationName < rows[j].IntegrationName
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(IntegrationAccessByUserResponse{
		User: IntegrationAccessUserRow{
			UUID:    target.UUID,
			Name:    target.Name,
			Email:   target.Email,
			IsAdmin: target.IsAdmin,
		},
		Rows: rows,
	})
}

func (h *IntegrationsHandler) GrantAccess(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if !requireAdmin(user, w) {
		return
	}

	targetUUID := strings.TrimSpace(r.PathValue("user_uuid"))
	integrationName := strings.ToLower(strings.TrimSpace(r.PathValue("integration_name")))
	if targetUUID == "" || integrationName == "" {
		http.Error(w, "user_uuid and integration_name are required", http.StatusBadRequest)
		return
	}

	var target database.User
	if err := DB.First(&target, "uuid = ?", targetUUID).Error; err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	def, found := backendintegrations.Get(integrationName)
	if !found {
		http.Error(w, "integration not found", http.StatusNotFound)
		return
	}
	if def.AdminOnly && !target.IsAdmin {
		http.Error(w, "admin_only integrations can only be assigned to admin users", http.StatusBadRequest)
		return
	}
	if err := database.EnsureIntegrationAccess(DB, target.ID, integrationName); err != nil {
		http.Error(w, "failed to grant integration access", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *IntegrationsHandler) RevokeAccess(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if !requireAdmin(user, w) {
		return
	}

	targetUUID := strings.TrimSpace(r.PathValue("user_uuid"))
	integrationName := strings.ToLower(strings.TrimSpace(r.PathValue("integration_name")))
	if targetUUID == "" || integrationName == "" {
		http.Error(w, "user_uuid and integration_name are required", http.StatusBadRequest)
		return
	}

	var target database.User
	if err := DB.First(&target, "uuid = ?", targetUUID).Error; err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	if _, found := backendintegrations.Get(integrationName); !found {
		http.Error(w, "integration not found", http.StatusNotFound)
		return
	}
	if err := database.RevokeIntegrationAccess(DB, target.ID, integrationName); err != nil {
		http.Error(w, "failed to revoke integration access", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
