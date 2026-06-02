package sqlitezstd

import (
	"sync"

	"github.com/cespare/xxhash/v2"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/klauspost/compress/zstd"
)

// sharedDecoder is a single, process-wide zstd decoder shared by every opened
// file. The seekable reader only ever calls DecodeAll, which is safe for
// concurrent use, so one decoder replaces the per-Open decoder pool that was
// allocated (and never closed) for each connection. It is intentionally never
// closed because it lives for the lifetime of the process.
//
// nolint: gochecknoglobals
var sharedDecoder = sync.OnceValues(func() (*zstd.Decoder, error) {
	return zstd.NewReader(nil)
})

// zstdDecoder is the subset of *zstd.Decoder used here (matching the seekable
// ZSTDDecoder interface). It is an interface so the cache can be unit-tested.
type zstdDecoder interface {
	DecodeAll(input, dst []byte) ([]byte, error)
}

// cachingDecoder wraps a zstd decoder with an LRU of decompressed frames keyed
// by the hash of the compressed input. The upstream seekable reader keeps only
// a single decompressed frame, so SQLite's scattered page reads otherwise force
// the same frames to be decompressed (and freshly allocated) over and over —
// the dominant cost in the FTS5/trigram benchmarks.
type cachingDecoder struct {
	dec   zstdDecoder
	cache *lru.Cache[uint64, []byte]
}

func newCachingDecoder(dec zstdDecoder, size int) (*cachingDecoder, error) {
	cache, err := lru.New[uint64, []byte](size)
	if err != nil {
		return nil, err
	}

	return &cachingDecoder{dec: dec, cache: cache}, nil
}

// DecodeAll implements the seekable ZSTDDecoder interface. The seekable reader
// only ever reads from (never mutates) the returned slice and always passes a
// nil dst, so returning a shared cached slice is safe for concurrent readers.
func (c *cachingDecoder) DecodeAll(input, dst []byte) ([]byte, error) {
	key := xxhash.Sum64(input)
	if cached, ok := c.cache.Get(key); ok {
		return cached, nil
	}

	out, err := c.dec.DecodeAll(input, dst)
	if err != nil {
		return nil, err
	}

	_ = c.cache.Add(key, out)

	return out, nil
}
