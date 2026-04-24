package data

import (
	"log/slog"
	"strings"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/plugin/prometheus"
)

func Open(dsn, logLevel string) (*gorm.DB, error) {
	var db *gorm.DB
	var err error

	var gormLogLevel logger.LogLevel
	switch strings.ToUpper(logLevel) {
	case "DEBUG":
		gormLogLevel = logger.Info // GORM Info includes SQL queries
	case "INFO":
		gormLogLevel = logger.Warn
	case "WARN", "WARNING":
		gormLogLevel = logger.Error
	case "ERROR":
		gormLogLevel = logger.Error
	default:
		gormLogLevel = logger.Warn
	}

	gormConfig := &gorm.Config{
		Logger:      NewGORMSlogLogger(gormLogLevel, 200*time.Millisecond, true),
		PrepareStmt: false, // Disable prepared statement caching to avoid SQLite locking issues
	}

	if strings.HasPrefix(dsn, "postgres") || strings.Contains(dsn, "host=") {
		slog.Info("Using Postgres DB")
		db, err = gorm.Open(postgres.Open(dsn), gormConfig)
	} else if strings.Contains(dsn, "@tcp(") || strings.Contains(dsn, "@unix(") {
		slog.Info("Using MySQL DB")
		db, err = gorm.Open(mysql.Open(dsn), gormConfig)
	} else {
		slog.Info("Using SQLite DB", "path", dsn)
		db, err = gorm.Open(sqlite.Open(dsn), gormConfig)
		if err == nil {
			if err := db.Exec("PRAGMA journal_mode=WAL;").Error; err != nil {
				slog.Warn("Failed to set WAL mode for SQLite", "error", err)
			}
			if err := db.Exec("PRAGMA busy_timeout=5000;").Error; err != nil {
				slog.Warn("Failed to set busy timeout for SQLite", "error", err)
			}
		}
	}

	if err == nil {
		if err := db.Use(prometheus.New(prometheus.Config{
			DBName:          "tronbyt",
			RefreshInterval: 15,
			StartServer:     false,
			HTTPServerPort:  8080,
		})); err != nil {
			slog.Warn("Failed to register GORM prometheus plugin", "error", err)
		}
	}

	return db, err
}
