# syntax=docker/dockerfile:1

ARG GOLANG_VERSION=1.25.10
ARG ALPINE_VERSION=3.20
ARG NODE_VERSION=22

FROM node:${NODE_VERSION}-alpine AS frontend
WORKDIR /frontend
COPY frontend/ ./
RUN npm install
RUN npm run build
RUN ./generate_golang_routes.sh

FROM docker.io/library/golang:${GOLANG_VERSION}-alpine AS basebuilder

ENV GOTOOLCHAIN=auto

WORKDIR /backend

RUN apk add --no-cache gcc musl-dev bash libc6-compat
COPY backend/go.mod ./
COPY backend/go.sum ./
COPY clients/go_tool_interface/go.mod /clients/go_tool_interface/go.mod
COPY clients/go_integration_interface/go.mod /clients/go_integration_interface/go.mod
# BEGIN GENERATED: integration-mod-manifests
COPY clients/integrations/account_management/go.mod /clients/integrations/account_management/go.mod
COPY clients/integrations/account_management/go.sum /clients/integrations/account_management/go.sum
COPY clients/integrations/admin_db_managemnt_integration/go.mod /clients/integrations/admin_db_managemnt_integration/go.mod
COPY clients/integrations/admin_db_managemnt_integration/go.sum /clients/integrations/admin_db_managemnt_integration/go.sum
COPY clients/integrations/mcp_integration/go.mod /clients/integrations/mcp_integration/go.mod
COPY clients/integrations/mcp_integration/go.sum /clients/integrations/mcp_integration/go.sum
COPY clients/integrations/email_integration/go.mod /clients/integrations/email_integration/go.mod
COPY clients/integrations/email_integration/go.sum /clients/integrations/email_integration/go.sum
COPY clients/integrations/rest_api_tool_integration/go.mod /clients/integrations/rest_api_tool_integration/go.mod
COPY clients/integrations/rest_api_tool_integration/go.sum /clients/integrations/rest_api_tool_integration/go.sum
COPY clients/integrations/ssh_integration/go.mod /clients/integrations/ssh_integration/go.mod
COPY clients/integrations/ssh_integration/go.sum /clients/integrations/ssh_integration/go.sum
# END GENERATED: integration-mod-manifests
RUN test -f /clients/go_tool_interface/go.mod
RUN test -f /clients/go_integration_interface/go.mod
RUN test -f /clients/integrations/admin_db_managemnt_integration/go.mod
RUN test -f /clients/integrations/mcp_integration/go.mod
RUN test -f /clients/integrations/rest_api_tool_integration/go.mod
RUN test -f /clients/integrations/ssh_integration/go.mod
RUN CGO_ENABLED=1 go mod download

FROM basebuilder AS builder

COPY clients/go_tool_interface /clients/go_tool_interface
COPY clients/go_integration_interface /clients/go_integration_interface
# BEGIN GENERATED: integration-source-copies
COPY clients/integrations/account_management /clients/integrations/account_management
COPY clients/integrations/admin_db_managemnt_integration /clients/integrations/admin_db_managemnt_integration
COPY clients/integrations/mcp_integration /clients/integrations/mcp_integration
COPY clients/integrations/email_integration /clients/integrations/email_integration
COPY clients/integrations/rest_api_tool_integration /clients/integrations/rest_api_tool_integration
COPY clients/integrations/ssh_integration /clients/integrations/ssh_integration
# END GENERATED: integration-source-copies
COPY backend/ ./
COPY --from=frontend /frontend/routes.json server/routes.json
COPY --from=frontend /frontend/dist/client server/frontend/
# BEGIN GENERATED: integration-frontend-asset-copies
COPY --from=frontend /frontend/dist/client/integrations/account_management/registration-requests/index.html /clients/integrations/account_management/frontend_assets/registration-requests/index.html
COPY --from=frontend /frontend/dist/client/integrations/admin/index.html /clients/integrations/admin_db_managemnt_integration/frontend_assets/index.html
COPY --from=frontend /frontend/dist/client/integrations/admin/integration-access/index.html /clients/integrations/admin_db_managemnt_integration/frontend_assets/integration-access/index.html
COPY --from=frontend /frontend/dist/client/integrations/admin/queues/index.html /clients/integrations/admin_db_managemnt_integration/frontend_assets/queues/index.html
COPY --from=frontend /frontend/dist/client/integrations/admin/{table_name}/{id}/index.html /clients/integrations/admin_db_managemnt_integration/frontend_assets/table_name/id/index.html
COPY --from=frontend /frontend/dist/client/integrations/admin/{table_name}/index.html /clients/integrations/admin_db_managemnt_integration/frontend_assets/table_name/index.html
COPY --from=frontend /frontend/dist/client/integrations/mcp/servers/add/index.html /clients/integrations/mcp_integration/frontend_assets/servers/add/index.html
COPY --from=frontend /frontend/dist/client/integrations/mcp/servers/index.html /clients/integrations/mcp_integration/frontend_assets/servers/index.html
COPY --from=frontend /frontend/dist/client/integrations/emails/create/index.html /clients/integrations/email_integration/frontend_assets/create/index.html
COPY --from=frontend /frontend/dist/client/integrations/emails/dynamic_email_uuid/edit/index.html /clients/integrations/email_integration/frontend_assets/dynamic_email_uuid/edit/index.html
COPY --from=frontend /frontend/dist/client/integrations/emails/dynamic_email_uuid/send/index.html /clients/integrations/email_integration/frontend_assets/dynamic_email_uuid/send/index.html
COPY --from=frontend /frontend/dist/client/integrations/emails/index.html /clients/integrations/email_integration/frontend_assets/index.html
COPY --from=frontend /frontend/dist/client/integrations/emails/send/index.html /clients/integrations/email_integration/frontend_assets/send/index.html
COPY --from=frontend /frontend/dist/client/integrations/rest_api_tool/tools/index.html /clients/integrations/rest_api_tool_integration/frontend_assets/tools/index.html
COPY --from=frontend /frontend/dist/client/integrations/rest_api_tool/tools/tool-uuid-placeholder/index.html /clients/integrations/rest_api_tool_integration/frontend_assets/tools/tool_uuid/index.html
COPY --from=frontend /frontend/dist/client/integrations/ssh/keys/add/index.html /clients/integrations/ssh_integration/frontend_assets/keys/add/index.html
COPY --from=frontend /frontend/dist/client/integrations/ssh/keys/index.html /clients/integrations/ssh_integration/frontend_assets/keys/index.html
COPY --from=frontend /frontend/dist/client/integrations/ssh/keys/key-uuid-placeholder/details/index.html /clients/integrations/ssh_integration/frontend_assets/keys/key_uuid/details/index.html
COPY --from=frontend /frontend/dist/client/integrations/ssh/keys/key-uuid-placeholder/edit/index.html /clients/integrations/ssh_integration/frontend_assets/keys/key_uuid/edit/index.html
COPY --from=frontend /frontend/dist/client/integrations/ssh/servers/add/index.html /clients/integrations/ssh_integration/frontend_assets/servers/add/index.html
COPY --from=frontend /frontend/dist/client/integrations/ssh/servers/index.html /clients/integrations/ssh_integration/frontend_assets/servers/index.html
COPY --from=frontend /frontend/dist/client/integrations/ssh/servers/server-uuid-placeholder/details/index.html /clients/integrations/ssh_integration/frontend_assets/servers/server_uuid/details/index.html
COPY --from=frontend /frontend/dist/client/integrations/ssh/servers/server-uuid-placeholder/edit/index.html /clients/integrations/ssh_integration/frontend_assets/servers/server_uuid/edit/index.html
COPY --from=frontend /frontend/dist/client/integrations/ssh/servers/server-uuid-placeholder/shell/index.html /clients/integrations/ssh_integration/frontend_assets/servers/server_uuid/shell/index.html
# END GENERATED: integration-frontend-asset-copies

ARG MVPAPP_VERSION=dockerbuild
RUN ls -alt
RUN bash full_build.sh --no-frontend

FROM scratch AS prod
COPY --from=builder /backend/backend /backend
COPY --from=builder /backend/little_world_default_bots /backend/little_world_default_bots

FROM docker.io/library/alpine:${ALPINE_VERSION} AS prod-alpine
WORKDIR /backend
COPY --from=builder /backend/backend /usr/local/bin/backend
COPY --from=builder /backend/server/routes.json /backend/routes.json
COPY --from=builder /backend/little_world_default_bots /backend/little_world_default_bots

CMD ["backend", "server"]
