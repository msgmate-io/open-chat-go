package websocket

import (
	"backend/runtimecfg"
	"net/http"
	"net/url"
	"strings"

	"github.com/coder/websocket"
)

// websocketOriginPatterns derives websocket Origin host patterns from the
// configured CORS allowlist. Same-origin connections are always permitted by
// websocket.Accept regardless of these patterns, so an empty allowlist
// preserves the previous same-origin-only behavior; configured origins are
// added on top and never replace the server's own origin.
func websocketOriginPatterns() []string {
	origins := runtimecfg.CORSAllowedOrigins()
	patterns := make([]string, 0, len(origins))
	for _, origin := range origins {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host == "" {
			continue
		}
		patterns = append(patterns, strings.ToLower(parsed.Host))
	}
	return patterns
}

func (cs *WebSocketHandler) acceptConnection(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	options := &websocket.AcceptOptions{
		OriginPatterns: websocketOriginPatterns(),
	}
	return websocket.Accept(w, r, options)
}
