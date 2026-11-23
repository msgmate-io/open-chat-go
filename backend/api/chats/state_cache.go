package chats

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"backend/database"
)

// Short-lived cache of resolved interaction states.
//
// Why this exists: resolving one chat's state is not free - it reads the
// transcript tail, the shared config, the auto-continue state and (for
// interaction chats) the stall guard, i.e. several queries per chat. The
// sidebar resolves up to 100 chats per /api/v1/chats/states poll AND per
// /api/v1/chats/list page, and every poll re-derived states that had not
// changed since the previous one. On ci.msgmate.io that turned into hundreds of
// statements per poll and multi-second list loads.
//
// The cache trades a bounded staleness window for that cost: a resolved state
// is reused for OCI_CHAT_STATE_CACHE_MS (default 3000, 0 disables the cache and
// restores the previous behaviour). The UI polls on the order of seconds, so a
// badge can lag a state transition by at most that window - which is smaller
// than the poll interval the client already tolerates.
const defaultChatStateCacheMS = 3000

// chatStateCacheMaxEntries bounds the cache: it is keyed by chat UUID and only
// keeps states long enough to cover the current poll burst.
const chatStateCacheMaxEntries = 4096

type chatStateCacheEntry struct {
	row       ChatStateRow
	expiresAt time.Time
}

type chatStateCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]chatStateCacheEntry
	now     func() time.Time
}

func newChatStateCache(ttl time.Duration) *chatStateCache {
	return &chatStateCache{
		ttl:     ttl,
		entries: make(map[string]chatStateCacheEntry),
		now:     time.Now,
	}
}

// chatStateCacheTTL reads OCI_CHAT_STATE_CACHE_MS. An unset, unparsable or
// negative value falls back to the default; 0 disables the cache.
func chatStateCacheTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv("OCI_CHAT_STATE_CACHE_MS"))
	if raw == "" {
		return defaultChatStateCacheMS * time.Millisecond
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms < 0 {
		return defaultChatStateCacheMS * time.Millisecond
	}
	return time.Duration(ms) * time.Millisecond
}

var interactionStateCache = newChatStateCache(chatStateCacheTTL())

func (c *chatStateCache) get(uuid string) (ChatStateRow, bool) {
	if c == nil || c.ttl <= 0 || uuid == "" {
		return ChatStateRow{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[uuid]
	if !ok {
		return ChatStateRow{}, false
	}
	if !c.now().Before(entry.expiresAt) {
		delete(c.entries, uuid)
		return ChatStateRow{}, false
	}
	return entry.row, true
}

func (c *chatStateCache) put(row ChatStateRow) {
	if c == nil || c.ttl <= 0 || row.ChatUUID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= chatStateCacheMaxEntries {
		now := c.now()
		for uuid, entry := range c.entries {
			if !now.Before(entry.expiresAt) {
				delete(c.entries, uuid)
			}
		}
	}
	c.entries[row.ChatUUID] = chatStateCacheEntry{row: row, expiresAt: c.now().Add(c.ttl)}
}

// cachedChatStates returns the still-fresh states of the given chats, keyed by
// chat UUID. Misses are simply absent from the map.
func cachedChatStates(chats []database.Chat) map[string]ChatStateRow {
	out := make(map[string]ChatStateRow, len(chats))
	for _, chat := range chats {
		if row, ok := interactionStateCache.get(chat.UUID); ok {
			out[chat.UUID] = row
		}
	}
	return out
}

// storeChatState remembers a freshly resolved state.
func storeChatState(row ChatStateRow) {
	interactionStateCache.put(row)
}
