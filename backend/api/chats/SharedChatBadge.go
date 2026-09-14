package chats

import (
	"fmt"
	"html"
	"net/http"
	"strings"

	"backend/chatstate"
	"backend/server/util"
)

// badgeState describes how an interaction state is rendered in the public
// interaction badge.
type badgeState struct {
	Label string
	Color string
}

// badgeStates maps the shared chat state vocabulary to badge label/color pairs.
var badgeStates = map[chatstate.State]badgeState{
	chatstate.StateActive:            {Label: "running", Color: "#0969da"},
	chatstate.StateFinished:          {Label: "finished", Color: "#2ecc40"},
	chatstate.StateFailed:            {Label: "failed", Color: "#c0392b"},
	chatstate.StateNeedsConfirmation: {Label: "waiting", Color: "#e2b93d"},
	chatstate.StateIdle:              {Label: "idle", Color: "#8b8b8b"},
}

// badgePillMinWidth is the minimum pill width (in px) of the right badge side.
const badgePillMinWidth = 32

// badgeFontWidth is the per-character width estimate (in px) for the 11px
// Verdana bold text rendered in the badge.
const badgeFontWidth = 7.0

// GetSharedInteractionBadge renders a public, shields.io-style SVG badge for a
// shared interaction. The badge only exposes the current processing state of
// the interaction (running / finished / failed / waiting / idle) — no message
// contents or other sensitive information. Same share UUID policy as the
// other /api/interaction endpoints: holding the chat share UUID is the
// authentication.
//
//	@Summary      Interaction state badge
//	@Description  Render an SVG badge showing the current processing state of a shared interaction
//	@Tags         chats
//	@Produce      image/svg+xml
//	@Param        chat_share_uuid path string true "Shared chat UUID"
//	@Success      200 {string} string "SVG badge"
//	@Router       /api/interaction/{chat_share_uuid}/badge.svg [get]
func (h *ChatsHandler) GetSharedInteractionBadge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "max-age=60")

	fallback := func(state badgeState, code int) {
		w.WriteHeader(code)
		_, _ = w.Write(renderBadgeSVG(state))
	}

	DB, err := util.GetDB(r)
	if err != nil {
		fallback(badgeState{Label: "unavailable", Color: "#8b8b8b"}, http.StatusInternalServerError)
		return
	}

	shareUUID := strings.TrimSpace(r.PathValue("chat_share_uuid"))
	if shareUUID == "" {
		fallback(badgeState{Label: "not found", Color: "#8b8b8b"}, http.StatusBadRequest)
		return
	}

	chat, _, err := getSharedChatByUUID(DB, shareUUID)
	if err != nil {
		fallback(badgeState{Label: "not found", Color: "#8b8b8b"}, http.StatusNotFound)
		return
	}

	// The live queue backend is optional for badges: without an asynq
	// inspector connected the status fallback still resolves idle/finished
	// states from the message metadata.
	inspector, inspectorErr := util.GetAsynqInspector(r)
	if inspectorErr != nil {
		inspector = nil
	}

	status, err := resolveInteractionStatus(DB, inspector, chat)
	if err != nil {
		fallback(badgeState{Label: "unavailable", Color: "#8b8b8b"}, http.StatusInternalServerError)
		return
	}

	state, ok := badgeStates[chatstate.State(status.State)]
	if !ok {
		state = badgeState{Label: strings.ToLower(status.State), Color: "#8b8b8b"}
	}
	_, _ = w.Write(renderBadgeSVG(state))
}

// renderBadgeSVG renders the shields-style flat SVG badge for the given state.
func renderBadgeSVG(state badgeState) []byte {
	const label = "open-chat"

	escapedLabel := html.EscapeString(label)
	escapedPill := html.EscapeString(state.Label)
	escapedTitle := html.EscapeString(fmt.Sprintf("open-chat: %s", state.Label))

	labelWidth := 12 + int(float64(len(label))*badgeFontWidth) + 12
	pillWidth := badgePillMinWidth
	if estimated := int(float64(len(state.Label)) * badgeFontWidth); estimated > pillWidth {
		pillWidth = estimated
	}
	pillWidth += 14
	if pillWidth < 48 {
		pillWidth = 48
	}
	total := labelWidth + pillWidth

	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s">
<title>%s</title>
<linearGradient id="s" x2="0" y2="100%%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>
<clipPath id="r"><rect width="%d" height="20" rx="3" fill="#fff"/></clipPath>
<g clip-path="url(#r)">
<rect width="%d" height="20" fill="#555"/>
<rect x="%d" width="%d" height="20" fill="%s"/>
<rect width="%d" height="20" fill="url(#s)"/>
</g>
<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11" font-weight="bold">
<text x="%d" y="14" fill="#010101" fill-opacity=".3">%s</text>
<text x="%d" y="13.5">%s</text>
<text x="%d" y="14" fill="#010101" fill-opacity=".3">%s</text>
<text x="%d" y="13.5">%s</text>
</g>
</svg>
`,
		total, escapedTitle, escapedTitle, total,
		labelWidth,
		labelWidth, pillWidth, state.Color,
		total,
		labelWidth/2, escapedLabel, labelWidth/2, escapedLabel,
		labelWidth+pillWidth/2+1, escapedPill, labelWidth+pillWidth/2+1, escapedPill,
	)
	return []byte(svg)
}
