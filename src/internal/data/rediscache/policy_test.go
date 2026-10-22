package rediscache

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/luck/nexus-authorization/internal/config"
)

func testCache(t *testing.T) (*PolicyCache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	cache, err := New(config.RedisConfig{URL: "redis://" + server.Addr(), KeyPrefix: "nexusauthorization:test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	return cache, server
}
