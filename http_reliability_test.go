package sqlitezstd_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	sqlitezstd "github.com/jtarchie/sqlitezstd"
	_ "github.com/mattn/go-sqlite3"
)

// TestHTTPServerIgnoringRangeFailsCleanly verifies that a server which ignores
// the Range header (returning 200 with the full body) produces a clean error
// rather than silently feeding non-frame bytes into the decoder.
func TestHTTPServerIgnoringRangeFailsCleanly(t *testing.T) {
	t.Parallel()

	zstPath := buildCompressedDB(t, 1000)

	data, err := os.ReadFile(zstPath) //nolint: gosec
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Ignore the Range header entirely and return the whole file with 200.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
	defer server.Close()

	db, err := sql.Open("sqlite3", server.URL+"?vfs=zstd")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint: errcheck

	var count int64
	if err := db.QueryRow("SELECT COUNT(*) FROM entries").Scan(&count); err == nil {
		t.Fatal("expected an error when the server ignores Range requests, got success (silent corruption risk)")
	}
}

// TestHTTPTimeoutDoesNotHang verifies that a hung server is bounded by the
// configured timeout instead of blocking the query forever.
func TestHTTPTimeoutDoesNotHang(t *testing.T) {
	t.Parallel()

	const vfsName = "zstd-timeout-test"

	if err := sqlitezstd.Register(vfsName,
		sqlitezstd.WithHTTPTimeout(200*time.Millisecond),
		sqlitezstd.WithHTTPRetries(0),
	); err != nil && !strings.Contains(err.Error(), "already") {
		t.Fatalf("register: %v", err)
	}

	// quit lets the hung handler return at cleanup so server.Close() does not
	// block on the in-flight (deliberately stalled) request.
	quit := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-quit:
		case <-time.After(10 * time.Second):
		}
	}))
	defer server.Close()
	defer close(quit)

	db, err := sql.Open("sqlite3", server.URL+"?vfs="+vfsName)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint: errcheck

	done := make(chan error, 1)

	go func() {
		var count int64
		done <- db.QueryRow("SELECT COUNT(*) FROM entries").Scan(&count)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a timeout error, got success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("query hung well past the configured 200ms timeout — no HTTP timeout enforced")
	}
}
