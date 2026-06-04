// The Read/Seek bounds logic below is derived from the Wuffs project's
// readerat package.
//
// Copyright 2019 The Wuffs Authors.
//
// Licensed under the Apache License, Version 2.0 <LICENSE-APACHE or
// https://www.apache.org/licenses/LICENSE-2.0> or the MIT license
// <LICENSE-MIT or https://opensource.org/licenses/MIT>, at your
// option. This file may not be copied, modified, or distributed
// except according to those terms.
//
// SPDX-License-Identifier: Apache-2.0 OR MIT

package sqlitezstd

import (
	"errors"
	"io"
	"sync"
)

var (
	errInvalidSize            = errors.New("sqlitezstd: invalid size")
	errSeekToInvalidWhence    = errors.New("sqlitezstd: seek to invalid whence")
	errSeekToNegativePosition = errors.New("sqlitezstd: seek to negative position")
)

// frameReader adapts a fixed-size io.ReaderAt (a local *os.File or the HTTP
// range reader) into the io.ReadSeeker the seekable reader requires, while also
// exposing io.ReaderAt directly.
//
// Exposing ReadAt is important because the seekable reader only takes its
// concurrency-safe, positional fast-path when the underlying reader implements
// io.ReaderAt; otherwise it falls back to a mutex-guarded Seek+Read.
//
// ReadAt is safe for concurrent use. The sequential Read/Seek methods (used only
// for the seek-table footer at open time) are guarded by mu and must not be
// called concurrently with each other.
type frameReader struct {
	src  io.ReaderAt
	size int64

	mu     sync.Mutex
	offset int64
}

var (
	_ io.ReadSeeker = (*frameReader)(nil)
	_ io.ReaderAt   = (*frameReader)(nil)
	_ io.Closer     = (*frameReader)(nil)
)

func newFrameReader(src io.ReaderAt, size int64) *frameReader {
	return &frameReader{src: src, size: size}
}

// ReadAt implements io.ReaderAt and is safe for concurrent use.
func (r *frameReader) ReadAt(p []byte, off int64) (int, error) {
	if r.size < 0 {
		return 0, errInvalidSize
	}
	if len(p) == 0 {
		return 0, nil
	}

	return r.src.ReadAt(p, off)
}

// Read implements io.Reader. It is not safe for concurrent use.
func (r *frameReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.size < 0 {
		return 0, errInvalidSize
	}
	if r.size <= r.offset {
		return 0, io.EOF
	}

	length := r.size - r.offset
	if int64(len(p)) > length {
		p = p[:length]
	}
	if len(p) == 0 {
		return 0, nil
	}

	actual, err := r.src.ReadAt(p, r.offset)
	r.offset += int64(actual)
	if (err == nil) && (r.offset == r.size) {
		err = io.EOF
	}

	return actual, err
}

// Seek implements io.Seeker. It is not safe for concurrent use.
func (r *frameReader) Seek(offset int64, whence int) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.size < 0 {
		return 0, errInvalidSize
	}

	switch whence {
	case io.SeekStart:
		// No-op.
	case io.SeekCurrent:
		offset += r.offset
	case io.SeekEnd:
		offset += r.size
	default:
		return 0, errSeekToInvalidWhence
	}

	if offset < 0 {
		return 0, errSeekToNegativePosition
	}

	r.offset = offset

	return r.offset, nil
}

// Close closes the underlying source if it is an io.Closer (e.g. a local file).
func (r *frameReader) Close() error {
	if closer, ok := r.src.(io.Closer); ok {
		return closer.Close()
	}

	return nil
}
