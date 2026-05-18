package skills

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiskCache_FetchAndStore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "max-age=3600")
		fmt.Fprint(w, "file content")
	}))
	defer srv.Close()

	cache := newDiskCache(t.TempDir())

	content, err := cache.FetchAndStore(t.Context(), "https://example.com", "my-skill", "SKILL.md", srv.URL+"/SKILL.md")
	require.NoError(t, err)
	assert.Equal(t, "file content", content)

	// Verify it was written to disk
	filePath := filepath.Join(cache.cacheDir("https://example.com", "my-skill"), "SKILL.md")
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, "file content", string(data))

	// Verify metadata was written
	metaPath := filePath + ".meta"
	_, err = os.Stat(metaPath)
	require.NoError(t, err)
}

func TestDiskCache_Get_NotCached(t *testing.T) {
	cache := newDiskCache(t.TempDir())

	_, ok := cache.Get("https://example.com", "nonexistent", "SKILL.md")
	assert.False(t, ok)
}

func TestDiskCache_Get_Cached(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "max-age=3600")
		fmt.Fprint(w, "cached content")
	}))
	defer srv.Close()

	cache := newDiskCache(t.TempDir())

	_, err := cache.FetchAndStore(t.Context(), "https://example.com", "skill", "SKILL.md", srv.URL+"/SKILL.md")
	require.NoError(t, err)

	content, ok := cache.Get("https://example.com", "skill", "SKILL.md")
	assert.True(t, ok)
	assert.Equal(t, "cached content", content)
}

func TestDiskCache_Get_Expired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "max-age=0")
		fmt.Fprint(w, "expired content")
	}))
	defer srv.Close()

	cache := newDiskCache(t.TempDir())

	_, err := cache.FetchAndStore(t.Context(), "https://example.com", "skill", "SKILL.md", srv.URL+"/SKILL.md")
	require.NoError(t, err)

	// The max-age=0 should make it immediately expired
	_, ok := cache.Get("https://example.com", "skill", "SKILL.md")
	assert.False(t, ok)
}

func TestDiskCache_NestedFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "nested file content")
	}))
	defer srv.Close()

	cache := newDiskCache(t.TempDir())

	content, err := cache.FetchAndStore(t.Context(), "https://example.com", "my-skill", "references/FORMS.md", srv.URL+"/file")
	require.NoError(t, err)
	assert.Equal(t, "nested file content", content)

	// Verify the nested directory was created
	filePath := filepath.Join(cache.cacheDir("https://example.com", "my-skill"), "references", "FORMS.md")
	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, "nested file content", string(data))
}

func TestDiskCache_DifferentURLsGetDifferentDirs(t *testing.T) {
	cache := newDiskCache(t.TempDir())

	dir1 := cache.cacheDir("https://example.com", "skill")
	dir2 := cache.cacheDir("https://other.com", "skill")

	assert.NotEqual(t, dir1, dir2)
}

func TestParseCacheControl(t *testing.T) {
	now := time.Now()

	t.Run("empty header uses default", func(t *testing.T) {
		d := parseCacheControl("")
		assert.False(t, d.noStore)
		assert.False(t, d.noCache)
		assert.WithinDuration(t, now.Add(1*time.Hour), d.expiresAt(), 2*time.Second)
	})

	t.Run("max-age=3600", func(t *testing.T) {
		d := parseCacheControl("max-age=3600")
		assert.True(t, d.hasMaxAge)
		assert.WithinDuration(t, now.Add(3600*time.Second), d.expiresAt(), 2*time.Second)
	})

	t.Run("max-age=0", func(t *testing.T) {
		d := parseCacheControl("max-age=0")
		assert.True(t, d.hasMaxAge)
		assert.WithinDuration(t, now, d.expiresAt(), 2*time.Second)
	})

	t.Run("no-store", func(t *testing.T) {
		d := parseCacheControl("no-store")
		assert.True(t, d.noStore)
	})

	t.Run("no-cache forces immediate expiry", func(t *testing.T) {
		d := parseCacheControl("no-cache")
		assert.True(t, d.noCache)
		assert.WithinDuration(t, now, d.expiresAt(), 2*time.Second)
	})

	t.Run("no-cache wins over max-age", func(t *testing.T) {
		d := parseCacheControl("max-age=3600, no-cache")
		assert.True(t, d.noCache)
		assert.WithinDuration(t, now, d.expiresAt(), 2*time.Second)
	})

	t.Run("multiple directives with max-age", func(t *testing.T) {
		d := parseCacheControl("public, max-age=7200")
		assert.WithinDuration(t, now.Add(7200*time.Second), d.expiresAt(), 2*time.Second)
	})

	t.Run("unknown directives use default", func(t *testing.T) {
		d := parseCacheControl("public")
		assert.WithinDuration(t, now.Add(1*time.Hour), d.expiresAt(), 2*time.Second)
	})
}

func TestDiskCache_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	cache := newDiskCache(t.TempDir())

	_, err := cache.FetchAndStore(t.Context(), "https://example.com", "skill", "SKILL.md", srv.URL+"/notfound")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 404")
}

// TestDiskCache_NoStoreSkipsDiskWrite verifies that a Cache-Control: no-store
// response is returned in-memory but never persisted, per RFC 9111 §5.2.2.5.
// The skills cache feeds fetched content to the LLM as instructions, so
// persisting an upstream-marked-private response under ~/.cagent would be
// both a privacy hazard and a spec violation.
func TestDiskCache_NoStoreSkipsDiskWrite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprint(w, "private content")
	}))
	defer srv.Close()

	cache := newDiskCache(t.TempDir())

	content, err := cache.FetchAndStore(t.Context(), "https://example.com", "skill", "SKILL.md", srv.URL+"/SKILL.md")
	require.NoError(t, err)
	assert.Equal(t, "private content", content)

	// Nothing must have been persisted under the cache directory.
	filePath := filepath.Join(cache.cacheDir("https://example.com", "skill"), "SKILL.md")
	_, err = os.Stat(filePath)
	require.ErrorIs(t, err, os.ErrNotExist, "no-store response must not be written to disk")

	_, err = os.Stat(filePath + ".meta")
	require.ErrorIs(t, err, os.ErrNotExist, "no-store response must not have metadata persisted")

	// And subsequent Get() must report a miss.
	_, ok := cache.Get("https://example.com", "skill", "SKILL.md")
	assert.False(t, ok)
}

// TestDiskCache_NoCacheStoresButExpiresImmediately verifies that no-cache
// allows storage but forces revalidation: the entry is written so it can be
// inspected, but Get() must report a miss so the next read refetches.
func TestDiskCache_NoCacheStoresButExpiresImmediately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		fmt.Fprint(w, "revalidate me")
	}))
	defer srv.Close()

	cache := newDiskCache(t.TempDir())

	_, err := cache.FetchAndStore(t.Context(), "https://example.com", "skill", "SKILL.md", srv.URL+"/SKILL.md")
	require.NoError(t, err)

	filePath := filepath.Join(cache.cacheDir("https://example.com", "skill"), "SKILL.md")
	_, err = os.Stat(filePath)
	require.NoError(t, err, "no-cache response should still be stored on disk")

	_, ok := cache.Get("https://example.com", "skill", "SKILL.md")
	assert.False(t, ok, "no-cache must force a refetch on the next read")
}
