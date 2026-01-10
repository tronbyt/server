package data

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// GORMSlogLogger wraps slog for GORM logging.
type GORMSlogLogger struct {
	LogLevel                  logger.LogLevel
	SlowThreshold             time.Duration
	IgnoreRecordNotFoundError bool
}

// LogMode log mode.
func (l *GORMSlogLogger) LogMode(level logger.LogLevel) logger.Interface {
	newLogger := *l
	newLogger.LogLevel = level
	return &newLogger
}

// Info prints info.
func (l *GORMSlogLogger) Info(ctx context.Context, msg string, data ...any) {
	if l.LogLevel >= logger.Info {
		slog.InfoContext(ctx, msg, data...)
	}
}

// Warn prints warn messages.
func (l *GORMSlogLogger) Warn(ctx context.Context, msg string, data ...any) {
	if l.LogLevel >= logger.Warn {
		slog.WarnContext(ctx, msg, data...)
	}
}

// Error prints error messages.
func (l *GORMSlogLogger) Error(ctx context.Context, msg string, data ...any) {
	if l.LogLevel >= logger.Error {
		slog.ErrorContext(ctx, msg, data...)
	}
}

// Trace prints trace messages.
func (l *GORMSlogLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.LogLevel <= logger.Silent {
		return
	}

	elapsed := time.Since(begin)
	sql, rows := fc()
	fields := []any{
		slog.Duration("elapsed", elapsed),
		slog.String("sql", sql),
		slog.Int64("rows", rows),
	}

	if err != nil && l.LogLevel >= logger.Error {
		if l.IgnoreRecordNotFoundError && errors.Is(err, gorm.ErrRecordNotFound) {
			return
		}
		slog.ErrorContext(ctx, "GORM query error", append(fields, slog.Any("error", err))...)
	} else if elapsed > l.SlowThreshold && l.SlowThreshold != 0 && l.LogLevel >= logger.Warn {
		slog.WarnContext(ctx, "GORM slow query", fields...)
	} else if l.LogLevel >= logger.Info { // GORM logger.Info maps to slog.LevelDebug for SQL
		slog.DebugContext(ctx, "GORM query", fields...)
	}
}

// NewGORMSlogLogger creates a new GORM logger that uses slog.
func NewGORMSlogLogger(slogLevel logger.LogLevel, slowThreshold time.Duration, ignoreRecordNotFoundError bool) logger.Interface {
	return &GORMSlogLogger{
		LogLevel:                  slogLevel,
		SlowThreshold:             slowThreshold,
		IgnoreRecordNotFoundError: ignoreRecordNotFoundError,
	}
}
