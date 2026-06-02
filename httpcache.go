package sqlitezstd

import (
	"errors"
	"io"
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
)

// DefaultHTTPPageSize is the coalescing/read-ahead page size used when caching
// is enabled without an explicit page size. A small frame read pulls the whole
// page, so adjacent frames become cache hits served without another GET.
const DefaultHTTPPageSize = 64 << 10 // 64 KiB

// httpReadCache implements httpreadat.CacheHandler with an in-memory,
// page-aligned, bounded cache. It coalesces the contiguous run of missing pages
// covering a read into a single underlying fetch (one HTTP Range GET), which is
// what collapses a remote query's many one-frame GETs into far fewer requests.
//
// It is created per opened file, so under SQLite's per-connection serialization
// there is no real contention; the mutex is correctness insurance (and makes it
// safe under go test -race).
type httpReadCache struct {
	pageSize int64
	fileSize int64

	mu    sync.Mutex
	pages *lru.Cache[int64, []byte]

	hits   int
	misses int
}

func newHTTPReadCache(fileSize int64, pageSize int, maxBytes int64) (*httpReadCache, error) {
	if pageSize <= 0 {
		pageSize = DefaultHTTPPageSize
	}

	if maxBytes <= 0 {
		return nil, errors.New("sqlitezstd: http cache size must be positive")
	}

	maxPages := int(maxBytes / int64(pageSize))
	if maxPages < 1 {
		maxPages = 1
	}

	pages, err := lru.New[int64, []byte](maxPages)
	if err != nil {
		return nil, err
	}

	return &httpReadCache{
		pageSize: int64(pageSize),
		fileSize: fileSize,
		pages:    pages,
	}, nil
}

// Get implements httpreadat.CacheHandler.
func (c *httpReadCache) Get(p []byte, off int64, fetcher io.ReaderAt) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	startPage := off / c.pageSize
	endPage := (off + int64(len(p)) - 1) / c.pageSize

	// Collect the page slices covering the request, recording the contiguous run
	// of missing pages. Hits are gathered as references so that adding the
	// fetched pages below cannot disturb the assembly of this read.
	pageData := make(map[int64][]byte, endPage-startPage+1)

	firstMissing, lastMissing := int64(-1), int64(-1)

	for i := startPage; i <= endPage; i++ {
		if b, ok := c.pages.Get(i); ok {
			pageData[i] = b

			continue
		}

		if firstMissing < 0 {
			firstMissing = i
		}

		lastMissing = i
	}

	if firstMissing >= 0 {
		c.misses++

		if err := c.fetchPages(firstMissing, lastMissing, fetcher, pageData); err != nil {
			return 0, err
		}
	} else {
		c.hits++
	}

	return c.assemble(p, off, pageData), nil
}

// fetchPages reads [firstMissing, lastMissing] in a single ReadAt, slicing the
// result into per-page entries that are both returned (via pageData) and stored
// in the LRU for future reads.
func (c *httpReadCache) fetchPages(firstMissing, lastMissing int64, fetcher io.ReaderAt, pageData map[int64][]byte) error {
	fetchStart := firstMissing * c.pageSize

	fetchEnd := (lastMissing + 1) * c.pageSize
	if fetchEnd > c.fileSize {
		fetchEnd = c.fileSize
	}

	buf := make([]byte, fetchEnd-fetchStart)

	n, err := fetcher.ReadAt(buf, fetchStart)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}

	buf = buf[:n]

	for i := firstMissing; i <= lastMissing; i++ {
		lo := (i - firstMissing) * c.pageSize
		if lo >= int64(len(buf)) {
			break
		}

		hi := lo + c.pageSize
		if hi > int64(len(buf)) {
			hi = int64(len(buf))
		}

		page := make([]byte, hi-lo)
		copy(page, buf[lo:hi])
		pageData[i] = page
		_ = c.pages.Add(i, page)
	}

	return nil
}

// assemble copies the requested range out of the gathered page slices, stopping
// at the end of available data (a short read at EOF).
func (c *httpReadCache) assemble(p []byte, off int64, pageData map[int64][]byte) int {
	copied := 0

	for copied < len(p) {
		cur := off + int64(copied)
		pageIndex := cur / c.pageSize

		page, ok := pageData[pageIndex]
		if !ok {
			break
		}

		within := int(cur - pageIndex*c.pageSize)
		if within >= len(page) {
			break
		}

		copied += copy(p[copied:], page[within:])
	}

	return copied
}

// stats reports cache hit/miss counts and the number of resident pages. Used by
// tests.
func (c *httpReadCache) stats() (hits, misses, pages int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.hits, c.misses, c.pages.Len()
}
