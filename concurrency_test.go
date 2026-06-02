package sqlitezstd_test

import (
	"database/sql"
	"fmt"
	"sync"
	"testing"

	_ "github.com/jtarchie/sqlitezstd"
	_ "github.com/mattn/go-sqlite3"
)

// TestConcurrentReadersSharedDB exercises many goroutines reading concurrently
// through a single *sql.DB (so multiple connections share the process-wide zstd
// decoder). Run with -race to catch data races on the shared decoder and the
// per-file caches.
func TestConcurrentReadersSharedDB(t *testing.T) {
	t.Parallel()

	zstPath := buildCompressedDB(t, 5000)

	db, err := sql.Open("sqlite3", fmt.Sprintf("%s?vfs=zstd", zstPath))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint: errcheck

	db.SetMaxOpenConns(8)

	const (
		goroutines = 8
		iterations = 200
	)

	var wg sync.WaitGroup

	errs := make(chan error, goroutines)

	for g := range goroutines {
		wg.Add(1)

		go func(seed int) {
			defer wg.Done()

			for i := range iterations {
				var count int64
				if err := db.QueryRow(
					"SELECT COUNT(*) FROM entries WHERE id > ?", (seed*iterations+i)%5000,
				).Scan(&count); err != nil {
					errs <- err

					return
				}
			}
		}(g)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent read failed: %v", err)
	}
}
