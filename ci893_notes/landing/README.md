# Open-Chat product landing pages

The **product landing pages** integration owns the public, in-integration React
landing pages that describe what Open-Chat can be used for, together with a
per-page config model that lets operators enable/disable a page and edit its
pre-stored static data.

## Pages

The React code lives in this repository under `frontend/pages` and is linked into
the frontend aggregator (`frontend/pages/<route>`) by
`openchat-integrations frontend`; `integration.frontend.json` declares the
mounts and the prerendered assets embedded by the Go registration.

Current pages:

| Route | Purpose |
| --- | --- |
| `/overview` | Hub that links the individual landing pages (always served). |
| `/company_ai_integration` | Open-Chat as a self-hosted company AI layer. |
| `/framework` | Embed Open-Chat in your own app through a typed API and generated clients. |
| `/evaluations_and_benchmarks` | Evaluations, benchmarks and release gates for AI workflows. |
| `/faq_page` | Frequently asked questions about hosting, data, licensing and pricing. |

Every page shares the same top navigation bar (`frontend/components/LandingLayout.tsx`)
and the provider logo wall (`frontend/components/ProviderLogos.tsx`, extracted from
the ci#858 pitch deck).

The per-page config (`enabled`, `title`, nav label/order and free-form
`static_data`) and its admin API are owned by the core
(`backend/database/product_landing_page_config.go`,
`backend/api/integrations/ProductLandingPages.go`) and seeded for the known page
slugs.

## Building

This module is built as part of the Open-Chat integration workspace; page
prerendering and asset embedding are handled by the integration manager
(`openchat-integrations prepare` / `export`).
