package serve

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"tronbyt-server/internal/pixletcache"

	"github.com/tronbyt/pixlet/runtime"
)

func initCache(redisURL string) (*pixletcache.BypassCache, error) {
	// Initialize Pixlet Cache
	var inner runtime.Cache
	var err error

	if redisURL != "" {
		slog.Info("Initializing Pixlet Redis cache", "url", redisURL)

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		if inner, err = runtime.NewRedisCache(ctx, redisURL); err != nil {
			return nil, fmt.Errorf("failed to connect to Redis: %w", err)
		}
	} else {
		slog.Info("Initializing Pixlet in-memory cache")
		inner = runtime.NewInMemoryCache()
	}

	// Wrap so the server can force a one-shot refetch of an app's cached HTTP
	// responses (used by the schema "Refresh options" endpoint).
	cache := pixletcache.New(inner)

	runtime.InitHTTP(cache)
	runtime.InitCache(cache)
	return cache, nil
}
