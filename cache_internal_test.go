package sqlitezstd

import (
	"bytes"
	"io"
	"sync/atomic"
	"testing"
)

// countingReaderAt records how many times the underlying source is read.
type countingReaderAt struct {
	data  []byte
	reads atomic.Int64
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	c.reads.Add(1)

	if off >= int64(len(c.data)) {
		return 0, io.EOF
	}

	n := copy(p, c.data[off:])
	if n < len(p) {
		return n, io.EOF
	}

	return n, nil
}

func TestFrameReaderCachesCompressedReads(t *testing.T) {
	t.Parallel()

	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i)
	}

	src := &countingReaderAt{data: data}

	reader, err := newFrameReader(src, int64(len(data)), 8)
	if err != nil {
		t.Fatalf("newFrameReader: %v", err)
	}

	p := make([]byte, 128)

	if _, err := reader.ReadAt(p, 256); err != nil {
		t.Fatalf("first ReadAt: %v", err)
	}
	if got := src.reads.Load(); got != 1 {
		t.Fatalf("want 1 source read after first ReadAt, got %d", got)
	}

	// An identical read must be served from the cache, not the source.
	if _, err := reader.ReadAt(p, 256); err != nil {
		t.Fatalf("second ReadAt: %v", err)
	}
	if got := src.reads.Load(); got != 1 {
		t.Fatalf("want still 1 source read after cached ReadAt, got %d", got)
	}

	if !bytes.Equal(p, data[256:256+len(p)]) {
		t.Fatal("cached ReadAt returned wrong bytes")
	}
}
