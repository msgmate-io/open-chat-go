package chats

import (
	"backend/chatstate"
	"backend/database"
	"backend/logging"
	"backend/server/util"
	"backend/workqueue"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

// ci#1101: a coder TUI that runs over a *local* ssh link (the msgmate_coder
// integration forwards the TUI into a per-chat coding sandbox over an ssh
// session) keeps driving an interaction that only exists because a human is
// sitting in front of it. When that ssh session ends the TUI process dies with
// it, but the interaction used to keep running headless: the auto-continuation
// loop kept scheduling turns and a live bot:reply task kept burning tokens for
// nobody.
//
// The server cannot observe the ssh link itself (it is between the user's
// machine and the sandbox), so the TUI reports the link's liveness:
//
//   - POST /chats/{chat_uuid}/link/heartbeat while the link is up (the TUI
//     sends it on a short interval, e.g. every 15-30s);
//   - POST /chats/{chat_uuid}/link/end when the TUI is shutting down cleanly
//     (the ssh session is closing), which stops the interaction immediately.
//
// A heartbeat arms a *lease*: if no heartbeat arrives for the lease's grace
// period the sweeper stops the interaction, so a TUI that was killed (no clean
// end signal) still stops the run. The grace period is deliberately generous
// (default 90s) so a brief reconnect - a laptop sleeping, a flaky link, an ssh
// multiplexer reconnecting - refreshes the lease instead of killing a healthy
// run. Only chats that ever armed a lease are affected: an interaction driven
// from the web UI or by a dispatched agent never registers one and is never
// stopped by this mechanism.
const (
	// localLinkStopReason is the auto-continuation stop reason recorded when a
	// local link ends (cleanly or by lease expiry), so the stop is inspectable
	// and a later restart does not silently resume the loop.
	localLinkStopReason = "local_link_lost"

	// DefaultLocalLinkGrace is the lease grace period used when a heartbeat
	// does not ask for one. It is long enough to survive a brief reconnect.
	DefaultLocalLinkGrace = 90 * time.Second
	// MinLocalLinkGrace / MaxLocalLinkGrace clamp a client-requested grace so a
	// buggy or hostile client cannot pin an interaction alive forever nor make
	// the lease expire before a single reconnect can happen.
	MinLocalLinkGrace = 15 * time.Second
	MaxLocalLinkGrace = 30 * time.Minute

	// DefaultLocalLinkSweepInterval is how often the sweeper looks for expired
	// leases.
	DefaultLocalLinkSweepInterval = 30 * time.Second
)

// localLinkLease is the in-memory liveness record of one chat's local link.
// It is intentionally not persisted: a server restart forgets the leases and
// therefore never stops an interaction on stale state (fail-open - the worst
// case is the old headless behaviour, never a killed healthy run).
type localLinkLease struct {
	ChatUUID string
	LastSeen time.Time
	Grace    time.Duration
}

// expiresAt is the instant the lease stops being valid.
func (l localLinkLease) expiresAt() time.Time {
	return l.LastSeen.Add(l.Grace)
}

// expired reports whether the lease lapsed at now.
func (l localLinkLease) expired(now time.Time) bool {
	return !now.Before(l.expiresAt())
}

// localLinkRegistry holds the armed leases, keyed by chat UUID.
type localLinkRegistry struct {
	mu     sync.Mutex
	leases map[string]localLinkLease
}

var localLinkLeases = &localLinkRegistry{leases: map[string]localLinkLease{}}

// clampLocalLinkGrace normalises a requested grace period.
func clampLocalLinkGrace(grace time.Duration) time.Duration {
	switch {
	case grace <= 0:
		return DefaultLocalLinkGrace
	case grace < MinLocalLinkGrace:
		return MinLocalLinkGrace
	case grace > MaxLocalLinkGrace:
		return MaxLocalLinkGrace
	default:
		return grace
	}
}

// TouchLocalLinkLease arms or refreshes the local-link lease of a chat. A
// heartbeat that arrives inside the grace period simply moves LastSeen
// forward, which is what makes a brief reconnect harmless.
func TouchLocalLinkLease(chatUUID string, now time.Time, grace time.Duration) localLinkLease {
	lease := localLinkLease{
		ChatUUID: strings.TrimSpace(chatUUID),
		LastSeen: now,
		Grace:    clampLocalLinkGrace(grace),
	}
	localLinkLeases.mu.Lock()
	defer localLinkLeases.mu.Unlock()
	localLinkLeases.leases[lease.ChatUUID] = lease
	return lease
}

// EndLocalLinkLease drops the lease of a chat and reports whether one was
// armed. It is the clean session-end path: the caller stops the interaction
// right away instead of waiting for the grace period.
func EndLocalLinkLease(chatUUID string) (localLinkLease, bool) {
	key := strings.TrimSpace(chatUUID)
	localLinkLeases.mu.Lock()
	defer localLinkLeases.mu.Unlock()
	lease, ok := localLinkLeases.leases[key]
	if ok {
		delete(localLinkLeases.leases, key)
	}
	return lease, ok
}

// LocalLinkLeaseStatus reports the lease of a chat at now.
func LocalLinkLeaseStatus(chatUUID string, now time.Time) (localLinkLease, bool, bool) {
	key := strings.TrimSpace(chatUUID)
	localLinkLeases.mu.Lock()
	defer localLinkLeases.mu.Unlock()
	lease, ok := localLinkLeases.leases[key]
	if !ok {
		return localLinkLease{}, false, false
	}
	return lease, true, lease.expired(now)
}

// takeExpiredLocalLinkLeases removes and returns every lease that lapsed at
// now, so a single sweep stops each chat exactly once.
func takeExpiredLocalLinkLeases(now time.Time) []localLinkLease {
	localLinkLeases.mu.Lock()
	defer localLinkLeases.mu.Unlock()
	expired := make([]localLinkLease, 0, len(localLinkLeases.leases))
	for key, lease := range localLinkLeases.leases {
		if lease.expired(now) {
			expired = append(expired, lease)
			delete(localLinkLeases.leases, key)
		}
	}
	return expired
}

// resetLocalLinkLeasesForTest clears the registry (tests only).
func resetLocalLinkLeasesForTest() {
	localLinkLeases.mu.Lock()
	defer localLinkLeases.mu.Unlock()
	localLinkLeases.leases = map[string]localLinkLease{}
}

// StopInteractionForLocalLinkLoss stops a running interaction because the local
// link that drove it is gone: it aborts a live generation (both the detached
// external-backend goroutine and the queued bot:reply task), drains the queued
// markers of the replies that will never run, and latches the auto-continuation
// loop off with the local_link_lost reason so it cannot resume headless.
func StopInteractionForLocalLinkLoss(DB *gorm.DB, inspector *asynq.Inspector, chat database.Chat, reason string) {
	if DB == nil || chat.ID == 0 {
		return
	}
	if strings.TrimSpace(reason) == "" {
		reason = localLinkStopReason
	}

	// External chat backends (eg opencode) run their generations in detached
	// goroutines outside the asynq bot:reply task, so the queue cancellation
	// below never reaches them.
	if backend, ok := chatBackendName(DB, chat); ok {
		if handler, ok := chatstate.LookupBackendInterruptHandler(backend); ok {
			handler(chat.UUID)
		}
	}

	if inspector != nil {
		workqueue.CancelBotReplyTask(inspector, chat.UUID)
		ClearQueuedMarkersForChat(DB, chat.ID, chat.UUID)
	}

	if err := StopAutoContinueForChat(DB, chat.ID, reason); err != nil {
		logging.Errorf("ci#1101: failed to stop the auto-continuation loop of chat %s after local link loss: %v", chat.UUID, err)
	}

	// ci#1101: stopping alone leaves the operator with an interaction that
	// silently rests in `stopped`. Surface it as a visible failure turn that
	// carries the upstream sandboxes the interaction can be continued in, so the
	// web UI can offer a resume instead of the interaction just going quiet.
	ReportTuiDetached(DB, chat, tuiDetachedEventForChat(DB, chat, localLinkStopReason), time.Now())

	logging.Infof("ci#1101: stopped interaction %s because its local link ended (%s)", chat.UUID, reason)
}

// SweepExpiredLocalLinkLeases stops every interaction whose local-link lease
// lapsed at now and returns how many it stopped. A chat that no longer exists
// (or is not an interaction) is skipped silently.
func SweepExpiredLocalLinkLeases(DB *gorm.DB, inspector *asynq.Inspector, now time.Time) int {
	expired := takeExpiredLocalLinkLeases(now)
	stopped := 0
	for _, lease := range expired {
		if DB == nil {
			continue
		}
		var chat database.Chat
		if err := DB.Where("uuid = ?", lease.ChatUUID).First(&chat).Error; err != nil {
			logging.Debugf("ci#1101: dropping local-link lease of unknown chat %s: %v", lease.ChatUUID, err)
			continue
		}
		if !isInteractionChatType(chat.ChatType) {
			continue
		}
		StopInteractionForLocalLinkLoss(DB, inspector, chat, localLinkStopReason)
		stopped++
	}
	return stopped
}

// StartLocalLinkLeaseSweeper runs the lease sweeper until the returned stop
// function is called. A non-positive interval falls back to
// DefaultLocalLinkSweepInterval. It is safe to call with a nil DB or inspector:
// the sweeper then only drains the registry.
//
// Every tick is handed to the package's registered detached-work group
// (chatsDetachedDBWork, see detached_work.go) instead of running the sweep
// inline in the loop goroutine: the sweep writes through the pool, so a test
// teardown has to be able to drain an in-flight sweep before CloseTestDB closes
// the pool and t.TempDir removes the directory (ci#811, ci#1101). The loop
// itself only waits on the ticker and is stopped by the returned function, so it
// never pins the drain.
func StartLocalLinkLeaseSweeper(DB *gorm.DB, inspector *asynq.Inspector, interval time.Duration) func() {
	if interval <= 0 {
		interval = DefaultLocalLinkSweepInterval
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	// sweep is the tracked unit of work: it is the only place that touches the
	// pool, and it is reached through chatsDetachedDBWork.
	sweep := func(now time.Time) {
		if stopped := SweepExpiredLocalLinkLeases(DB, inspector, now); stopped > 0 {
			logging.Infof("ci#1101: stopped %d interaction(s) whose local link ended", stopped)
		}
	}

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				GoDetachedDBWork(func() { sweep(now) })
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}

// LocalLinkStatusResponse is the body of the local-link routes.
type LocalLinkStatusResponse struct {
	ChatUUID        string `json:"chat_uuid"`
	Active          bool   `json:"active"`
	Expired         bool   `json:"expired"`
	LastSeen        string `json:"last_seen,omitempty"`
	GraceSeconds    int    `json:"grace_seconds"`
	ExpiresInSecond int    `json:"expires_in_seconds,omitempty"`
	Stopped         bool   `json:"stopped,omitempty"`
	StopReason      string `json:"stop_reason,omitempty"`
}

// localLinkHeartbeatRequest is the optional body of the heartbeat route.
type localLinkHeartbeatRequest struct {
	GraceSeconds int `json:"grace_seconds"`
}

// localLinkChat resolves the chat of a local-link request and enforces the
// interaction/browser-token rules. It writes the error response and returns
// false when the request must not proceed.
func localLinkChat(w http.ResponseWriter, r *http.Request) (database.Chat, *gorm.DB, bool) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		writeChatErrorJSON(w, http.StatusBadRequest, "Unable to get database or user")
		return database.Chat{}, nil, false
	}
	chatUUID, chatUUIDErr := parseChatUUID(r)
	if chatUUIDErr != nil {
		writeChatErrorJSON(w, http.StatusBadRequest, "Invalid chat UUID")
		return database.Chat{}, nil, false
	}
	chat, err := findAccessibleChat(DB, user, chatUUID)
	if err != nil {
		writeChatErrorJSON(w, http.StatusNotFound, "Chat not found")
		return database.Chat{}, nil, false
	}
	if !enforceBrowserTokenInteractionChat(w, r, chat.ChatType) {
		return database.Chat{}, nil, false
	}
	return chat, DB, true
}

// PostLocalLinkHeartbeat arms or refreshes the local-link lease of a chat.
//
//	@Summary      Report a live local link
//	@Description  Refresh the local-link lease of an interaction so it is not stopped while the TUI is attached
//	@Tags         chats
//	@Accept       json
//	@Produce      json
//	@Param        chat_uuid path string true "Chat UUID"
//	@Success      200 {object} LocalLinkStatusResponse "Local link lease"
//	@Failure      400 {string} string "Invalid chat UUID"
//	@Failure      404 {string} string "Chat not found"
//	@Router       /api/v1/chats/{chat_uuid}/link/heartbeat [post]
func (h *ChatsHandler) PostLocalLinkHeartbeat(w http.ResponseWriter, r *http.Request) {
	chat, _, ok := localLinkChat(w, r)
	if !ok {
		return
	}
	request := localLinkHeartbeatRequest{}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&request)
	}
	now := time.Now()
	lease := TouchLocalLinkLease(chat.UUID, now, time.Duration(request.GraceSeconds)*time.Second)
	writeLocalLinkStatus(w, chat.UUID, lease, now, false)
}

// PostLocalLinkEnd reports that the local link ended (the TUI is shutting down
// with the ssh session) and stops the interaction immediately.
//
//	@Summary      Report the end of a local link
//	@Description  Stop the interaction because the local link that drove it ended
//	@Tags         chats
//	@Produce      json
//	@Param        chat_uuid path string true "Chat UUID"
//	@Success      200 {object} LocalLinkStatusResponse "Local link lease"
//	@Failure      400 {string} string "Invalid chat UUID"
//	@Failure      404 {string} string "Chat not found"
//	@Router       /api/v1/chats/{chat_uuid}/link/end [post]
func (h *ChatsHandler) PostLocalLinkEnd(w http.ResponseWriter, r *http.Request) {
	chat, DB, ok := localLinkChat(w, r)
	if !ok {
		return
	}
	EndLocalLinkLease(chat.UUID)
	inspector, _ := util.GetAsynqInspector(r)
	StopInteractionForLocalLinkLoss(DB, inspector, chat, localLinkStopReason)
	writeLocalLinkStatus(w, chat.UUID, localLinkLease{ChatUUID: chat.UUID, LastSeen: time.Now()}, time.Now(), true)
}

// GetLocalLinkStatus reports the local-link lease of a chat.
//
//	@Summary      Get the local link lease
//	@Description  Report whether a local link is currently keeping the interaction alive
//	@Tags         chats
//	@Produce      json
//	@Param        chat_uuid path string true "Chat UUID"
//	@Success      200 {object} LocalLinkStatusResponse "Local link lease"
//	@Failure      400 {string} string "Invalid chat UUID"
//	@Failure      404 {string} string "Chat not found"
//	@Router       /api/v1/chats/{chat_uuid}/link [get]
func (h *ChatsHandler) GetLocalLinkStatus(w http.ResponseWriter, r *http.Request) {
	chat, _, ok := localLinkChat(w, r)
	if !ok {
		return
	}
	now := time.Now()
	lease, armed, expired := LocalLinkLeaseStatus(chat.UUID, now)
	if !armed {
		writeLocalLinkStatus(w, chat.UUID, localLinkLease{ChatUUID: chat.UUID}, now, false)
		return
	}
	writeLocalLinkStatus(w, chat.UUID, lease, now, expired)
}

// writeLocalLinkStatus renders a lease as the route response.
func writeLocalLinkStatus(w http.ResponseWriter, chatUUID string, lease localLinkLease, now time.Time, stopped bool) {
	response := LocalLinkStatusResponse{
		ChatUUID:     chatUUID,
		Active:       !lease.LastSeen.IsZero() && !lease.expired(now),
		Expired:      !lease.LastSeen.IsZero() && lease.expired(now),
		GraceSeconds: int(lease.Grace / time.Second),
		Stopped:      stopped,
	}
	if response.GraceSeconds == 0 {
		response.GraceSeconds = int(DefaultLocalLinkGrace / time.Second)
	}
	if !lease.LastSeen.IsZero() {
		response.LastSeen = lease.LastSeen.UTC().Format(time.RFC3339)
		if remaining := lease.expiresAt().Sub(now); remaining > 0 {
			response.ExpiresInSecond = int(remaining / time.Second)
		}
	}
	if stopped {
		response.StopReason = localLinkStopReason
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
