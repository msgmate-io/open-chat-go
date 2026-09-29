package admin

import (
	"backend/integrationsettings"
	"backend/runtimecfg"
	"backend/server/util"
	"backend/servicecontrol"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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

type rawConfigResponse struct {
	Deployment integrationsettings.DeploymentInfo `json:"deployment"`
	Format     string                             `json:"format"`
	YAML       string                             `json:"yaml"`
	Redacted   bool                               `json:"redacted"`
	InMemory   bool                               `json:"in_memory"`
}

type rawConfigValidateRequest struct {
	YAML string `json:"yaml"`
}

type rawConfigValidateResponse struct {
	Valid  bool     `json:"valid"`
	Errors []string `json:"errors"`
}

type rawConfigSaveRequest struct {
	YAML string `json:"yaml"`
}

type rawConfigSaveResponse struct {
	Deployment      integrationsettings.DeploymentInfo `json:"deployment"`
	Persisted       bool                               `json:"persisted"`
	PersistError    string                             `json:"persist_error,omitempty"`
	RestartRequired bool                               `json:"restart_required"`
	RemotePersisted bool                               `json:"remote_persisted"`
	RemoteTarget    string                             `json:"remote_target,omitempty"`
	RemoteError     string                             `json:"remote_error,omitempty"`
}

type rawConfigDownloadRequest struct {
	Password string `json:"password"`
}

// GetRawIntegrationSettings returns the active config document as YAML with
// sensitive values masked.
//
//	@Summary      Get raw integration settings
//	@Description  Returns the active config document rendered as YAML with sensitive values masked.
//	@Tags         admin
//	@Produce      json
//	@Success      200 {object} rawConfigResponse
//	@Router       /api/v1/admin/integration-settings/raw [get]
func GetRawIntegrationSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireAdmin(w, r) {
		return
	}

	deployment := integrationsettings.BuildDeploymentInfo()
	defs := settingsDefinitions()
	values := runtimecfg.GetAll()

	root, format, err := integrationsettings.LoadConfigDocument(runtimecfg.GetConfigSource())
	inMemory := false
	if err != nil {
		if !errors.Is(err, integrationsettings.ErrNotPersistable) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		root = integrationsettings.BuildRuntimeConfigDocument(defs, values)
		format = deployment.ConfigFormat
		inMemory = true
	}

	redacted := integrationsettings.RedactConfig(root, values, defs)
	rendered, err := integrationsettings.RenderConfigYAML(redacted)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, rawConfigResponse{
		Deployment: deployment,
		Format:     format,
		YAML:       string(rendered),
		Redacted:   true,
		InMemory:   inMemory,
	})
}

// ValidateRawIntegrationSettings validates a raw config document without
// persisting it.
//
//	@Summary      Validate raw integration settings
//	@Description  Validates a raw config document and returns the list of problems without persisting.
//	@Tags         admin
//	@Accept       json
//	@Produce      json
//	@Success      200 {object} rawConfigValidateResponse
//	@Router       /api/v1/admin/integration-settings/raw/validate [post]
func ValidateRawIntegrationSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireAdmin(w, r) {
		return
	}

	payload := rawConfigValidateRequest{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	root, err := integrationsettings.ParseConfigDocument(payload.YAML)
	if err != nil {
		writeJSON(w, http.StatusOK, rawConfigValidateResponse{Valid: false, Errors: []string{err.Error()}})
		return
	}
	errs := integrationsettings.ValidateConfigDocument(root, settingsDefinitions())
	writeJSON(w, http.StatusOK, rawConfigValidateResponse{Valid: len(errs) == 0, Errors: errs})
}

// SaveRawIntegrationSettings validates and persists a raw config document,
// preserving values that were sent back as the redaction sentinel.
//
//	@Summary      Save raw integration settings
//	@Description  Validates and persists a raw config document, restoring redacted values from disk.
//	@Tags         admin
//	@Accept       json
//	@Produce      json
//	@Success      200 {object} rawConfigSaveResponse
//	@Router       /api/v1/admin/integration-settings/raw [put]
func SaveRawIntegrationSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !requireAdmin(w, r) {
		return
	}

	payload := rawConfigSaveRequest{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	root, err := integrationsettings.ParseConfigDocument(payload.YAML)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	defs := settingsDefinitions()
	if errs := integrationsettings.ValidateConfigDocument(root, defs); len(errs) > 0 {
		writeJSON(w, http.StatusBadRequest, rawConfigValidateResponse{Valid: false, Errors: errs})
		return
	}

	source := runtimecfg.GetConfigSource()
	persisted := false
	persistError := ""
	remotePersisted := false
	remoteTarget := ""
	remoteError := ""

	if original, sourceFormat, err := integrationsettings.LoadConfigDocument(source); err != nil {
		persistError = err.Error()
	} else {
		restored := integrationsettings.RestoreRedactedConfig(original, root, runtimecfg.GetAll(), defs)
		if _, err := integrationsettings.SaveConfigDocument(source, restored); err != nil {
			persistError = err.Error()
		} else {
			persisted = true
		}

		// Mirror the config into the deployment-host kubernetes Secret when one
		// is configured (decoupling the config from the Helm release). A
		// read-only config mount makes the local write above fail, so a
		// successful remote persist still counts as persisted.
		format := sourceFormat
		if encoded, encErr := integrationsettings.EncodeConfigDocument(restored, format); encErr != nil {
			remoteError = encErr.Error()
		} else if target, configured, remoteErr := integrationsettings.PersistRemoteConfig(encoded); remoteErr != nil {
			if errors.Is(remoteErr, integrationsettings.ErrNoRemoteConfigTarget) {
				// No deployment host configured; nothing to do.
			} else {
				remoteError = remoteErr.Error()
			}
		} else if configured {
			remotePersisted = true
			remoteTarget = target
			persisted = true
		}
	}

	writeJSON(w, http.StatusOK, rawConfigSaveResponse{
		Deployment:      integrationsettings.BuildDeploymentInfo(),
		Persisted:       persisted,
		PersistError:    persistError,
		RestartRequired: true,
		RemotePersisted: remotePersisted,
		RemoteTarget:    remoteTarget,
		RemoteError:     remoteError,
	})
}

// DownloadRawIntegrationSettings streams the raw config file after the admin
// re-authenticates with their password.
//
//	@Summary      Download raw integration settings
//	@Description  Streams the raw config file after password confirmation.
//	@Tags         admin
//	@Accept       json
//	@Produce      application/octet-stream
//	@Success      200 {file} file
//	@Router       /api/v1/admin/integration-settings/raw/download [post]
func DownloadRawIntegrationSettings(w http.ResponseWriter, r *http.Request) {
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

	payload := rawConfigDownloadRequest{}
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

	source := strings.TrimSpace(runtimecfg.GetConfigSource())
	if source == "" || strings.HasPrefix(source, "inline") {
		http.Error(w, "no persistable config file", http.StatusConflict)
		return
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		http.Error(w, "no persistable config file", http.StatusConflict)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(source)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}
