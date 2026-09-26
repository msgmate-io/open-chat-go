// Package productlandingpages registers the "product landing pages" integration:
// it owns the public, in-integration React landing pages (e.g. /overview)
// that describe what Open-Chat can be used for, and the per-page config model
// that lets operators enable/disable a page and edit its pre-stored static data.
//
// The page React code lives in this integration (under frontend/pages) and is
// linked into the frontend aggregator by `openchat-integrations frontend`, so it
// stays out of the public open-chat-go repository. The per-page config model and
// its admin API are owned by the core (backend/database/product_landing_page_config.go).
package productlandingpages

import (
	"embed"
	"io/fs"
	"strings"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

// IntegrationName is the manifest/profile id of this integration.
const IntegrationName = "product_landing_pages"

// frontendPages describes the routes this integration renders. The page files
// live under frontend/pages and are mounted at their root route by
// integration.frontend.json. The slugs match the seeded
// product_landing_page_configs rows, and every page is Root because the
// manager mounts each one at a first-party root route (e.g. /faq_page) rather
// than under the default /integrations/<name> prefix.
var frontendPages = []integrationinterface.FrontendPage{
	{
		Route:       "/overview",
		Public:      true,
		Root:        true,
		Description: "What can Open-Chat be used for — hub linking the landing pages.",
		AssetPath:   "overview/index.html",
	},
	{
		Route:       "/company_ai_integration",
		Public:      true,
		Root:        true,
		Description: "Open-Chat as a self-hosted company AI layer.",
		AssetPath:   "company_ai_integration/index.html",
	},
	{
		Route:       "/framework",
		Public:      true,
		Root:        true,
		Description: "Embed Open-Chat in your own app through a typed API and generated clients.",
		AssetPath:   "framework/index.html",
	},
	{
		Route:       "/evaluations_and_benchmarks",
		Public:      true,
		Root:        true,
		Description: "Evaluations, benchmarks and release gates for AI workflows.",
		AssetPath:   "evaluations_and_benchmarks/index.html",
	},
	{
		Route:       "/faq_page",
		Public:      true,
		Root:        true,
		Description: "Frequently asked questions about hosting, data, licensing and pricing.",
		AssetPath:   "faq_page/index.html",
	},
}

//go:embed frontend_assets
var frontendAssets embed.FS

//go:embed README.md
var readmeMarkdown string

func init() {
	integrationinterface.MustRegister(integrationinterface.Definition{
		Name:           IntegrationName,
		AdminOnly:      false,
		UserAccessible: true,
		ReadmeMarkdown: strings.TrimSpace(readmeMarkdown),
		FrontendPages:  append([]integrationinterface.FrontendPage(nil), frontendPages...),
		FrontendAssets: mustSubFS(frontendAssets, "frontend_assets"),
		Labels:         []string{"core", "full"},
	})
}

func mustSubFS(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
