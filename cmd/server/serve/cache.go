package serve

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/tronbyt/pixlet/runtime"
)

func initCache(redisURL string) (runtime.Cache, error) {
	// Initialize Pixlet Cache
	var cache runtime.Cache
	var err error

	if redisURL != "" {
		slog.Info("Initializing Pixlet Redis cache", "url", redisURL)

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		if cache, err = runtime.NewRedisCache(ctx, redisURL); err != nil {
			return nil, fmt.Errorf("failed to connect to Redis: %w", err)
		}
	} else {
		slog.Info("Initializing Pixlet in-memory cache")
		cache = runtime.NewInMemoryCache()
	}

	runtime.InitHTTP(cache)
	runtime.InitCache(cache)
	return cache, nil
}
