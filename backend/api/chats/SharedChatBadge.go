package chats

import (
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"backend/chatstate"
	"backend/database"
	"backend/runtimecfg"
	"backend/server/util"

	"gorm.io/gorm"
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

// badgePillMinWidth is the minimum pill width (in px) of a text badge segment.
const badgePillMinWidth = 32

// badgeFontWidth is the per-character width estimate (in px) for the 11px
// Verdana bold text rendered in the badge.
const badgeFontWidth = 7.0

const (
	badgeHeight   = 20
	badgeFontSize = 11
	badgePadX     = 10

	// Runtime ticker geometry. Each digit column is a clipped vertical
	// odometer of badgeFontSize glyphs; the ticker shows MM:SS.
	tickerCellHeight   = 14
	tickerDigitWidth   = 7
	tickerColonWidth   = 4
	tickerWindowY      = 3
	tickerWindowHeight = 12
)

// tickerContentWidth is the width of the MM:SS glyph area (without padding).
var tickerContentWidth = 4*tickerDigitWidth + tickerColonWidth

// badgeRuntime is the runtime of the current or last interaction run.
// Active runtimes are rendered as a live SMIL odometer; terminal runtimes are
// rendered as a static timestamp.
type badgeRuntime struct {
	Seconds int64
	Active  bool
}

// badgeData is the high level description of a detailed badge.
type badgeData struct {
	Label   string
	Host    string
	State   string
	Color   string
	Runtime *badgeRuntime
}

// badgeSegment is a single rendered section of a badge. When Runtime is set
// the segment renders the runtime instead of Text.
type badgeSegment struct {
	Text    string
	Color   string
	Runtime *badgeRuntime
}

// GetSharedInteractionBadge renders a public, shields.io-style SVG badge for a
// shared interaction. The badge only exposes the current processing state of
// the interaction (running / finished / failed / waiting / idle) plus, in the
// default detailed variant, the server host name and the interaction runtime.
// No message contents or other sensitive information are exposed. Same share
// UUID policy as the other /api/interaction endpoints: holding the chat share
// UUID is the authentication.
//
//	@Summary      Interaction state badge
//	@Description  Render an SVG badge showing the current processing state, server host and runtime of a shared interaction. Pass ?variant=simple for the legacy two-segment badge.
//	@Tags         chats
//	@Produce      image/svg+xml
//	@Param        chat_share_uuid path string true "Shared chat UUID"
//	@Param        variant query string false "Badge variant: detailed (default) or simple"
//	@Success      200 {string} string "SVG badge"
//	@Router       /api/interaction/{chat_share_uuid}/badge.svg [get]
func (h *ChatsHandler) GetSharedInteractionBadge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "max-age=60")

	variant := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("variant")))
	simpleVariant := variant == "simple" || variant == "flat"

	fallback := func(state badgeState, code int) {
		w.WriteHeader(code)
		_, _ = w.Write(renderSimpleBadgeSVG(state))
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

	if simpleVariant {
		_, _ = w.Write(renderSimpleBadgeSVG(state))
		return
	}

	// Active badges animate from a server computed phase, so they must not be
	// cached for long or the SMIL begin offset freezes at the first fetch.
	if status.IsActive {
		w.Header().Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
	}

	data := badgeData{
		Label:   "open-chat",
		Host:    resolveBadgeHost(r),
		State:   state.Label,
		Color:   state.Color,
		Runtime: resolveInteractionRuntime(DB, chat, status),
	}
	_, _ = w.Write(renderBadgeSVG(data))
}

// resolveBadgeHost resolves the host name advertised on the badge. It prefers
// the configured public base URL, then proxy/host headers and finally the OS
// hostname.
func resolveBadgeHost(r *http.Request) string {
	if base := strings.TrimSpace(runtimecfg.PublicBaseURL()); base != "" {
		if parsed, err := url.Parse(base); err == nil && parsed.Host != "" {
			return parsed.Host
		}
	}
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		if comma := strings.Index(forwarded, ","); comma >= 0 {
			forwarded = forwarded[:comma]
		}
		if host := strings.TrimSpace(forwarded); host != "" {
			return host
		}
	}
	if host := strings.TrimSpace(r.Host); host != "" {
		return host
	}
	if hostname, err := os.Hostname(); err == nil {
		return hostname
	}
	return ""
}

// resolveInteractionRuntime derives the runtime of the current or last run of
// an interaction. The run is assumed to start at the latest message sent by a
// human (falling back to the chat creation time). Returns nil when there is no
// runtime to show (idle interaction without any run).
func resolveInteractionRuntime(DB *gorm.DB, chat database.Chat, status InteractionStatusResponse) *badgeRuntime {
	start, ok := latestHumanMessageCreatedAt(DB, chat)
	if !ok {
		start = chat.CreatedAt
	}

	switch chatstate.State(status.State) {
	case chatstate.StateActive:
		return &badgeRuntime{Seconds: elapsedSeconds(time.Since(start)), Active: true}
	case chatstate.StateFinished, chatstate.StateFailed:
		latest, err := latestMessageForChat(DB, chat.ID)
		if err != nil {
			return nil
		}
		if total, ok := parseTotalTime(latest); ok {
			return &badgeRuntime{Seconds: int64(total.Seconds()), Active: false}
		}
		return &badgeRuntime{Seconds: elapsedSeconds(latest.CreatedAt.Sub(start)), Active: false}
	case chatstate.StateNeedsConfirmation:
		latest, err := latestMessageForChat(DB, chat.ID)
		if err != nil {
			return nil
		}
		return &badgeRuntime{Seconds: elapsedSeconds(latest.CreatedAt.Sub(start)), Active: false}
	default:
		// Idle: surface the last run's runtime only when the chat has messages.
		latest, err := latestMessageForChat(DB, chat.ID)
		if err != nil {
			return nil
		}
		return &badgeRuntime{Seconds: elapsedSeconds(latest.CreatedAt.Sub(start)), Active: false}
	}
}

// latestHumanMessageCreatedAt returns the creation time of the most recent
// message sent by a non-automated (human) user in the chat.
func latestHumanMessageCreatedAt(DB *gorm.DB, chat database.Chat) (time.Time, bool) {
	var message database.Message
	err := DB.
		Joins("JOIN users ON users.id = messages.sender_id").
		Where("messages.chat_id = ?", chat.ID).
		Where("messages.deleted_at IS NULL").
		Where("users.deleted_at IS NULL").
		Where("users.is_automated = ?", false).
		Order("messages.created_at DESC").
		First(&message).Error
	if err != nil {
		return time.Time{}, false
	}
	return message.CreatedAt, true
}

// parseTotalTime extracts the "total_time" run duration from a message's
// metadata. The value is written by the bot handlers as a time.Duration string.
func parseTotalTime(message database.Message) (time.Duration, bool) {
	if len(message.MetaData) == 0 {
		return 0, false
	}
	meta := map[string]interface{}{}
	if json.Unmarshal(message.MetaData, &meta) != nil {
		return 0, false
	}
	raw, ok := meta["total_time"].(string)
	if !ok {
		return 0, false
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		return 0, false
	}
	if duration < 0 {
		duration = 0
	}
	return duration, true
}

func elapsedSeconds(d time.Duration) int64 {
	seconds := int64(d.Seconds())
	if seconds < 0 {
		return 0
	}
	return seconds
}

// formatBadgeRuntime renders whole seconds as MM:SS, or H:MM:SS beyond an hour.
func formatBadgeRuntime(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, secs)
	}
	return fmt.Sprintf("%02d:%02d", minutes, secs)
}

// renderSimpleBadgeSVG renders the legacy two-segment badge (label + state).
func renderSimpleBadgeSVG(state badgeState) []byte {
	return renderSegments([]badgeSegment{
		{Text: "open-chat", Color: "#555"},
		{Text: state.Label, Color: state.Color},
	}, fmt.Sprintf("open-chat: %s", state.Label))
}

// renderBadgeSVG renders the detailed badge from the resolved badge data.
func renderBadgeSVG(data badgeData) []byte {
	segments := make([]badgeSegment, 0, 4)
	if data.Label != "" {
		segments = append(segments, badgeSegment{Text: data.Label, Color: "#555"})
	}
	if data.Host != "" {
		segments = append(segments, badgeSegment{Text: data.Host, Color: "#3b3b3b"})
	}
	if data.State != "" {
		segments = append(segments, badgeSegment{Text: data.State, Color: data.Color})
	}
	if data.Runtime != nil {
		segments = append(segments, badgeSegment{Runtime: data.Runtime, Color: "#3b3b3b"})
	}

	title := data.Label
	if data.Host != "" {
		title += " [" + data.Host + "]"
	}
	if data.State != "" {
		title += ": " + data.State
	}
	if data.Runtime != nil {
		title += " " + formatBadgeRuntime(data.Runtime.Seconds)
	}
	return renderSegments(segments, strings.TrimSpace(title))
}

// renderSegments renders a shields-style badge from a list of segments.
func renderSegments(segments []badgeSegment, title string) []byte {
	if len(segments) == 0 {
		segments = []badgeSegment{{Text: "open-chat", Color: "#555"}}
	}

	widths := make([]int, len(segments))
	totalWidth := 0
	for i, segment := range segments {
		widths[i] = badgeSegmentWidth(segment)
		totalWidth += widths[i]
	}

	escapedTitle := html.EscapeString(title)
	var b strings.Builder

	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" role="img" aria-label="%s">`+"\n",
		totalWidth, badgeHeight, escapedTitle)
	fmt.Fprintf(&b, "<title>%s</title>\n", escapedTitle)
	b.WriteString(`<linearGradient id="s" x2="0" y2="100%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>` + "\n")
	fmt.Fprintf(&b, `<clipPath id="r"><rect width="%d" height="%d" rx="3" fill="#fff"/></clipPath>`+"\n", totalWidth, badgeHeight)
	b.WriteString(`<g clip-path="url(#r)">` + "\n")

	x := 0
	for i, segment := range segments {
		color := segment.Color
		if color == "" {
			color = "#555"
		}
		fmt.Fprintf(&b, `<rect x="%d" width="%d" height="%d" fill="%s"/>`+"\n",
			x, widths[i], badgeHeight, color)
		x += widths[i]
	}
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="url(#s)"/>`+"\n", totalWidth, badgeHeight)
	b.WriteString("</g>\n")

	x = 0
	for i, segment := range segments {
		if segment.Runtime != nil && segment.Runtime.Active {
			b.WriteString(renderRuntimeTicker(segment.Runtime, x, widths[i], i))
		} else {
			text := segment.Text
			if segment.Runtime != nil {
				text = formatBadgeRuntime(segment.Runtime.Seconds)
			}
			b.WriteString(renderSegmentLabel(text, x, widths[i]))
		}
		x += widths[i]
	}

	b.WriteString("</svg>\n")
	return []byte(b.String())
}

// badgeSegmentWidth estimates the rendered width of a segment in pixels.
func badgeSegmentWidth(segment badgeSegment) int {
	if segment.Runtime != nil && segment.Runtime.Active {
		return tickerContentWidth + 2*badgePadX
	}
	text := segment.Text
	if segment.Runtime != nil {
		text = formatBadgeRuntime(segment.Runtime.Seconds)
	}
	width := int(math.Ceil(float64(len(text)) * badgeFontWidth))
	if width < badgePillMinWidth {
		width = badgePillMinWidth
	}
	return width + 2*badgePadX
}

// renderSegmentLabel renders a centered text label with a drop shadow.
func renderSegmentLabel(text string, x, width int) string {
	escaped := html.EscapeString(text)
	center := x + width/2
	return fmt.Sprintf(
		`<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="%d" font-weight="bold">`+"\n"+
			`<text x="%d" y="14" fill="#010101" fill-opacity=".3">%s</text>`+"\n"+
			`<text x="%d" y="13.5">%s</text>`+"\n"+
			`</g>`+"\n",
		badgeFontSize, center, escaped, center, escaped,
	)
}

// tickerColumn describes one digit of the MM:SS odometer. A column repeats
// every periodSeconds and has cells distinct digits.
type tickerColumn struct {
	PeriodSeconds int64
	Cells         int
}

// tickerColumns are the four MM:SS digits: minutes tens/ones, seconds tens/ones.
var tickerColumns = [4]tickerColumn{
	{PeriodSeconds: 6000, Cells: 10},
	{PeriodSeconds: 600, Cells: 10},
	{PeriodSeconds: 60, Cells: 6},
	{PeriodSeconds: 10, Cells: 10},
}

// renderRuntimeTicker renders a pure-SVG SMIL odometer that ticks once per
// second inside the SVG. The inner group keeps a static transform matching the
// server computed value so renderers that strip SMIL still show a correct
// (non-ticking) timestamp.
func renderRuntimeTicker(runtime *badgeRuntime, x, width, seed int) string {
	seconds := runtime.Seconds
	if seconds < 0 {
		seconds = 0
	}
	if maxSeconds := int64(99*60 + 59); seconds > maxSeconds {
		seconds = maxSeconds
	}

	contentX := x + (width-tickerContentWidth)/2
	columnX := [4]int{
		contentX,
		contentX + tickerDigitWidth,
		contentX + 2*tickerDigitWidth + tickerColonWidth,
		contentX + 3*tickerDigitWidth + tickerColonWidth,
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="%d" font-weight="bold">`+"\n", badgeFontSize)
	for i, column := range tickerColumns {
		b.WriteString(renderTickerColumn(column, seconds, columnX[i], seed, i))
	}
	colonCenter := contentX + 2*tickerDigitWidth + tickerColonWidth/2
	fmt.Fprintf(&b, `<text x="%d" y="14">:</text>`+"\n", colonCenter)
	b.WriteString("</g>\n")
	return b.String()
}

// renderTickerColumn renders one animated digit column of the runtime ticker.
func renderTickerColumn(column tickerColumn, seconds int64, x, seed, index int) string {
	cellSeconds := column.PeriodSeconds / int64(column.Cells)
	valueIndex := int64(0)
	if cellSeconds > 0 {
		valueIndex = (seconds / cellSeconds) % int64(column.Cells)
	}
	begin := -(seconds % column.PeriodSeconds)
	clipID := fmt.Sprintf("ocrt%d_%d", seed, index)
	digitX := x + tickerDigitWidth/2

	var b strings.Builder
	fmt.Fprintf(&b, `<clipPath id="%s"><rect x="%d" y="%d" width="%d" height="%d"/></clipPath>`+"\n",
		clipID, x, tickerWindowY, tickerDigitWidth, tickerWindowHeight)
	fmt.Fprintf(&b, `<g clip-path="url(#%s)">`+"\n", clipID)
	fmt.Fprintf(&b, `<g transform="translate(0,%d)">`+"\n", -int(valueIndex)*tickerCellHeight)

	for cell := 0; cell <= column.Cells; cell++ {
		baseline := tickerWindowY + badgeFontSize + cell*tickerCellHeight
		fmt.Fprintf(&b, `<text x="%d" y="%d">%d</text>`+"\n", digitX, baseline, cell%column.Cells)
	}

	values := make([]string, 0, column.Cells+1)
	keyTimes := make([]string, 0, column.Cells+1)
	for cell := 0; cell <= column.Cells; cell++ {
		values = append(values, fmt.Sprintf("0 %d", -cell*tickerCellHeight))
		switch {
		case cell == 0:
			keyTimes = append(keyTimes, "0")
		case cell == column.Cells:
			keyTimes = append(keyTimes, "1")
		default:
			keyTimes = append(keyTimes, strconv.FormatFloat(float64(cell)/float64(column.Cells), 'f', 4, 64))
		}
	}
	fmt.Fprintf(&b, `<animateTransform attributeName="transform" type="translate" values="%s" keyTimes="%s" dur="%ds" begin="%ds" repeatCount="indefinite" calcMode="discrete"/>`+"\n",
		strings.Join(values, ";"), strings.Join(keyTimes, ";"), column.PeriodSeconds, begin)
	b.WriteString("</g>\n</g>\n")
	return b.String()
}
