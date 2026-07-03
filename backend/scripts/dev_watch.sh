#!/usr/bin/env bash

set -euo pipefail

exec /dev_bin/CompileDaemon \
  -polling \
  -build="bash ./scripts/dev_rebuild.sh" \
  -command="./.devbin/backend server --fpx http://frontend:3000 --host 0.0.0.0 --port 1984" \
  -directory=/backend \
  -directory=/clients/go_tool_interface \
  -directory=/clients/go_integration_interface \
  -directory=/clients/integrations \
  -include="*.go" \
  -include="*.c" \
  -include="go.mod" \
  -include="go.sum" \
  -include="tooldeps.json" \
  -include="integrationdeps.json" \
  -exclude-dir=/backend/docs \
  -exclude-dir=/backend/.devbin \
  -exclude-dir=/clients/integrations/mcp_integration/frontend_assets \
  -exclude-dir=/clients/integrations/rest_api_tool_integration/frontend_assets \
  -exclude="swagger.json" \
  -exclude="imports_gen.go" \
  -exclude="*.db" \
  -exclude="*.db-*"
