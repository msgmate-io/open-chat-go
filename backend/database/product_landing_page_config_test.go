package database

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func setupProductLandingPageConfigDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := SetupTestDatabase(t, DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "product_landing_pages_test.db"),
		ResetDB:  true,
	})
	if db == nil {
		t.Fatal("failed to set up test database")
	}
	return db
}

func TestSeedProductLandingPageConfigsCreatesAllDefaults(t *testing.T) {
	db := setupProductLandingPageConfigDB(t)

	if err := SeedProductLandingPageConfigs(db); err != nil {
		t.Fatalf("seed product landing pages: %v", err)
	}

	rows, err := ListProductLandingPageConfigs(db, ProductLandingPageIntegrationID)
	if err != nil {
		t.Fatalf("list product landing pages: %v", err)
	}
	if len(rows) != len(ProductLandingPageDefaults) {
		t.Fatalf("expected %d seeded pages, got %d", len(ProductLandingPageDefaults), len(rows))
	}
	for i, def := range ProductLandingPageDefaults {
		if rows[i].PageSlug != def.PageSlug {
			t.Fatalf("row %d: expected slug %q, got %q", i, def.PageSlug, rows[i].PageSlug)
		}
		if !rows[i].Enabled {
			t.Fatalf("page %q should be enabled by default", rows[i].PageSlug)
		}
		if rows[i].Title == "" || rows[i].NavLabel == "" {
			t.Fatalf("page %q should have title and nav label seeded", rows[i].PageSlug)
		}
	}
}

func TestSeedProductLandingPageConfigsIsIdempotentAndKeepsEdits(t *testing.T) {
	db := setupProductLandingPageConfigDB(t)

	if err := SeedProductLandingPageConfigs(db); err != nil {
		t.Fatalf("seed product landing pages: %v", err)
	}

	disabled := false
	title := "FAQ, edited by ops"
	if _, err := UpdateProductLandingPageConfig(db, ProductLandingPageIntegrationID, "faq_page", ProductLandingPageConfigUpdate{
		Enabled:   &disabled,
		Title:     &title,
		UpdatedBy: "ops@example.com",
	}); err != nil {
		t.Fatalf("update faq page: %v", err)
	}

	// A second seed pass must not resurrect defaults nor duplicate rows.
	if err := SeedProductLandingPageConfigs(db); err != nil {
		t.Fatalf("re-seed product landing pages: %v", err)
	}

	rows, err := ListProductLandingPageConfigs(db, ProductLandingPageIntegrationID)
	if err != nil {
		t.Fatalf("list product landing pages: %v", err)
	}
	if len(rows) != len(ProductLandingPageDefaults) {
		t.Fatalf("expected %d pages after re-seed, got %d", len(ProductLandingPageDefaults), len(rows))
	}
	faq, err := GetProductLandingPageConfig(db, ProductLandingPageIntegrationID, "faq_page")
	if err != nil {
		t.Fatalf("get faq page: %v", err)
	}
	if faq.Enabled {
		t.Fatal("re-seed re-enabled a page the operator disabled")
	}
	if faq.Title != title {
		t.Fatalf("re-seed overwrote the operator title: got %q", faq.Title)
	}
	if faq.UpdatedBy != "ops@example.com" {
		t.Fatalf("expected updated_by to be recorded, got %q", faq.UpdatedBy)
	}
}

func TestNormalizeProductLandingPageSlugEnforcesAllowList(t *testing.T) {
	slug, err := NormalizeProductLandingPageSlug("  FAQ_PAGE ")
	if err != nil {
		t.Fatalf("expected faq_page to be allow-listed: %v", err)
	}
	if slug != "faq_page" {
		t.Fatalf("expected normalized slug faq_page, got %q", slug)
	}

	for _, bad := range []string{"", "   ", "admin_settings", "../faq_page", "unknown_page"} {
		if _, err := NormalizeProductLandingPageSlug(bad); !errors.Is(err, ErrUnknownProductLandingPageSlug) {
			t.Fatalf("expected %q to be rejected with ErrUnknownProductLandingPageSlug, got %v", bad, err)
		}
	}
}

func TestUpdateProductLandingPageConfigValidatesInput(t *testing.T) {
	db := setupProductLandingPageConfigDB(t)

	longTitle := strings.Repeat("x", MaxProductLandingPageTitleLength+1)
	longLabel := strings.Repeat("y", MaxProductLandingPageNavLabelLength+1)
	badOrder := MaxProductLandingPageNavOrder + 1
	negativeOrder := -1
	brokenJSON := json.RawMessage("{not json")
	oversized := json.RawMessage(`"` + strings.Repeat("z", MaxProductLandingPageStaticDataBytes) + `"`)

	cases := []struct {
		name   string
		slug   string
		update ProductLandingPageConfigUpdate
	}{
		{name: "unknown slug", slug: "not_a_page", update: ProductLandingPageConfigUpdate{}},
		{name: "title too long", slug: "faq_page", update: ProductLandingPageConfigUpdate{Title: &longTitle}},
		{name: "nav label too long", slug: "faq_page", update: ProductLandingPageConfigUpdate{NavLabel: &longLabel}},
		{name: "nav order too large", slug: "faq_page", update: ProductLandingPageConfigUpdate{NavOrder: &badOrder}},
		{name: "nav order negative", slug: "faq_page", update: ProductLandingPageConfigUpdate{NavOrder: &negativeOrder}},
		{name: "invalid json", slug: "faq_page", update: ProductLandingPageConfigUpdate{StaticData: &brokenJSON}},
		{name: "oversized static data", slug: "faq_page", update: ProductLandingPageConfigUpdate{StaticData: &oversized}},
	}

	// The rejected updates must not have created or changed any row.
	before, err := ListProductLandingPageConfigs(db, ProductLandingPageIntegrationID)
	if err != nil {
		t.Fatalf("list product landing pages: %v", err)
	}
	for _, tc := range cases {
		if _, err := UpdateProductLandingPageConfig(db, ProductLandingPageIntegrationID, tc.slug, tc.update); err == nil {
			t.Fatalf("expected %s to be rejected", tc.name)
		}
	}
	after, err := ListProductLandingPageConfigs(db, ProductLandingPageIntegrationID)
	if err != nil {
		t.Fatalf("list product landing pages: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("expected row count to stay %d after rejected updates, got %d", len(before), len(after))
	}
	if _, err := GetProductLandingPageConfig(db, ProductLandingPageIntegrationID, "faq_page"); err != nil {
		t.Fatalf("faq_page row should still exist after rejected updates: %v", err)
	}
}

func TestUpdateProductLandingPageConfigPatchesSingleFields(t *testing.T) {
	db := setupProductLandingPageConfigDB(t)

	disabled := false
	if _, err := UpdateProductLandingPageConfig(db, ProductLandingPageIntegrationID, ProductLandingPageOverviewSlug, ProductLandingPageConfigUpdate{
		Enabled: &disabled,
	}); err != nil {
		t.Fatalf("disable overview page: %v", err)
	}

	row, err := GetProductLandingPageConfig(db, ProductLandingPageIntegrationID, ProductLandingPageOverviewSlug)
	if err != nil {
		t.Fatalf("get overview page: %v", err)
	}
	if row.Enabled {
		t.Fatal("expected overview page to be disabled")
	}

	// A second partial update that only touches static_data keeps the toggle.
	staticData := json.RawMessage(`{"faq":[{"q":"Q","a":"A"}]}`)
	navOrder := 5
	row, err = UpdateProductLandingPageConfig(db, ProductLandingPageIntegrationID, ProductLandingPageOverviewSlug, ProductLandingPageConfigUpdate{
		StaticData: &staticData,
		NavOrder:   &navOrder,
	})
	if err != nil {
		t.Fatalf("patch overview static data: %v", err)
	}
	if row.Enabled {
		t.Fatal("static_data patch flipped the enabled flag back on")
	}
	if row.NavOrder != navOrder {
		t.Fatalf("expected nav_order %d, got %d", navOrder, row.NavOrder)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(row.StaticData, &decoded); err != nil {
		t.Fatalf("stored static_data is not valid JSON: %v", err)
	}
	if _, ok := decoded["faq"]; !ok {
		t.Fatalf("static_data lost its content: %s", string(row.StaticData))
	}
}

func TestListProductLandingPageConfigsOrdersByNavOrder(t *testing.T) {
	db := setupProductLandingPageConfigDB(t)

	if err := SeedProductLandingPageConfigs(db); err != nil {
		t.Fatalf("seed product landing pages: %v", err)
	}

	navOrder := 1
	if _, err := UpdateProductLandingPageConfig(db, ProductLandingPageIntegrationID, "faq_page", ProductLandingPageConfigUpdate{
		NavOrder: &navOrder,
	}); err != nil {
		t.Fatalf("reorder faq page: %v", err)
	}

	rows, err := ListProductLandingPageConfigs(db, ProductLandingPageIntegrationID)
	if err != nil {
		t.Fatalf("list product landing pages: %v", err)
	}
	if len(rows) == 0 || rows[0].PageSlug != "faq_page" {
		t.Fatalf("expected faq_page first after reorder, got %+v", rows)
	}
}
