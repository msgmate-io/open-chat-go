package database

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// ProductLandingPageConfig is the monorepo half of the private "product landing
// pages" integration (ci#858): the integration repo owns the page markup and the
// static defaults, while every mutable per-page value (enabled flag, nav labels,
// pre-stored static data) lives here so ops can toggle or patch a page from the
// integration settings UI without a deploy.
//
// One row exists per (integration_id, page_slug); the five product pages are
// seeded with their defaults so a fresh install always has a complete set.
type ProductLandingPageConfig struct {
	Model
	IntegrationId string `json:"integration_id" gorm:"type:varchar(128);index;uniqueIndex:idx_product_landing_page_integration_slug"`
	PageSlug      string `json:"page_slug" gorm:"type:varchar(128);uniqueIndex:idx_product_landing_page_integration_slug"`
	Enabled       bool   `json:"enabled" gorm:"default:true;index"`
	Title         string `json:"title" gorm:"type:varchar(200)"`
	NavLabel      string `json:"nav_label" gorm:"type:varchar(80)"`
	NavOrder      int    `json:"nav_order" gorm:"default:0"`
	// StaticData carries the pre-stored copy, FAQ entries and benchmark tables
	// of a page. It is versionable in git through the integration defaults and
	// patchable at runtime through this row.
	StaticData json.RawMessage `json:"static_data" gorm:"type:jsonb"`
	UpdatedBy  string          `json:"updated_by" gorm:"type:varchar(128)"`
}

// TableName keeps the table name explicit: the migration and the ops queries
// refer to it by name.
func (ProductLandingPageConfig) TableName() string {
	return "product_landing_page_config"
}

const (
	// ProductLandingPageIntegrationID is the integration id every product
	// landing page row belongs to.
	ProductLandingPageIntegrationID = "product_landing_pages"

	// ProductLandingPageOverviewSlug is the entry hub page: disabled pages
	// redirect to it, so it is always served.
	ProductLandingPageOverviewSlug = "product_overview"

	// MaxProductLandingPageStaticDataBytes caps one page's static_data blob.
	// The value is generous for copy plus FAQ/benchmark tables while keeping a
	// single row from bloating the (SQLite-backed) settings reads.
	MaxProductLandingPageStaticDataBytes = 64 * 1024

	// MaxProductLandingPageTitleLength caps a page title.
	MaxProductLandingPageTitleLength = 200

	// MaxProductLandingPageNavLabelLength caps a nav label.
	MaxProductLandingPageNavLabelLength = 80

	// MaxProductLandingPageNavOrder bounds nav_order so a bad client cannot
	// push a page out of the nav by overflowing the sort key.
	MaxProductLandingPageNavOrder = 9999
)

// ErrUnknownProductLandingPageSlug is returned for a page slug that is not part
// of the registered product pages (the allow-list is the contract with the
// integration repo).
var ErrUnknownProductLandingPageSlug = errors.New("unknown product landing page slug")

// ProductLandingPageDefaults is the ordered default set of product pages. The
// order here is the default nav order and the order the seed writes.
var ProductLandingPageDefaults = []ProductLandingPageConfig{
	{
		IntegrationId: ProductLandingPageIntegrationID,
		PageSlug:      "product_overview",
		Enabled:       true,
		Title:         "Product overview",
		NavLabel:      "Overview",
		NavOrder:      10,
	},
	{
		IntegrationId: ProductLandingPageIntegrationID,
		PageSlug:      "company_ai_integration",
		Enabled:       true,
		Title:         "AI integration for your company",
		NavLabel:      "AI integration",
		NavOrder:      20,
	},
	{
		IntegrationId: ProductLandingPageIntegrationID,
		PageSlug:      "in_product_ai_framework",
		Enabled:       true,
		Title:         "In-product AI framework",
		NavLabel:      "AI framework",
		NavOrder:      30,
	},
	{
		IntegrationId: ProductLandingPageIntegrationID,
		PageSlug:      "evaluations_and_benchmarks",
		Enabled:       true,
		Title:         "Evaluations and benchmarks",
		NavLabel:      "Benchmarks",
		NavOrder:      40,
	},
	{
		IntegrationId: ProductLandingPageIntegrationID,
		PageSlug:      "faq_page",
		Enabled:       true,
		Title:         "Frequently asked questions",
		NavLabel:      "FAQ",
		NavOrder:      50,
	},
}

// ProductLandingPageSlugs returns the allow-listed page slugs in default order.
func ProductLandingPageSlugs() []string {
	slugs := make([]string, 0, len(ProductLandingPageDefaults))
	for _, def := range ProductLandingPageDefaults {
		slugs = append(slugs, def.PageSlug)
	}
	return slugs
}

// NormalizeProductLandingPageSlug trims and lowercases a slug and rejects
// anything outside the allow-list, so a request can never create a row for an
// arbitrary route.
func NormalizeProductLandingPageSlug(slug string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(slug))
	if normalized == "" {
		return "", fmt.Errorf("%w: empty slug", ErrUnknownProductLandingPageSlug)
	}
	for _, def := range ProductLandingPageDefaults {
		if def.PageSlug == normalized {
			return normalized, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownProductLandingPageSlug, slug)
}

// ProductLandingPageConfigUpdate is a partial update of one page config. A nil
// field is left untouched, which lets the settings UI patch a single value (for
// example only the enabled toggle) without re-sending the static data.
type ProductLandingPageConfigUpdate struct {
	Enabled    *bool
	Title      *string
	NavLabel   *string
	NavOrder   *int
	StaticData *json.RawMessage
	UpdatedBy  string
}

// ValidateProductLandingPageConfigUpdate checks an update against the
// allow-list, the length caps and the static_data JSON/size cap. It returns the
// normalized slug.
func ValidateProductLandingPageConfigUpdate(slug string, update ProductLandingPageConfigUpdate) (string, error) {
	normalized, err := NormalizeProductLandingPageSlug(slug)
	if err != nil {
		return "", err
	}

	if update.Title != nil && len(strings.TrimSpace(*update.Title)) > MaxProductLandingPageTitleLength {
		return "", fmt.Errorf("title exceeds %d characters", MaxProductLandingPageTitleLength)
	}
	if update.NavLabel != nil && len(strings.TrimSpace(*update.NavLabel)) > MaxProductLandingPageNavLabelLength {
		return "", fmt.Errorf("nav_label exceeds %d characters", MaxProductLandingPageNavLabelLength)
	}
	if update.NavOrder != nil {
		if *update.NavOrder < 0 || *update.NavOrder > MaxProductLandingPageNavOrder {
			return "", fmt.Errorf("nav_order must be between 0 and %d", MaxProductLandingPageNavOrder)
		}
	}
	if update.StaticData != nil {
		trimmed := strings.TrimSpace(string(*update.StaticData))
		if trimmed == "" || trimmed == "null" {
			return "", fmt.Errorf("static_data must be a JSON object or array")
		}
		if !json.Valid(*update.StaticData) {
			return "", fmt.Errorf("static_data must be valid JSON")
		}
		if len(*update.StaticData) > MaxProductLandingPageStaticDataBytes {
			return "", fmt.Errorf("static_data exceeds %d bytes", MaxProductLandingPageStaticDataBytes)
		}
	}

	return normalized, nil
}

// SeedProductLandingPageConfigsMigration seeds the default product landing page
// rows once the table exists. It is a FunctionMigration so it can sit right
// after TableMigration{&ProductLandingPageConfig{}} and re-run harmlessly on
// every boot (FirstOrCreate never overwrites operator edits).
type SeedProductLandingPageConfigsMigration struct{}

func (SeedProductLandingPageConfigsMigration) Migrate(db *gorm.DB) error {
	return SeedProductLandingPageConfigs(db)
}

// SeedProductLandingPageConfigs creates the missing default rows for the five
// product pages. It is idempotent and never overwrites an existing row, so it is
// safe to run on every boot (and from the integration's ensure path) without
// discarding operator edits.
func SeedProductLandingPageConfigs(db *gorm.DB) error {
	if db == nil {
		return errors.New("nil database")
	}
	for _, def := range ProductLandingPageDefaults {
		// The defaults carry the title/nav/order values, so a missing row is
		// created complete (FirstOrCreate inserts the dest struct as-is).
		row := def
		if err := db.Where("integration_id = ? AND page_slug = ?", def.IntegrationId, def.PageSlug).
			FirstOrCreate(&row).Error; err != nil {
			return fmt.Errorf("seed product landing page %q: %w", def.PageSlug, err)
		}
	}
	return nil
}

// ListProductLandingPageConfigs returns every page of an integration ordered by
// nav order (then slug, so equal orders stay stable).
func ListProductLandingPageConfigs(db *gorm.DB, integrationID string) ([]ProductLandingPageConfig, error) {
	if db == nil {
		return nil, errors.New("nil database")
	}
	integrationID = strings.TrimSpace(integrationID)
	if integrationID == "" {
		integrationID = ProductLandingPageIntegrationID
	}

	rows := []ProductLandingPageConfig{}
	if err := db.Where("integration_id = ?", integrationID).
		Order("nav_order asc, page_slug asc").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// GetProductLandingPageConfig reads one page config. A missing row yields
// gorm.ErrRecordNotFound.
func GetProductLandingPageConfig(db *gorm.DB, integrationID, slug string) (*ProductLandingPageConfig, error) {
	if db == nil {
		return nil, errors.New("nil database")
	}
	normalized, err := NormalizeProductLandingPageSlug(slug)
	if err != nil {
		return nil, err
	}
	integrationID = strings.TrimSpace(integrationID)
	if integrationID == "" {
		integrationID = ProductLandingPageIntegrationID
	}

	row := ProductLandingPageConfig{}
	if err := db.Where("integration_id = ? AND page_slug = ?", integrationID, normalized).
		First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// UpdateProductLandingPageConfig applies a validated partial update to one page
// config and returns the stored row. Missing rows are seeded first, so the
// settings UI can patch a page on a database whose seed has not run yet.
func UpdateProductLandingPageConfig(db *gorm.DB, integrationID string, slug string, update ProductLandingPageConfigUpdate) (*ProductLandingPageConfig, error) {
	if db == nil {
		return nil, errors.New("nil database")
	}
	normalized, err := ValidateProductLandingPageConfigUpdate(slug, update)
	if err != nil {
		return nil, err
	}
	integrationID = strings.TrimSpace(integrationID)
	if integrationID == "" {
		integrationID = ProductLandingPageIntegrationID
	}

	row := ProductLandingPageConfig{IntegrationId: integrationID, PageSlug: normalized}
	if err := db.Where("integration_id = ? AND page_slug = ?", integrationID, normalized).
		FirstOrCreate(&row).Error; err != nil {
		return nil, err
	}

	updates := map[string]interface{}{}
	if update.Enabled != nil {
		updates["enabled"] = *update.Enabled
	}
	if update.Title != nil {
		updates["title"] = strings.TrimSpace(*update.Title)
	}
	if update.NavLabel != nil {
		updates["nav_label"] = strings.TrimSpace(*update.NavLabel)
	}
	if update.NavOrder != nil {
		updates["nav_order"] = *update.NavOrder
	}
	if update.StaticData != nil {
		updates["static_data"] = json.RawMessage(*update.StaticData)
	}
	if update.UpdatedBy != "" {
		updates["updated_by"] = update.UpdatedBy
	}

	if len(updates) > 0 {
		if err := db.Model(&ProductLandingPageConfig{}).
			Where("id = ?", row.ID).
			Updates(updates).Error; err != nil {
			return nil, err
		}
	}

	return GetProductLandingPageConfig(db, integrationID, normalized)
}
