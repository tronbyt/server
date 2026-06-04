package pixletcache

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCache is a minimal in-memory runtime.Cache for testing.
type fakeCache struct {
	data map[string][]byte
}

func newFakeCache() *fakeCache { return &fakeCache{data: map[string][]byte{}} }

func (f *fakeCache) Get(_ context.Context, key string) ([]byte, bool, error) {
	v, ok := f.data[key]
	return v, ok, nil
}

func (f *fakeCache) Set(_ context.Context, key string, value []byte, _ int64) error {
	f.data[key] = value
	return nil
}

func (f *fakeCache) Close() {}

func TestBypassCacheDelegatesWhenNotBypassed(t *testing.T) {
	inner := newFakeCache()
	c := New(inner)
	ctx := context.Background()

	require.NoError(t, c.Set(ctx, "httpcache:soccermens.star:abc", []byte("cached"), 60))

	v, found, err := c.Get(ctx, "httpcache:soccermens.star:abc")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, []byte("cached"), v)
}

func TestBypassForcesMissForMatchingPrefixOnly(t *testing.T) {
	inner := newFakeCache()
	c := New(inner)
	ctx := context.Background()

	require.NoError(t, c.Set(ctx, "httpcache:soccermens.star:abc", []byte("soccer"), 60))
	require.NoError(t, c.Set(ctx, "httpcache:other.star:xyz", []byte("other"), 60))

	err := c.WithBypass("httpcache:soccermens.star:", func() error {
		// Matching key is forced to miss so the caller refetches.
		_, found, _ := c.Get(ctx, "httpcache:soccermens.star:abc")
		assert.False(t, found, "matching key should miss while bypassed")

		// A different app's entries are unaffected.
		_, found, _ = c.Get(ctx, "httpcache:other.star:xyz")
		assert.True(t, found, "non-matching key should still hit")
		return nil
	})
	require.NoError(t, err)

	// Once the bypass window closes, reads hit the cache again.
	_, found, _ := c.Get(ctx, "httpcache:soccermens.star:abc")
	assert.True(t, found, "key should hit again after bypass ends")
}

func TestBypassRefcountKeepsActiveUntilAllReleased(t *testing.T) {
	inner := newFakeCache()
	c := New(inner)
	ctx := context.Background()
	require.NoError(t, c.Set(ctx, "httpcache:app:1", []byte("v"), 60))

	const prefix = "httpcache:app:"
	c.add(prefix)
	c.add(prefix)

	_, found, _ := c.Get(ctx, "httpcache:app:1")
	assert.False(t, found, "bypass active with two holders")

	c.remove(prefix)
	_, found, _ = c.Get(ctx, "httpcache:app:1")
	assert.False(t, found, "bypass still active after one release")

	c.remove(prefix)
	_, found, _ = c.Get(ctx, "httpcache:app:1")
	assert.True(t, found, "bypass cleared after all releases")
}

func TestBypassRestoredWhenFnPanics(t *testing.T) {
	inner := newFakeCache()
	c := New(inner)
	ctx := context.Background()
	require.NoError(t, c.Set(ctx, "httpcache:app:1", []byte("v"), 60))

	assert.Panics(t, func() {
		_ = c.WithBypass("httpcache:app:", func() error {
			panic("boom")
		})
	})

	// defer in WithBypass must have released the bypass even on panic.
	_, found, _ := c.Get(ctx, "httpcache:app:1")
	assert.True(t, found, "bypass should be released after panic")
}
