package database

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"backend/logging"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// configureTestLogging points the process logger at a fresh buffer.
func configureTestLogging(t *testing.T, level string) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	reconfigureTestLogging(t, buf, level)
	return buf
}

// reconfigureTestLogging reuses an existing buffer so a test can observe the
// output of a second configuration without losing its handle on the writer.
func reconfigureTestLogging(t *testing.T, buf *bytes.Buffer, level string) {
	t.Helper()
	if err := logging.Configure(logging.Options{Level: level, Format: "text", Writer: buf}); err != nil {
		t.Fatalf("configure logging: %v", err)
	}
	t.Cleanup(func() {
		_ = logging.Configure(logging.Options{Level: "info", Format: "text", Writer: io.Discard})
	})
}

func traceOnce(l logger.Interface, err error) {
	l.Trace(context.Background(), time.Now().Add(-time.Millisecond), func() (string, int64) {
		return "SELECT 1 FROM messages", 1
	}, err)
}

func TestGormLoggerTracesQueriesOnlyAtDebug(t *testing.T) {
	buf := configureTestLogging(t, "info")
	traceOnce(gormLoggerFor(false), nil)
	if strings.Contains(buf.String(), "SELECT 1") {
		t.Fatalf("info level must not trace statements, got %q", buf.String())
	}

	buf.Reset()
	reconfigureTestLogging(t, buf, "debug")
	traceOnce(gormLoggerFor(false), nil)
	if !strings.Contains(buf.String(), "SELECT 1") {
		t.Fatalf("debug level must trace statements, got %q", buf.String())
	}
}

func TestGormLoggerReportsStatementFailures(t *testing.T) {
	buf := configureTestLogging(t, "info")
	traceOnce(gormLoggerFor(false), errors.New("database is locked"))
	if !strings.Contains(buf.String(), "database is locked") {
		t.Fatalf("statement failures must be logged, got %q", buf.String())
	}
}

func TestGormLoggerIgnoresRecordNotFound(t *testing.T) {
	buf := configureTestLogging(t, "debug")
	traceOnce(gormLoggerFor(false), gorm.ErrRecordNotFound)
	if strings.Contains(buf.String(), "SELECT 1") {
		t.Fatalf("record-not-found must not be traced, got %q", buf.String())
	}
}

func TestGormLoggerSilentWhenLoggingOff(t *testing.T) {
	buf := configureTestLogging(t, "off")
	l := gormLoggerFor(false)
	traceOnce(l, errors.New("database is locked"))
	if buf.Len() != 0 {
		t.Fatalf("off must silence the sql tracer, got %q", buf.String())
	}
	if got := l.(*gormLogger).level; got != logger.Silent {
		t.Fatalf("off should map to logger.Silent, got %v", got)
	}
}

func TestGormLoggerForcedInfo(t *testing.T) {
	configureTestLogging(t, "warn")
	l := gormLoggerFor(true)
	if got := l.(*gormLogger).level; got != logger.Info {
		t.Fatalf("forced tracing should map to logger.Info, got %v", got)
	}
}
