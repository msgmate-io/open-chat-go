package admin

import (
	"backend/integrationsettings"
	"backend/runtimecfg"
	"backend/server/util"
	"backend/servicecontrol"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
	"golang.org/x/crypto/bcrypt"
)

// settingsDefinitions returns the registered integration definitions. It uses
// the interface registry directly (rather than backend/integrations) to avoid
// an import cycle with integration packages that import backend/api/user.
func settingsDefinitions() []integrationinterface.Definition {
	defs := integrationinterface.List()
	// Synthetic group exposing the backend core's own configuration (effective
	// env values and bootstrap specs) for editing in the same UI.
	defs = append(defs, integrationsettings.CoreDefinition())
	return defs
}

type revealIntegrationSettingsRequest struct {
	Password string `json:"password"`
}

type saveIntegrationSettingsRequest struct {
	Values map[string]*string `json:"values"`
}

type saveIntegrationSettingsResponse struct {
	Deployment      integrationsettings.DeploymentInfo      `json:"deployment"`
	Integration     integrationsettings.IntegrationSnapshot `json:"integration"`
	RestartRequired bool                                    `json:"restart_required"`
	Persisted       bool                                    `json:"persisted"`
	PersistError    string                                  `json:"persist_error,omitempty"`
}

type restartServerResponse struct {
	Status     string                             `json:"status"`
	Deployment integrationsettings.DeploymentInfo `json:"deployment"`
	Message    string                             `json:"message,omitempty"`
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	_, user, err := util.GetDBAndUser(r)
	if err != nil || user == nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return false
	}
	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// ListIntegrationSettings returns deployment capabilities and masked settings
// for every integration that declares runtime env vars.
//
//	@Summary      List integration settings
//	@Description  Returns deployment capabilities and masked settings for every integration that declares runtime env vars.
//	@Tags         admin
//	@Produce      json
//	@Success      200 {object} integrationsettings.IntegrationSettingsListResponse
//	@Router       /api/v1/admin/integration-settings [get]
func ListIntegrationSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, integrationsettings.IntegrationSettingsListResponse{
		Deployment:   integrationsettings.BuildDeploymentInfo(),
		Integrations: integrationsettings.ListSnapshots(settingsDefinitions(), runtimecfg.GetAll(), false),
	})
}

// GetIntegrationSettings returns masked settings for one integration.
//
//	@Summary      Get integration settings
//	@Description  Returns masked settings and field descriptors for one integration.
//	@Tags         admin
//	@Produce      json
//	@Success      200 {object} integrationsettings.IntegrationSettingsDetailResponse
//	@Router       /api/v1/admin/integration-settings/{integration_name} [get]
func GetIntegrationSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	def, ok := integrationsettings.FindDefinition(settingsDefinitions(), r.PathValue("integration_name"))
	if !ok || len(def.RuntimeEnvVars) == 0 {
		http.Error(w, "integration not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, integrationsettings.IntegrationSettingsDetailResponse{
		Deployment:  integrationsettings.BuildDeploymentInfo(),
		Integration: integrationsettings.BuildIntegrationSnapshot(def, runtimecfg.GetAll(), false),
	})
}

// SaveIntegrationSettings validates and applies runtime values for one
// integration, then attempts to persist them to the active config file.
//
//	@Summary      Save integration settings
//	@Description  Validates, applies, and (when possible) persists runtime values for one integration.
//	@Tags         admin
//	@Accept       json
//	@Produce      json
//	@Success      200 {object} saveIntegrationSettingsResponse
//	@Router       /api/v1/admin/integration-settings/{integration_name} [put]
func SaveIntegrationSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireAdmin(w, r) {
		return
	}
	def, ok := integrationsettings.FindDefinition(settingsDefinitions(), r.PathValue("integration_name"))
	if !ok || len(def.RuntimeEnvVars) == 0 {
		http.Error(w, "integration not found", http.StatusNotFound)
		return
	}

	payload := saveIntegrationSettingsRequest{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if payload.Values == nil {
		http.Error(w, "values is required", http.StatusBadRequest)
		return
	}

	normalized, err := integrationsettings.ValidateValues(def, payload.Values)
	if err != nil {
		var validationErr *integrationsettings.ValidationError
		if errors.As(err, &validationErr) {
			http.Error(w, validationErr.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	integrationsettings.ApplyValues(def, normalized)

	persisted := false
	persistError := ""
	if err := integrationsettings.PersistValues(def, normalized); err != nil {
		persistError = err.Error()
	} else {
		persisted = true
	}

	writeJSON(w, http.StatusOK, saveIntegrationSettingsResponse{
		Deployment:      integrationsettings.BuildDeploymentInfo(),
		Integration:     integrationsettings.BuildIntegrationSnapshot(def, runtimecfg.GetAll(), false),
		RestartRequired: true,
		Persisted:       persisted,
		PersistError:    persistError,
	})
}

// RevealIntegrationSettings returns unmasked values for one integration after
// the admin re-authenticates with their password.
//
//	@Summary      Reveal integration settings
//	@Description  Reveals unmasked runtime values for one integration after password confirmation.
//	@Tags         admin
//	@Accept       json
//	@Produce      json
//	@Success      200 {object} integrationsettings.IntegrationSettingsDetailResponse
//	@Router       /api/v1/admin/integration-settings/{integration_name}/reveal [post]
func RevealIntegrationSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, user, err := util.GetDBAndUser(r)
	if err != nil || user == nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	def, ok := integrationsettings.FindDefinition(settingsDefinitions(), r.PathValue("integration_name"))
	if !ok || len(def.RuntimeEnvVars) == 0 {
		http.Error(w, "integration not found", http.StatusNotFound)
		return
	}

	payload := revealIntegrationSettingsRequest{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	payload.Password = strings.TrimSpace(payload.Password)
	if payload.Password == "" {
		http.Error(w, "password is required", http.StatusBadRequest)
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(payload.Password)); err != nil {
		http.Error(w, "invalid password", http.StatusUnauthorized)
		return
	}

	writeJSON(w, http.StatusOK, integrationsettings.IntegrationSettingsDetailResponse{
		Deployment:  integrationsettings.BuildDeploymentInfo(),
		Integration: integrationsettings.BuildIntegrationSnapshot(def, runtimecfg.GetAll(), true),
	})
}

// RestartServer asks the OS service manager to restart the running server so
// persisted environment changes are picked up.
//
//	@Summary      Restart the server
//	@Description  Asks the OS service manager to restart the server when service-managed; otherwise returns 409.
//	@Tags         admin
//	@Produce      json
//	@Success      202 {object} restartServerResponse
//	@Router       /api/v1/admin/integration-settings/restart [post]
func RestartServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireAdmin(w, r) {
		return
	}

	deployment := integrationsettings.BuildDeploymentInfo()
	if !deployment.CanRestart {
		writeJSON(w, http.StatusConflict, restartServerResponse{
			Status:     "unsupported",
			Deployment: deployment,
			Message:    "The server is not managed by an OS service manager. Restart it manually to apply the new settings.",
		})
		return
	}

	go func() {
		time.Sleep(500 * time.Millisecond)
		_ = servicecontrol.Restart()
	}()

	writeJSON(w, http.StatusAccepted, restartServerResponse{
		Status:     "restarting",
		Deployment: deployment,
	})
}
