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
	chatstate.StateNeedsConfirmation: {Label: "confirmation required", Color: "#e2b93d"},
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
	// FollowUp, when set, renders a second row beneath the interaction row
	// describing the follow-up interaction that was started (e.g. the agent a
	// runtime-selector/chooser bot handed the request off to).
	FollowUp *badgeFollowUp
}

// badgeFollowUp is the rendered description of a follow-up interaction shown as
// a nested badge row.
type badgeFollowUp struct {
	Agent   string
	State   string
	Color   string
	Runtime *badgeRuntime
	URL     string
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
// the interaction (running / finished / failed / confirmation required / idle)
// plus, in the default detailed variant, the server host name and the
// interaction runtime. When an interaction is blocked on user input the state
// label spells out the required action instead of a generic "waiting" pill, so
// the indicator posted on the triggering issue makes the pending confirmation
// obvious. No message contents or other sensitive information are exposed. Same
// share UUID policy as the other /api/interaction endpoints: holding the chat
// share UUID is the authentication.
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
	// cached for long or the SMIL begin offset freezes at the first fetch. The
	// same applies when the nested follow-up row is still running.
	followUp := badgeFollowUpForInteraction(DB, inspector, chat)
	if status.IsActive || (followUp != nil && followUp.Runtime != nil && followUp.Runtime.Active) {
		w.Header().Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
	}

	data := badgeData{
		Label:    "open-chat",
		Host:     resolveBadgeHost(r),
		State:    state.Label,
		Color:    state.Color,
		Runtime:  resolveInteractionRuntime(DB, chat, status),
		FollowUp: followUp,
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

// renderBadgeSVG renders the detailed badge from the resolved badge data. When
// the interaction started a follow-up interaction (e.g. a runtime-selector
// chooser handing the request off to the picked agent) the follow-up is
// rendered as a second row beneath the interaction row.
func renderBadgeSVG(data badgeData) []byte {
	rows := make([][]badgeSegment, 0, 2)
	parent := make([]badgeSegment, 0, 4)
	if data.Label != "" {
		parent = append(parent, badgeSegment{Text: data.Label, Color: "#555"})
	}
	if data.Host != "" {
		parent = append(parent, badgeSegment{Text: data.Host, Color: "#3b3b3b"})
	}
	if data.State != "" {
		parent = append(parent, badgeSegment{Text: data.State, Color: data.Color})
	}
	if data.Runtime != nil {
		parent = append(parent, badgeSegment{Runtime: data.Runtime, Color: "#3b3b3b"})
	}
	rows = append(rows, parent)

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

	if data.FollowUp != nil {
		follow := make([]badgeSegment, 0, 4)
		follow = append(follow, badgeSegment{Text: "follow-up", Color: "#2b2b2b"})
		if data.FollowUp.Agent != "" {
			follow = append(follow, badgeSegment{Text: data.FollowUp.Agent, Color: "#3b3b3b"})
		}
		if data.FollowUp.State != "" {
			follow = append(follow, badgeSegment{Text: data.FollowUp.State, Color: data.FollowUp.Color})
		}
		if data.FollowUp.Runtime != nil {
			follow = append(follow, badgeSegment{Runtime: data.FollowUp.Runtime, Color: "#3b3b3b"})
		}
		rows = append(rows, follow)

		title += " -> follow-up"
		if data.FollowUp.Agent != "" {
			title += ": " + data.FollowUp.Agent
		}
		if data.FollowUp.State != "" {
			title += " (" + data.FollowUp.State + ")"
		}
	}
	return renderRows(rows, strings.TrimSpace(title), data.FollowUp)
}

// renderSegments renders a single-row shields-style badge from a list of
// segments.
func renderSegments(segments []badgeSegment, title string) []byte {
	return renderRows([][]badgeSegment{segments}, title, nil)
}

// badgeRowGap is the vertical gap (in px) between stacked badge rows.
const badgeRowGap = 3

// renderRows renders one or more stacked shields-style badge rows. When the
// last row belongs to a follow-up interaction (followUp != nil) it is wrapped
// in an SVG link so renderers that honour SVG anchors can navigate to it.
func renderRows(rows [][]badgeSegment, title string, followUp *badgeFollowUp) []byte {
	if len(rows) == 0 || len(rows[0]) == 0 {
		rows = [][]badgeSegment{{{Text: "open-chat", Color: "#555"}}}
	}

	widths := make([][]int, len(rows))
	totalWidth := 0
	for r, segments := range rows {
		widths[r] = make([]int, len(segments))
		rowWidth := 0
		for i, segment := range segments {
			widths[r][i] = badgeSegmentWidth(segment)
			rowWidth += widths[r][i]
		}
		if rowWidth > totalWidth {
			totalWidth = rowWidth
		}
	}

	totalHeight := len(rows)*badgeHeight + (len(rows)-1)*badgeRowGap
	escapedTitle := html.EscapeString(title)
	var b strings.Builder

	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" role="img" aria-label="%s">`+"\n",
		totalWidth, totalHeight, escapedTitle)
	fmt.Fprintf(&b, "<title>%s</title>\n", escapedTitle)
	b.WriteString(`<linearGradient id="s" x2="0" y2="100%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>` + "\n")

	// Backgrounds, one clip path per row so each row keeps its own rounded
	// corners.
	for r, segments := range rows {
		yOffset := r * (badgeHeight + badgeRowGap)
		clipID := fmt.Sprintf("r%d", r)
		fmt.Fprintf(&b, `<clipPath id="%s"><rect y="%d" width="%d" height="%d" rx="3" fill="#fff"/></clipPath>`+"\n",
			clipID, yOffset, totalWidth, badgeHeight)
		fmt.Fprintf(&b, `<g clip-path="url(#%s)">`+"\n", clipID)
		x := 0
		for i, segment := range segments {
			color := segment.Color
			if color == "" {
				color = "#555"
			}
			fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" fill="%s"/>`+"\n",
				x, yOffset, widths[r][i], badgeHeight, color)
			x += widths[r][i]
		}
		fmt.Fprintf(&b, `<rect y="%d" width="%d" height="%d" fill="url(#s)"/>`+"\n", yOffset, totalWidth, badgeHeight)
		b.WriteString("</g>\n")
	}

	// Labels/tickers. The last row is wrapped in a link when it is a follow-up
	// so renderers that support SVG anchors can open the follow-up interaction.
	seed := 0
	for r, segments := range rows {
		yOffset := r * (badgeHeight + badgeRowGap)
		isFollowUpRow := followUp != nil && r == len(rows)-1 && len(rows) > 1
		linkRow := isFollowUpRow && strings.TrimSpace(followUp.URL) != ""
		if linkRow {
			fmt.Fprintf(&b, `<a href="%s" target="_blank" rel="noopener">`+"\n", html.EscapeString(followUp.URL))
		}
		x := 0
		for i, segment := range segments {
			if segment.Runtime != nil && segment.Runtime.Active {
				b.WriteString(renderRuntimeTicker(segment.Runtime, x, widths[r][i], seed, yOffset))
			} else {
				text := segment.Text
				if segment.Runtime != nil {
					text = formatBadgeRuntime(segment.Runtime.Seconds)
				}
				b.WriteString(renderSegmentLabel(text, x, widths[r][i], yOffset))
			}
			x += widths[r][i]
			seed++
		}
		if linkRow {
			b.WriteString("</a>\n")
		}
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

// renderSegmentLabel renders a centered text label with a drop shadow at the
// given row y offset.
func renderSegmentLabel(text string, x, width, yOffset int) string {
	escaped := html.EscapeString(text)
	center := x + width/2
	var b strings.Builder
	fmt.Fprintf(&b, `<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="%d" font-weight="bold">`+"\n", badgeFontSize)
	fmt.Fprintf(&b, `<text x="%d" y="%d" fill="#010101" fill-opacity=".3">`, center, 14+yOffset)
	b.WriteString(escaped)
	b.WriteString("</text>\n")
	fmt.Fprintf(&b, `<text x="%d" y="%v">`, center, 13.5+float64(yOffset))
	b.WriteString(escaped)
	b.WriteString("</text>\n</g>\n")
	return b.String()
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
func renderRuntimeTicker(runtime *badgeRuntime, x, width, seed, yOffset int) string {
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
		b.WriteString(renderTickerColumn(column, seconds, columnX[i], seed, i, yOffset))
	}
	colonCenter := contentX + 2*tickerDigitWidth + tickerColonWidth/2
	fmt.Fprintf(&b, `<text x="%d" y="%d">:</text>`+"\n", colonCenter, 14+yOffset)
	b.WriteString("</g>\n")
	return b.String()
}

// renderTickerColumn renders one animated digit column of the runtime ticker.
func renderTickerColumn(column tickerColumn, seconds int64, x, seed, index, yOffset int) string {
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
		clipID, x, tickerWindowY+yOffset, tickerDigitWidth, tickerWindowHeight)
	fmt.Fprintf(&b, `<g clip-path="url(#%s)">`+"\n", clipID)
	fmt.Fprintf(&b, `<g transform="translate(0,%d)">`+"\n", -int(valueIndex)*tickerCellHeight)

	for cell := 0; cell <= column.Cells; cell++ {
		baseline := tickerWindowY + badgeFontSize + cell*tickerCellHeight + yOffset
		fmt.Fprintf(&b, `<text x="%d" y="%d">%d</text>`+"\n", digitX, baseline, cell%column.Cells)
	}

	values := make([]string, 0, column.Cells+1)
	keyTimes := make([]string, 0, column.Cells+1)
	for cell := 0; cell <= column.Cells; cell++ {
		values = append(values, fmt.Sprintf("0 %d", -cell*tickerCellHeight))
		switch cell {
		case 0:
			keyTimes = append(keyTimes, "0")
		case column.Cells:
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
