// Package logging installs the process-wide leveled logger for open-chat.
//
// It is a thin layer on top of the standard library log/slog so the whole
// backend (server, worker and bootstrap commands) shares one destination,
// format and level. The level is controlled by LOG_LEVEL
// (OPEN_CHAT_LOG_LEVEL is accepted as a legacy alias) or the --log-level flag
// of the server command; the format is controlled by LOG_FORMAT
// (text, the default, or json).
//
// Legacy call sites that still use the standard log package are bridged
// through the same handler at info level, so a single LOG_LEVEL also silences
// them without touching every file.
package logging

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
)

// Environment variables understood by ConfigureFromEnv.
const (
	EnvLogLevel       = "LOG_LEVEL"
	EnvLogLevelLegacy = "OPEN_CHAT_LOG_LEVEL"
	EnvLogFormat      = "LOG_FORMAT"
	EnvDebugLegacy    = "DEBUG"
)

// Silent discards every record, including errors.
const Silent slog.Level = 12

// Options configure Configure.
type Options struct {
	// Level is one of debug, info, warn, error or off. Empty means info.
	Level string
	// Format is text (default) or json.
	Format string
	// Writer is the destination; defaults to os.Stdout.
	Writer io.Writer
}

// ParseLevel maps a textual level to a slog.Level. An empty value means info.
func ParseLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return slog.LevelInfo, nil
	case "debug", "trace":
		return slog.LevelDebug, nil
	case "info", "notice":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error", "err":
		return slog.LevelError, nil
	case "off", "none", "silent":
		return Silent, nil
	}
	return slog.LevelInfo, fmt.Errorf("unknown log level %q (want debug, info, warn, error or off)", raw)
}

// Configure installs the process-wide logger and bridges the standard library
// log package through the same handler.
func Configure(opts Options) error {
	level, err := ParseLevel(opts.Level)
	if err != nil {
		return err
	}
	writer := opts.Writer
	if writer == nil {
		writer = os.Stdout
	}
	handlerOpts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	switch strings.ToLower(strings.TrimSpace(opts.Format)) {
	case "", "text", "logfmt", "console":
		handler = slog.NewTextHandler(writer, handlerOpts)
	case "json":
		handler = slog.NewJSONHandler(writer, handlerOpts)
	default:
		return fmt.Errorf("unknown log format %q (want text or json)", opts.Format)
	}

	slog.SetDefault(slog.New(handler))

	// Legacy log.Printf/Println call sites: route them through the same
	// handler so LOG_LEVEL and the selected format apply to them as well.
	log.SetOutput(slog.NewLogLogger(handler, slog.LevelInfo).Writer())
	log.SetFlags(0)
	log.SetPrefix("")
	return nil
}

// ConfigureFromEnv applies LOG_LEVEL/OPEN_CHAT_LOG_LEVEL and LOG_FORMAT.
func ConfigureFromEnv() error {
	level := strings.TrimSpace(os.Getenv(EnvLogLevel))
	if level == "" {
		level = strings.TrimSpace(os.Getenv(EnvLogLevelLegacy))
	}
	// Legacy compatibility: the historic DEBUG flag (default true in the
	// server/worker commands) used to mean "verbose". LOG_LEVEL always wins.
	if level == "" {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvDebugLegacy))) {
		case "1", "true", "yes", "on":
			level = "debug"
		case "0", "false", "no", "off":
			level = "info"
		}
	}
	return Configure(Options{Level: level, Format: os.Getenv(EnvLogFormat)})
}

// Enabled reports whether the process-wide logger emits records at level.
// Packages that own their own logging knobs (the GORM SQL tracer, for example)
// use it to follow LOG_LEVEL without importing log/slog themselves.
func Enabled(level slog.Level) bool {
	return slog.Default().Enabled(context.Background(), level)
}

// DebugEnabled reports whether debug records are emitted.
func DebugEnabled() bool {
	return Enabled(slog.LevelDebug)
}

// Debugf logs a printf-style debug record.
func Debugf(format string, args ...any) { logf(slog.LevelDebug, format, args) }

// Infof logs a printf-style info record.
func Infof(format string, args ...any) { logf(slog.LevelInfo, format, args) }

// Warnf logs a printf-style warning record.
func Warnf(format string, args ...any) { logf(slog.LevelWarn, format, args) }

// Errorf logs a printf-style error record.
func Errorf(format string, args ...any) { logf(slog.LevelError, format, args) }

// Debug logs a structured debug record.
func Debug(msg string, args ...any) { logArgs(slog.LevelDebug, msg, args) }

// Info logs a structured info record.
func Info(msg string, args ...any) { logArgs(slog.LevelInfo, msg, args) }

// Warn logs a structured warning record.
func Warn(msg string, args ...any) { logArgs(slog.LevelWarn, msg, args) }

// Error logs a structured error record.
func Error(msg string, args ...any) { logArgs(slog.LevelError, msg, args) }

func logf(level slog.Level, format string, args []any) {
	logger := slog.Default()
	ctx := context.Background()
	if !logger.Enabled(ctx, level) {
		return
	}
	// Call sites migrated from log.Printf/log.Println frequently end their
	// format string with a newline; slog emits one record per call, so a
	// single trailing newline is dropped to avoid blank lines in the output.
	msg := strings.TrimSuffix(fmt.Sprintf(format, args...), "\n")
	logger.Log(ctx, level, msg)
}

func logArgs(level slog.Level, msg string, args []any) {
	logger := slog.Default()
	ctx := context.Background()
	if !logger.Enabled(ctx, level) {
		return
	}
	logger.Log(ctx, level, msg, args...)
}
