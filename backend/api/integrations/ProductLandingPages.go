package integrations

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"gorm.io/gorm"
)

// This file exposes the monorepo half of the private "product landing pages"
// integration (ci#858): the page-config CRUD used by the integration settings
// panel. Reads are available to every authenticated user (the pages are public
// product pages and the frontend renders the nav from them); writes are
// admin-only, matching the other integration settings endpoints.

// ProductLandingPageIntegrationID is the integration id of the product landing
// pages integration; the routes below are mounted under it.
const ProductLandingPageIntegrationID = database.ProductLandingPageIntegrationID

type productLandingPageRow struct {
	IntegrationId string          `json:"integration_id"`
	PageSlug      string          `json:"page_slug"`
	Enabled       bool            `json:"enabled"`
	Title         string          `json:"title"`
	NavLabel      string          `json:"nav_label"`
	NavOrder      int             `json:"nav_order"`
	StaticData    json.RawMessage `json:"static_data"`
	UpdatedBy     string          `json:"updated_by"`
	UpdatedAt     string          `json:"updated_at"`
}

type productLandingPageListResponse struct {
	IntegrationId string                  `json:"integration_id"`
	Slugs         []string                `json:"slugs"`
	Rows          []productLandingPageRow `json:"rows"`
}

type productLandingPageUpdateRequest struct {
	Enabled    *bool            `json:"enabled"`
	Title      *string          `json:"title"`
	NavLabel   *string          `json:"nav_label"`
	NavOrder   *int             `json:"nav_order"`
	StaticData *json.RawMessage `json:"static_data"`
}

func productLandingPageToRow(row database.ProductLandingPageConfig) productLandingPageRow {
	staticData := row.StaticData
	if len(staticData) == 0 {
		staticData = json.RawMessage("{}")
	}
	return productLandingPageRow{
		IntegrationId: row.IntegrationId,
		PageSlug:      row.PageSlug,
		Enabled:       row.Enabled,
		Title:         row.Title,
		NavLabel:      row.NavLabel,
		NavOrder:      row.NavOrder,
		StaticData:    staticData,
		UpdatedBy:     row.UpdatedBy,
		UpdatedAt:     row.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

// ListProductLandingPageConfigs returns the seeded page configs for the product
// landing pages integration, ordered by nav_order.
//
//	@Summary      List product landing page configs
//	@Description  Returns the per-page config (enabled/title/nav/static data) of the product landing pages integration.
//	@Tags         integrations
//	@Produce      json
//	@Success      200 {object} productLandingPageListResponse
//	@Router       /api/v1/integrations/product_landing_pages/pages [get]
func (h *IntegrationsHandler) ListProductLandingPageConfigs(w http.ResponseWriter, r *http.Request) {
	DB, _, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	// Self-heal a database that predates the seed migration: a fresh checkout or
	// a restored dump still gets the complete default set on first read.
	if err := database.SeedProductLandingPageConfigs(DB); err != nil {
		http.Error(w, "Failed to ensure product landing page defaults", http.StatusInternalServerError)
		return
	}

	rows, err := database.ListProductLandingPageConfigs(DB, database.ProductLandingPageIntegrationID)
	if err != nil {
		http.Error(w, "Failed to list product landing page configs", http.StatusInternalServerError)
		return
	}

	out := make([]productLandingPageRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, productLandingPageToRow(row))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(productLandingPageListResponse{
		IntegrationId: database.ProductLandingPageIntegrationID,
		Slugs:         database.ProductLandingPageSlugs(),
		Rows:          out,
	})
}

// GetProductLandingPageConfig returns one page config by slug.
//
//	@Summary      Get a product landing page config
//	@Description  Returns the config of a single product landing page.
//	@Tags         integrations
//	@Produce      json
//	@Success      200 {object} productLandingPageRow
//	@Router       /api/v1/integrations/product_landing_pages/pages/{page_slug} [get]
func (h *IntegrationsHandler) GetProductLandingPageConfig(w http.ResponseWriter, r *http.Request) {
	DB, _, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	slug := strings.TrimSpace(r.PathValue("page_slug"))
	if _, err := database.NormalizeProductLandingPageSlug(slug); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := database.SeedProductLandingPageConfigs(DB); err != nil {
		http.Error(w, "Failed to ensure product landing page defaults", http.StatusInternalServerError)
		return
	}

	row, err := database.GetProductLandingPageConfig(DB, database.ProductLandingPageIntegrationID, slug)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			http.Error(w, "Product landing page not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to read product landing page config", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(productLandingPageToRow(*row))
}

// UpdateProductLandingPageConfig patches one page config (admin only). Omitted
// fields are left untouched; the slug allow-list and the size caps are enforced
// in database.ValidateProductLandingPageConfigUpdate.
//
//	@Summary      Update a product landing page config
//	@Description  Updates enabled/title/nav label/order and the pre-stored static data of one product landing page.
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Success      200 {object} productLandingPageRow
//	@Router       /api/v1/admin/integrations/product_landing_pages/pages/{page_slug} [put]
func (h *IntegrationsHandler) UpdateProductLandingPageConfig(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}
	if !requireAdmin(user, w) {
		return
	}

	slug := strings.TrimSpace(r.PathValue("page_slug"))
	if _, err := database.NormalizeProductLandingPageSlug(slug); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	payload := productLandingPageUpdateRequest{}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	updatedBy := ""
	if user != nil {
		if strings.TrimSpace(user.Email) != "" {
			updatedBy = user.Email
		} else {
			updatedBy = user.UUID
		}
	}

	row, err := database.UpdateProductLandingPageConfig(DB, database.ProductLandingPageIntegrationID, slug, database.ProductLandingPageConfigUpdate{
		Enabled:    payload.Enabled,
		Title:      payload.Title,
		NavLabel:   payload.NavLabel,
		NavOrder:   payload.NavOrder,
		StaticData: payload.StaticData,
		UpdatedBy:  updatedBy,
	})
	if err != nil {
		if errors.Is(err, database.ErrUnknownProductLandingPageSlug) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(productLandingPageToRow(*row))
}
