# MCP (Model Context Protocol) HTTP Streamable Server Implementation

This document describes the HTTP Streamable MCP server implementation that exposes tools to n8n agents dynamically.

## Overview

The MCP server is implemented as an **HTTP Streamable** JSON-RPC 2.0 server that allows bot users to discover and execute tools for specific chat interactions. It follows the Model Context Protocol specification using HTTP Streamable transport with newline-delimited JSON (NDJSON) for real-time bidirectional communication. The server provides the same tools that are available to the n8n instance in Signal bot interactions.

## Authentication and Access

- **Access Control**: Only bot users can access the MCP server
- **Chat-specific**: Each MCP server instance is tied to a specific chat UUID
- **URL Pattern**: `/api/v1/interactions/{chat_uuid}/mcp`

### Bot Users

Bot users are identified by their name matching one of:
- `signal`
- `bot` 
- `msgmate`

## Available Methods

### 1. `tools/list`

Lists all available tools for the chat.

**Request (NDJSON line):**
```json
{"jsonrpc": "2.0", "method": "tools/list", "id": 1}
```

**Response (NDJSON line):**
```json
{"jsonrpc": "2.0", "result": {"tools": [{"name": "signal_send_message", "description": "Send a message via Signal", "inputSchema": {"type": "object", "properties": {"message": {"type": "string", "description": "The message to send"}}, "required": ["message"]}}]}, "id": 1}
```

### 2. `tools/call`

Executes a specific tool with given arguments.

**Request (NDJSON line):**
```json
{"jsonrpc": "2.0", "method": "tools/call", "params": {"name": "signal_send_message", "arguments": {"message": "Hello, World!"}}, "id": 2}
```

**Response (NDJSON line):**
```json
{"jsonrpc": "2.0", "result": {"content": [{"type": "text", "text": "Message sent successfully"}], "isError": false}, "id": 2}
```

## Available Tools

The MCP server exposes the same tools that are provided to n8n instances:

### Base Tools (All Chats)
- `signal_read_past_messages` - Read previous messages from the chat
- `signal_send_message` - Send a message via Signal
- `get_current_time` - Get the current timestamp
- `interaction_start:run_callback_function` - Start interaction callback
- `interaction_complete:run_callback_function` - Complete interaction callback

### Admin Tools (Admin Users Only)
- `signal_get_whitelist` - Get the Signal whitelist
- `signal_add_to_whitelist` - Add a number to the whitelist
- `signal_remove_from_whitelist` - Remove a number from the whitelist

### Integration Tools (When Available)
- `n8n_trigger_workflow_webhook` - Trigger n8n workflow via webhook

## Error Handling

The server returns JSON-RPC 2.0 error responses for various error conditions:

```json
{
  "jsonrpc": "2.0",
  "error": {
    "code": -32602,
    "message": "Invalid params",
    "data": "Missing tool name in params"
  },
  "id": 2
}
```

### Error Codes
- `-32700`: Parse error (Invalid JSON)
- `-32600`: Invalid Request
- `-32601`: Method not found
- `-32602`: Invalid params
- `-32603`: Internal error

## Integration with n8n

The n8n agent can connect to the HTTP Streamable MCP server using the chat UUID from the Signal bot interaction. The server URL pattern is:

```
POST /api/v1/interactions/{chat_uuid}/mcp
```

Where `{chat_uuid}` is the UUID of the chat where the Signal bot interaction is taking place.

### HTTP Streamable Transport

The server uses **HTTP Streamable** transport with the following characteristics:

- **Content-Type**: `application/x-ndjson`
- **Transfer-Encoding**: `chunked`
- **Format**: Newline-delimited JSON (NDJSON)
- **Connection**: Keep-alive for bidirectional streaming

## Tool Initialization

Tools are initialized with configuration data stored in the chat's shared configuration. This includes:

- Signal connection parameters (phone numbers, API host)
- Session tokens for authentication
- File mappings for attachment handling
- Admin privileges and whitelist management settings

## Security Considerations

1. **User Authentication**: Only authenticated bot users can access the MCP server
2. **Chat Access Control**: Users must have access to the specific chat UUID
3. **Tool Permissions**: Admin tools are only available to admin users
4. **Session Management**: Tools use session tokens for backend API calls

## Example Usage in n8n

For HTTP Streamable MCP, n8n should connect using the **HTTP Streamable** option in the MCP node with these settings:

### n8n MCP Node Configuration

1. **Transport**: Select "HTTP Streamable" (not "Server Send Events")
2. **Endpoint URL**: `https://your-backend.com/api/v1/interactions/{chat_uuid}/mcp`
3. **Authentication**: Use session cookie authentication
4. **Headers**: 
   ```
   Cookie: session_id={bot_session_token}
   Content-Type: application/x-ndjson
   ```

### Required Parameters for n8n

```json
{
  "endpoint": "https://your-backend.com/api/v1/interactions/{chat_uuid}/mcp",
  "transport": "http_streamable",
  "chat_uuid": "12345678-1234-1234-1234-123456789abc",
  "session_token": "bot_session_token_here",
  "headers": {
    "Cookie": "session_id=bot_session_token_here"
  }
}
```

### Manual HTTP Streamable Connection (for reference)

```javascript
// Example of how the streaming connection works (n8n handles this internally)
const mcpUrl = `${backendHost}/api/v1/interactions/${chatUuid}/mcp`;

// Create a streaming connection
const response = await fetch(mcpUrl, {
  method: 'POST',
  headers: {
    'Content-Type': 'application/x-ndjson',
    'Cookie': `session_id=${sessionToken}`
  },
  body: '{"jsonrpc": "2.0", "method": "tools/list", "id": 1}\n'
});

// Read streaming NDJSON responses
const reader = response.body.getReader();
const decoder = new TextDecoder();

while (true) {
  const { done, value } = await reader.read();
  if (done) break;
  
  const lines = decoder.decode(value).split('\n');
  for (const line of lines) {
    if (line.trim()) {
      const mcpResponse = JSON.parse(line);
      console.log('Received:', mcpResponse);
    }
  }
}
```

This implementation allows n8n agents to dynamically discover and execute tools at runtime, providing flexible integration capabilities for Signal bot interactions.
