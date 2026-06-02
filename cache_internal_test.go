package sqlitezstd

import (
	"bytes"
	"io"
	"sync/atomic"
	"testing"

	"github.com/klauspost/compress/zstd"
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

// countingDecoder records how many times the underlying decoder is invoked.
type countingDecoder struct {
	dec     zstdDecoder
	decodes atomic.Int64
}

func (c *countingDecoder) DecodeAll(input, dst []byte) ([]byte, error) {
	c.decodes.Add(1)

	return c.dec.DecodeAll(input, dst)
}

func TestCachingDecoderCachesDecompression(t *testing.T) {
	t.Parallel()

	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("new encoder: %v", err)
	}

	raw := bytes.Repeat([]byte("hello world "), 1000)
	compressed := enc.EncodeAll(raw, nil)
	_ = enc.Close()

	real, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatalf("new decoder: %v", err)
	}
	defer real.Close()

	counter := &countingDecoder{dec: real}

	decoder, err := newCachingDecoder(counter, 8)
	if err != nil {
		t.Fatalf("newCachingDecoder: %v", err)
	}

	out1, err := decoder.DecodeAll(compressed, nil)
	if err != nil {
		t.Fatalf("first DecodeAll: %v", err)
	}
	if !bytes.Equal(out1, raw) {
		t.Fatal("first DecodeAll produced wrong output")
	}
	if got := counter.decodes.Load(); got != 1 {
		t.Fatalf("want 1 underlying decode, got %d", got)
	}

	// An identical input must be served from the cache.
	out2, err := decoder.DecodeAll(compressed, nil)
	if err != nil {
		t.Fatalf("second DecodeAll: %v", err)
	}
	if !bytes.Equal(out2, raw) {
		t.Fatal("second DecodeAll produced wrong output")
	}
	if got := counter.decodes.Load(); got != 1 {
		t.Fatalf("want still 1 underlying decode after cache hit, got %d", got)
	}
}
