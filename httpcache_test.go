package sqlitezstd_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	sqlitezstd "github.com/jtarchie/sqlitezstd"
	_ "github.com/mattn/go-sqlite3"
)

// countingFileServer serves dir over HTTP (with Range support) and counts the
// number of Range GET requests it receives.
func countingFileServer(dir string) (*httptest.Server, *int64) {
	var rangeGETs int64

	fileServer := http.FileServer(http.Dir(dir))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			atomic.AddInt64(&rangeGETs, 1)
		}

		fileServer.ServeHTTP(w, r)
	}))

	return server, &rangeGETs
}

// registerCacheVFS registers (once) a cache-enabled VFS under name.
func registerCacheVFS(t *testing.T, name string, opts ...sqlitezstd.Option) {
	t.Helper()

	if err := sqlitezstd.Register(name, opts...); err != nil && !strings.Contains(err.Error(), "already") {
		t.Fatalf("register %q: %v", name, err)
	}
}

func TestHTTPCacheCoalescesGETs(t *testing.T) {
	t.Parallel()

	zstPath := buildCompressedDB(t, 100_000)
	dir := filepath.Dir(zstPath)
	base := filepath.Base(zstPath)

	const cacheVFS = "zstdcache-coalesce"
	registerCacheVFS(t, cacheVFS, sqlitezstd.WithHTTPCacheSize(16<<20))

	// A full table scan (value is not indexed) touches every data page, so it
	// spans many frames — the case coalescing helps most.
	const query = "SELECT COUNT(*) FROM entries WHERE value LIKE 'value-1%'"

	run := func(vfs string) (int64, int64) {
		server, gets := countingFileServer(dir)
		defer server.Close()

		db, err := sql.Open("sqlite3", fmt.Sprintf("%s/%s?vfs=%s", server.URL, base, vfs))
		if err != nil {
			t.Fatalf("open (%s): %v", vfs, err)
		}
		defer db.Close() //nolint: errcheck

		db.SetMaxOpenConns(1)

		var count int64
		if err := db.QueryRow(query).Scan(&count); err != nil {
			t.Fatalf("query (%s): %v", vfs, err)
		}

		return count, atomic.LoadInt64(gets)
	}

	wantCount, defaultGETs := run("zstd")
	gotCount, cacheGETs := run(cacheVFS)

	t.Logf("range GETs: default(no cache)=%d cache=%d (%.1fx fewer)",
		defaultGETs, cacheGETs, float64(defaultGETs)/float64(max64(cacheGETs, 1)))

	if gotCount != wantCount {
		t.Fatalf("cache changed query result: got %d, want %d", gotCount, wantCount)
	}
	if cacheGETs == 0 {
		t.Fatal("expected at least one range GET through the cache")
	}
	if cacheGETs*2 > defaultGETs {
		t.Fatalf("cache did not substantially reduce GETs: default=%d cache=%d", defaultGETs, cacheGETs)
	}
}

func TestHTTPCachePerURLCorrectness(t *testing.T) {
	t.Parallel()

	zstA := buildCompressedDB(t, 1_000)
	zstB := buildCompressedDB(t, 7_777)

	const cacheVFS = "zstdcache-perurl"
	registerCacheVFS(t, cacheVFS, sqlitezstd.WithHTTPCacheSize(8<<20))

	count := func(zstPath string) int64 {
		server, _ := countingFileServer(filepath.Dir(zstPath))
		defer server.Close()

		db, err := sql.Open("sqlite3", fmt.Sprintf("%s/%s?vfs=%s", server.URL, filepath.Base(zstPath), cacheVFS))
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer db.Close() //nolint: errcheck

		var n int64
		if err := db.QueryRow("SELECT COUNT(*) FROM entries").Scan(&n); err != nil {
			t.Fatalf("query: %v", err)
		}

		return n
	}

	if got := count(zstA); got != 1_000 {
		t.Fatalf("db A: got %d, want 1000", got)
	}
	if got := count(zstB); got != 7_777 {
		t.Fatalf("db B: got %d, want 7777", got)
	}
}

func TestHTTPCacheConcurrentSameURL(t *testing.T) {
	t.Parallel()

	zstPath := buildCompressedDB(t, 5_000)

	const cacheVFS = "zstdcache-concurrent"
	registerCacheVFS(t, cacheVFS, sqlitezstd.WithHTTPCacheSize(8<<20))

	server, _ := countingFileServer(filepath.Dir(zstPath))
	defer server.Close()

	db, err := sql.Open("sqlite3", fmt.Sprintf("%s/%s?vfs=%s", server.URL, filepath.Base(zstPath), cacheVFS))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint: errcheck

	db.SetMaxOpenConns(8)

	const (
		goroutines = 8
		iterations = 50
	)

	var wg sync.WaitGroup

	errs := make(chan error, goroutines)

	for range goroutines {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for i := range iterations {
				var count int64
				if err := db.QueryRow("SELECT COUNT(*) FROM entries WHERE id > ?", i%100).Scan(&count); err != nil {
					errs <- err

					return
				}
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent cached read failed: %v", err)
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}

	return b
}
