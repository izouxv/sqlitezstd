package sqlitezstd

import (
	"sync"

	"github.com/SaveTheRbtz/zstd-seekable-format-go/pkg/framecache"
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

func newDecodedFrameCache(size int) framecache.Cache {
	return framecache.NewSieve(framecache.Limits{MaxFrames: size})
}
