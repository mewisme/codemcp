package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"go.mewis.me/codemcp/internal/state"
)

const (
	CacheSchema     = 1
	DefaultCacheTTL = 24 * time.Hour
)

var ErrCacheNotFound = errors.New("update cache not found")

type Cache struct {
	Schema    int       `json:"schema"`
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

type CachedCheck struct {
	CheckResult
	CheckedAt time.Time
}

func (c Cache) Validate() error {
	if c.Schema != CacheSchema {
		return fmt.Errorf("unsupported update cache schema %d", c.Schema)
	}
	if c.CheckedAt.IsZero() {
		return errors.New("update cache checked_at is required")
	}
	latest, err := NormalizeVersion(c.Latest)
	if err != nil {
		return fmt.Errorf("update cache latest: %w", err)
	}
	if latest != c.Latest {
		return fmt.Errorf("update cache latest is not normalized: %s", c.Latest)
	}
	return nil
}

func (c Cache) Fresh(now time.Time, ttl time.Duration) bool {
	if ttl <= 0 || c.CheckedAt.IsZero() || now.Before(c.CheckedAt) {
		return false
	}
	return now.Sub(c.CheckedAt) <= ttl
}

func ReadCache(path string) (Cache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Cache{}, ErrCacheNotFound
		}
		return Cache{}, err
	}
	var cache Cache
	if err := json.Unmarshal(data, &cache); err != nil {
		return Cache{}, fmt.Errorf("decode update cache: %w", err)
	}
	if err := cache.Validate(); err != nil {
		return Cache{}, err
	}
	return cache, nil
}

func WriteCache(path, latest string, checkedAt time.Time) error {
	latest, err := NormalizeVersion(latest)
	if err != nil {
		return err
	}
	cache := Cache{Schema: CacheSchema, CheckedAt: checkedAt.UTC(), Latest: latest}
	if err := cache.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	return state.WriteFileAtomic(path, append(data, '\n'), 0600)
}

func ReadFreshCache(path, current string, now time.Time, ttl time.Duration) (CachedCheck, bool, error) {
	cache, err := ReadCache(path)
	if errors.Is(err, ErrCacheNotFound) {
		return CachedCheck{}, false, nil
	}
	if err != nil {
		return CachedCheck{}, false, err
	}
	if !cache.Fresh(now, ttl) {
		return CachedCheck{}, false, nil
	}
	result, err := checkRelease(current, Release{Version: cache.Latest})
	if err != nil {
		return CachedCheck{}, false, err
	}
	return CachedCheck{CheckResult: result, CheckedAt: cache.CheckedAt}, true, nil
}
