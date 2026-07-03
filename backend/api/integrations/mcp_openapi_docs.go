package integrations

type MCPServerUpsertRequestDoc struct {
	Name    string                 `json:"name"`
	Config  map[string]interface{} `json:"config"`
	Enabled *bool                  `json:"enabled,omitempty"`
}

type MCPAuthCompleteRequestDoc struct {
	BearerToken string `json:"bearer_token,omitempty"`
	Code        string `json:"code,omitempty"`
	State       string `json:"state,omitempty"`
}

type MCPServerRowDoc struct {
	Name          string                 `json:"name"`
	Config        map[string]interface{} `json:"config"`
	Enabled       bool                   `json:"enabled"`
	HasAuthData   bool                   `json:"has_auth_data"`
	AuthMode      string                 `json:"auth_mode"`
	AuthConnected bool                   `json:"auth_connected"`
	AuthPending   bool                   `json:"auth_pending"`
	CreatedAtUnix int64                  `json:"created_at_unix"`
	UpdatedAtUnix int64                  `json:"updated_at_unix"`
}

type MCPServerListResponseDoc struct {
	Rows []MCPServerRowDoc `json:"rows"`
}

// mcpListServersDocs documents the MCP list endpoint.
//
//	@Summary      List MCP servers
//	@Description  List owner-scoped MCP servers registered via the MCP integration.
//	@Tags         integrations
//	@Produce      json
//	@Security     SessionAuth
//	@Success      200 {object} MCPServerListResponseDoc
//	@Router       /api/v1/integrations/mcp/servers [get]
func mcpListServersDocs() {}

// mcpCreateServerDocs documents the MCP create endpoint.
//
//	@Summary      Create MCP server
//	@Description  Create an owner-scoped MCP server registration.
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Param        body body MCPServerUpsertRequestDoc true "MCP server config"
//	@Success      200 {object} map[string]interface{}
//	@Router       /api/v1/integrations/mcp/servers [post]
func mcpCreateServerDocs() {}

// mcpUpdateServerDocs documents the MCP update endpoint.
//
//	@Summary      Update MCP server
//	@Description  Update an owner-scoped MCP server registration.
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Param        server_name path string true "Server name"
//	@Param        body body MCPServerUpsertRequestDoc true "Updated MCP server config"
//	@Success      200 {object} map[string]interface{}
//	@Router       /api/v1/integrations/mcp/servers/{server_name} [put]
func mcpUpdateServerDocs() {}

// mcpDeleteServerDocs documents the MCP delete endpoint.
//
//	@Summary      Delete MCP server
//	@Description  Delete an owner-scoped MCP server registration.
//	@Tags         integrations
//	@Produce      json
//	@Security     SessionAuth
//	@Param        server_name path string true "Server name"
//	@Success      200 {object} map[string]interface{}
//	@Router       /api/v1/integrations/mcp/servers/{server_name} [delete]
func mcpDeleteServerDocs() {}

// mcpDiscoverServerDocs documents the MCP discover endpoint.
//
//	@Summary      Discover MCP server tools
//	@Description  Calls tools/list on a registered MCP server.
//	@Tags         integrations
//	@Produce      json
//	@Security     SessionAuth
//	@Param        server_name path string true "Server name"
//	@Success      200 {object} map[string]interface{}
//	@Router       /api/v1/integrations/mcp/servers/{server_name}/discover [post]
func mcpDiscoverServerDocs() {}

// mcpAuthStatusDocs documents MCP auth status endpoint.
//
//	@Summary      Get MCP server auth status
//	@Description  Returns current authentication status for a registered MCP server.
//	@Tags         integrations
//	@Produce      json
//	@Security     SessionAuth
//	@Param        server_name path string true "Server name"
//	@Success      200 {object} map[string]interface{}
//	@Router       /api/v1/integrations/mcp/servers/{server_name}/auth/status [get]
func mcpAuthStatusDocs() {}

// mcpAuthStartDocs documents MCP auth start endpoint.
//
//	@Summary      Start MCP server auth flow
//	@Description  Starts authentication flow for a registered MCP server.
//	@Tags         integrations
//	@Produce      json
//	@Security     SessionAuth
//	@Param        server_name path string true "Server name"
//	@Success      200 {object} map[string]interface{}
//	@Router       /api/v1/integrations/mcp/servers/{server_name}/auth/start [post]
func mcpAuthStartDocs() {}

// mcpAuthCallbackDocs documents MCP OAuth callback endpoint.
//
//	@Summary      Complete MCP OAuth callback
//	@Description  Completes OAuth via provider redirect query params and redirects to integration UI.
//	@Tags         integrations
//	@Produce      json
//	@Security     SessionAuth
//	@Success      302 {string} string "redirect"
//	@Router       /api/v1/integrations/mcp/auth/callback [get]
func mcpAuthCallbackDocs() {}

// mcpAuthCompleteDocs documents MCP auth complete endpoint.
//
//	@Summary      Complete MCP server auth flow
//	@Description  Completes authentication flow and stores auth_data server-side.
//	@Tags         integrations
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Param        server_name path string true "Server name"
//	@Param        body body MCPAuthCompleteRequestDoc true "Auth completion payload"
//	@Success      200 {object} map[string]interface{}
//	@Router       /api/v1/integrations/mcp/servers/{server_name}/auth/complete [post]
func mcpAuthCompleteDocs() {}

// mcpAuthClearDocs documents MCP auth clear endpoint.
//
//	@Summary      Clear MCP server auth data
//	@Description  Clears stored auth_data and pending auth session for a server.
//	@Tags         integrations
//	@Produce      json
//	@Security     SessionAuth
//	@Param        server_name path string true "Server name"
//	@Success      200 {object} map[string]interface{}
//	@Router       /api/v1/integrations/mcp/servers/{server_name}/auth/clear [post]
func mcpAuthClearDocs() {}
