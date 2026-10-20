package database

// ci#645: GORM used to log through its own default logger, which writes
// straight to stdout and ignores LOG_LEVEL entirely. Every statement it
// decided to print (slow queries, statement errors, and - with Debug() -
// every single query) therefore landed in the deployment logs no matter how
// the process was configured.
//
// gormLogger routes that SQL tracer through the process-wide leveled logger
// instead:
//
//	LOG_LEVEL=debug -> every statement (rows + duration) as debug
//	LOG_LEVEL=info  -> only statement failures/slow queries as warn/error
//	LOG_LEVEL=off   -> nothing at all
//
// GORM's own level scale is derived from the configured level in gormLogLevel,
// and callers that historically forced query logging keep that behaviour via
// gormLoggerFor(true).

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"backend/logging"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type gormLogger struct {
	level logger.LogLevel
}

// newGormLogger returns a logger.Interface that follows the configured level.
func newGormLogger() logger.Interface {
	return &gormLogger{level: gormLogLevel()}
}

// gormLoggerFor returns the GORM logger, forcing statement tracing when
// forceInfo is set (legacy DBConfig.Debug / --debug).
func gormLoggerFor(forceInfo bool) logger.Interface {
	if forceInfo {
		return &gormLogger{level: logger.Info}
	}
	return newGormLogger()
}

// gormLogLevel maps the process log level onto GORM's own level scale.
func gormLogLevel() logger.LogLevel {
	switch {
	case logging.DebugEnabled():
		return logger.Info
	case logging.Enabled(slog.LevelWarn):
		return logger.Warn
	case logging.Enabled(slog.LevelError):
		return logger.Error
	default:
		return logger.Silent
	}
}

func (l *gormLogger) LogMode(level logger.LogLevel) logger.Interface {
	clone := *l
	clone.level = level
	return &clone
}

// ParamsFilter is part of gorm's logger.Interface as of v1.30; parameters are
// always passed through unchanged.
func (l *gormLogger) ParamsFilter(_ context.Context, sql string, params ...interface{}) (string, []interface{}) {
	return sql, params
}

func (l *gormLogger) Info(_ context.Context, msg string, args ...interface{}) {
	if l.level >= logger.Info {
		logf := logging.Debugf
		logf("gorm: "+msg, args...)
	}
}

func (l *gormLogger) Warn(_ context.Context, msg string, args ...interface{}) {
	if l.level >= logger.Warn {
		logging.Warnf("gorm: "+msg, args...)
	}
}

func (l *gormLogger) Error(_ context.Context, msg string, args ...interface{}) {
	if l.level >= logger.Error {
		logging.Errorf("gorm: "+msg, args...)
	}
}

func (l *gormLogger) Trace(_ context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.level <= logger.Silent {
		return
	}
	sql, rows := fc()
	elapsed := time.Since(begin).Round(time.Millisecond)
	// gorm.ErrRecordNotFound is normal control flow (a First/Find miss) and
	// would otherwise flood the logs of every bot lookup.
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		if l.level >= logger.Error {
			logging.Errorf("gorm: %s [rows:%d elapsed:%s] %v", sql, rows, elapsed, err)
		}
		return
	}
	if l.level >= logger.Info && err == nil {
		logging.Debugf("gorm: %s [rows:%d elapsed:%s]", sql, rows, elapsed)
	}
}
