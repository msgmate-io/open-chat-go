package database

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"backend/logging"

	"gorm.io/gorm"
)

// Message rows are the bulk of the SQLite volume. A long-lived chat keeps every
// message it ever exchanged - text, reasoning and the full tool-call payload -
// forever: nothing in the read paths ever deletes a message, and the UI only
// pages back through recent history anyway. On the single-file SQLite volume the
// deployment notes flag as a growth risk, that is the dominant source of growth
// (ci#850), so the prune below is the retention/archival half of the ci#850 work
// (the busy timeout + WAL maintenance and the hot-path indexes are the other
// half).
//
// The prune is deliberately conservative:
//
//   - a window (default 90 days) decides which messages are candidates at all;
//   - a per-chat floor (HotMessageFloorPerChat) always keeps the newest N
//     messages of every chat, so an old but active chat never loses its recent
//     context and `chats.latest_message_id` always survives;
//   - it deletes at most MessagePruneBatchSize rows per transaction, so a large
//     backlog is reclaimed incrementally instead of in one long write that would
//     hold the SQLite write lock;
//   - it runs off the request path, from the server process, on its own interval
//     (StartMessageRetentionSweeper), through RetryOnBusy like the session sweep.
//
// The delete is unscoped on purpose: Message embeds Model, so a plain Delete
// would only stamp `deleted_at` and leave the row's bytes behind - exactly the
// growth this exists to stop. Soft-deleted messages are invisible to every read
// path anyway.
const (
	// DefaultMessageRetentionWindow is how far back message history is kept when
	// OPEN_CHAT_MESSAGE_RETENTION_WINDOW is not set.
	DefaultMessageRetentionWindow = 90 * 24 * time.Hour

	// HotMessageFloorPerChat is the number of newest messages of a chat that are
	// never pruned, however old the chat is. It also guarantees the chat's
	// latest_message_id keeps pointing at a live row.
	HotMessageFloorPerChat = 200

	// DefaultMessagePruneBatchSize is how many rows one prune transaction
	// deletes, i.e. the unit of work (and of lock holding) on the hot database.
	DefaultMessagePruneBatchSize = 500

	// DefaultMessageRetentionInterval is how often the server runs a prune pass
	// when OPEN_CHAT_MESSAGE_RETENTION_INTERVAL is not set.
	DefaultMessageRetentionInterval = 6 * time.Hour

	// MessageRetentionWindowEnv overrides the retention window (a Go duration,
	// or `off` / `disabled` / `never` / `0` to disable pruning entirely).
	MessageRetentionWindowEnv = "OPEN_CHAT_MESSAGE_RETENTION_WINDOW"

	// MessageRetentionIntervalEnv overrides how often the prune runs.
	MessageRetentionIntervalEnv = "OPEN_CHAT_MESSAGE_RETENTION_INTERVAL"

	// MessagePruneBatchSizeEnv overrides the batch size (rows per transaction).
	MessagePruneBatchSizeEnv = "OPEN_CHAT_MESSAGE_PRUNE_BATCH_SIZE"
)

// MessageRetentionPolicy is the resolved pruning policy: what is old enough to
// prune, what is always kept and how much work one transaction does.
type MessageRetentionPolicy struct {
	// Window is the age beyond which a message becomes a prune candidate. A
	// non-positive window disables the prune.
	Window time.Duration
	// HotFloorPerChat is the number of newest messages of each chat that are
	// kept regardless of age.
	HotFloorPerChat int
	// BatchSize is the maximum number of rows deleted per transaction.
	BatchSize int
}

// DefaultMessageRetentionPolicy resolves the policy from the environment, i.e.
// the exact policy the server runs.
func DefaultMessageRetentionPolicy() MessageRetentionPolicy {
	return MessageRetentionPolicy{
		Window:          MessageRetentionWindow(),
		HotFloorPerChat: HotMessageFloorPerChat,
		BatchSize:       MessagePruneBatchSize(),
	}
}

// withDefaults fills in the non-negotiables of a partially specified policy
// (zero floor/batch, so a caller cannot accidentally prune every message of a
// chat or delete an unbounded number of rows at once).
func (p MessageRetentionPolicy) withDefaults() MessageRetentionPolicy {
	if p.HotFloorPerChat <= 0 {
		p.HotFloorPerChat = HotMessageFloorPerChat
	}
	if p.BatchSize <= 0 {
		p.BatchSize = DefaultMessagePruneBatchSize
	}
	return p
}

// MessageRetentionWindow resolves the retention window. An unset or unparsable
// value falls back to the default; an explicit `off`/`disabled`/`never`/`false`/
// `no` (or any non-positive duration) disables the prune.
func MessageRetentionWindow() time.Duration {
	return retentionDurationEnv(MessageRetentionWindowEnv, DefaultMessageRetentionWindow)
}

// MessageRetentionInterval resolves how often the server prunes. It follows the
// same off/disabled convention as the retention window.
func MessageRetentionInterval() time.Duration {
	return retentionDurationEnv(MessageRetentionIntervalEnv, DefaultMessageRetentionInterval)
}

// MessagePruneBatchSize resolves the rows-per-transaction batch size. An unset
// or unparsable value falls back to the default instead of silently changing
// behaviour.
func MessagePruneBatchSize() int {
	raw := strings.TrimSpace(os.Getenv(MessagePruneBatchSizeEnv))
	if raw == "" {
		return DefaultMessagePruneBatchSize
	}
	size, err := strconv.Atoi(raw)
	if err != nil || size <= 0 {
		logging.Infof("%s=%q is not a positive integer; falling back to %d",
			MessagePruneBatchSizeEnv, raw, DefaultMessagePruneBatchSize)
		return DefaultMessagePruneBatchSize
	}
	return size
}

// retentionDurationEnv reads a duration knob shared by the retention window and
// interval settings.
func retentionDurationEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}

	switch strings.ToLower(raw) {
	case "off", "disabled", "never", "false", "no":
		return 0
	}

	value, err := time.ParseDuration(raw)
	if err != nil {
		logging.Infof("%s=%q is not a duration; falling back to %s", name, raw, fallback)
		return fallback
	}
	if value <= 0 {
		return 0
	}
	return value
}

// DeleteExpiredMessages prunes message history older than policy.Window,
// keeping the newest policy.HotFloorPerChat messages of every chat, and returns
// how many rows it deleted. Each batch is one transaction (select the batch, then
// delete exactly those rows) retried on SQLite lock contention, and the loop
// stops when a batch comes back short - so a caller can run it with a bounded
// context and leave the rest for the next pass.
//
// A non-positive window (or a nil db) is a no-op, so a disabled policy never
// touches the database.
func DeleteExpiredMessages(ctx context.Context, db *gorm.DB, now time.Time, policy MessageRetentionPolicy) (int64, error) {
	if db == nil {
		return 0, nil
	}
	policy = policy.withDefaults()
	if policy.Window <= 0 {
		return 0, nil
	}

	cutoff := now.UTC().Add(-policy.Window)
	var total int64

	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}

		var deleted int
		err := RetryOnBusy(ctx, "message retention prune", func() error {
			var batchErr error
			deleted, batchErr = pruneMessageBatch(ctx, db, cutoff, policy)
			return batchErr
		})
		if err != nil {
			return total, err
		}

		total += int64(deleted)
		if deleted < policy.BatchSize {
			// A short batch means there is nothing left to prune; the next full
			// batch would only repeat the same scan.
			return total, nil
		}
	}
}

// pruneMessageBatch deletes up to policy.BatchSize prune candidates inside one
// transaction, so a reader either sees all of the batch or none of it.
func pruneMessageBatch(ctx context.Context, db *gorm.DB, cutoff time.Time, policy MessageRetentionPolicy) (int, error) {
	var deleted int

	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ids, err := selectPrunableMessageIDs(ctx, tx, cutoff, policy)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}

		// Unscoped: a soft delete would leave the row (and its bytes) behind.
		result := tx.WithContext(ctx).Unscoped().Where("id IN ?", ids).Delete(&Message{})
		if result.Error != nil {
			return result.Error
		}
		deleted = int(result.RowsAffected)
		return nil
	})

	return deleted, err
}

// selectPrunableMessageIDs returns the ids of up to policy.BatchSize messages
// that are older than cutoff and not part of their chat's hot floor.
//
// The floor subquery keeps the newest HotFloorPerChat messages of the *same*
// chat (`keep.chat_id = m.chat_id`, ordered by id DESC, which is the same order
// the chat message list pages in and the order `chats.latest_message_id` is
// written in). Both backends run LIMIT in a scalar subquery, and the `id IN ?`
// delete below reuses the primary key, so the delete is a key lookup even when
// the outer scan touched the created_at index.
func selectPrunableMessageIDs(ctx context.Context, tx *gorm.DB, cutoff time.Time, policy MessageRetentionPolicy) ([]uint, error) {
	const query = "SELECT m.id FROM messages m " +
		"WHERE m.deleted_at IS NULL AND m.created_at < ? " +
		"AND m.id NOT IN (SELECT keep.id FROM messages keep " +
		"WHERE keep.chat_id = m.chat_id AND keep.deleted_at IS NULL " +
		"ORDER BY keep.id DESC LIMIT ?) " +
		"ORDER BY m.id ASC LIMIT ?"

	var ids []uint
	if err := tx.WithContext(ctx).Raw(query, cutoff, policy.HotFloorPerChat, policy.BatchSize).Scan(&ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// RunMessageRetentionSweeper prunes once and then every interval until ctx is
// done. A deleted batch leaves free pages inside the SQLite file, so a pass that
// actually removed rows ends with a truncating WAL checkpoint - that is what
// returns the space to the filesystem instead of only to the freelist.
func RunMessageRetentionSweeper(ctx context.Context, db *gorm.DB, interval time.Duration, policy MessageRetentionPolicy) {
	if db == nil || interval <= 0 || policy.withDefaults().Window <= 0 {
		return
	}
	policy = policy.withDefaults()

	pruneMessagesOnce(ctx, db, policy)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pruneMessagesOnce(ctx, db, policy)
		}
	}
}

// pruneMessagesOnce runs one retention pass and logs its outcome. A pass that
// deleted rows is followed by a WAL checkpoint; a cancelled context is a normal
// shutdown, not a failure.
func pruneMessagesOnce(ctx context.Context, db *gorm.DB, policy MessageRetentionPolicy) {
	deleted, err := DeleteExpiredMessages(ctx, db, time.Now().UTC(), policy)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		logging.Infof("message retention prune failed: %v", err)
		return
	}
	if deleted == 0 {
		return
	}

	logging.Infof("message retention prune removed %d message(s) older than %s (kept the newest %d per chat)",
		deleted, policy.Window, policy.HotFloorPerChat)
	if err := CheckpointWAL(db); err != nil {
		logging.Infof("message retention WAL checkpoint failed: %v", err)
	}
}

// StartMessageRetentionSweeper keeps the retention prune running in the
// background and returns the stop function the caller must defer. It waits for
// the goroutine to finish before returning, so no prune can outlive the server
// (the ci#811 teardown contract: the goroutine is drained by its own stop func).
func StartMessageRetentionSweeper(db *gorm.DB, interval time.Duration, policy MessageRetentionPolicy) func() {
	if db == nil || interval <= 0 || policy.withDefaults().Window <= 0 {
		return func() {}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunMessageRetentionSweeper(ctx, db, interval, policy)
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}
