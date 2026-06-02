package sqlitezstd

import (
	"bytes"
	"io"
	"sync/atomic"
	"testing"
)

// countingFetcher is an io.ReaderAt that records how many times it is called and
// how many bytes it serves.
type countingFetcher struct {
	data      []byte
	calls     atomic.Int64
	bytesRead atomic.Int64
}

func (f *countingFetcher) ReadAt(p []byte, off int64) (int, error) {
	f.calls.Add(1)

	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}

	n := copy(p, f.data[off:])
	f.bytesRead.Add(int64(n))

	if n < len(p) {
		return n, io.EOF
	}

	return n, nil
}

func makeData(size int) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i)
	}

	return data
}

// nolint: cyclop
func TestHTTPReadCacheCoalescesAndCaches(t *testing.T) {
	t.Parallel()

	data := makeData(300 * 1024)
	fetcher := &countingFetcher{data: data}

	cache, err := newHTTPReadCache(int64(len(data)), 64*1024, 8*1024*1024)
	if err != nil {
		t.Fatalf("newHTTPReadCache: %v", err)
	}

	// A small 4 KiB read should pull the whole 64 KiB page in one fetch.
	p := make([]byte, 4096)

	n, err := cache.Get(p, 1000, fetcher)
	if err != nil || n != len(p) {
		t.Fatalf("Get: n=%d err=%v", n, err)
	}
	if !bytes.Equal(p, data[1000:1000+len(p)]) {
		t.Fatal("first Get returned wrong bytes")
	}
	if got := fetcher.calls.Load(); got != 1 {
		t.Fatalf("want 1 fetch, got %d", got)
	}
	if got := fetcher.bytesRead.Load(); got != 64*1024 {
		t.Fatalf("want a 64 KiB read-ahead fetch, got %d bytes", got)
	}

	// Another read within the same page is served from cache: no new fetch.
	n, err = cache.Get(p, 5000, fetcher)
	if err != nil || n != len(p) {
		t.Fatalf("second Get: n=%d err=%v", n, err)
	}
	if !bytes.Equal(p, data[5000:5000+len(p)]) {
		t.Fatal("second Get returned wrong bytes")
	}
	if got := fetcher.calls.Load(); got != 1 {
		t.Fatalf("want still 1 fetch after cache hit, got %d", got)
	}

	hits, misses, _ := cache.stats()
	if hits != 1 || misses != 1 {
		t.Fatalf("stats: hits=%d misses=%d (want 1/1)", hits, misses)
	}
}

func TestHTTPReadCacheCrossPage(t *testing.T) {
	t.Parallel()

	data := makeData(300 * 1024)
	fetcher := &countingFetcher{data: data}

	cache, err := newHTTPReadCache(int64(len(data)), 64*1024, 8*1024*1024)
	if err != nil {
		t.Fatalf("newHTTPReadCache: %v", err)
	}

	// A read spanning three pages must be assembled correctly.
	p := make([]byte, 100*1024)

	n, err := cache.Get(p, 30*1024, fetcher)
	if err != nil || n != len(p) {
		t.Fatalf("Get: n=%d err=%v", n, err)
	}
	if !bytes.Equal(p, data[30*1024:30*1024+len(p)]) {
		t.Fatal("cross-page Get returned wrong bytes")
	}
}

func TestHTTPReadCacheBounded(t *testing.T) {
	t.Parallel()

	data := makeData(1024 * 1024)
	fetcher := &countingFetcher{data: data}

	// Cap at 2 pages (128 KiB) with a 64 KiB page size.
	cache, err := newHTTPReadCache(int64(len(data)), 64*1024, 128*1024)
	if err != nil {
		t.Fatalf("newHTTPReadCache: %v", err)
	}

	p := make([]byte, 4096)

	for off := int64(0); off+int64(len(p)) <= int64(len(data)); off += 64 * 1024 {
		n, err := cache.Get(p, off, fetcher)
		if err != nil || n != len(p) {
			t.Fatalf("Get @%d: n=%d err=%v", off, n, err)
		}
		if !bytes.Equal(p, data[off:off+int64(len(p))]) {
			t.Fatalf("wrong bytes @%d", off)
		}
	}

	if _, _, pages := cache.stats(); pages > 2 {
		t.Fatalf("cache exceeded its 2-page cap: %d pages resident", pages)
	}
}

func TestHTTPReadCacheReadToEOF(t *testing.T) {
	t.Parallel()

	// File size not a multiple of the page size, to exercise the partial last page.
	data := makeData(70 * 1024)
	fetcher := &countingFetcher{data: data}

	cache, err := newHTTPReadCache(int64(len(data)), 64*1024, 8*1024*1024)
	if err != nil {
		t.Fatalf("newHTTPReadCache: %v", err)
	}

	p := make([]byte, 4096)

	n, err := cache.Get(p, int64(len(data))-2048, fetcher)
	if n != 2048 {
		t.Fatalf("want 2048 bytes at EOF, got %d (err=%v)", n, err)
	}
	if !bytes.Equal(p[:n], data[len(data)-2048:]) {
		t.Fatal("EOF read returned wrong bytes")
	}
}
