package sqlitezstd

import (
	"io"

	seekable "github.com/SaveTheRbtz/zstd-seekable-format-go/pkg"
	"github.com/psanford/sqlite3vfs"
)

// ZstdFile is a read-only sqlite3vfs.File backed by a zstd-seekable reader.
type ZstdFile struct {
	reader   io.ReadSeeker
	seekable *seekable.Reader
	// size is the decompressed database size, captured once at Open so FileSize
	// does not have to call the reader's (non-goroutine-safe) Seek.
	size int64
}

var _ sqlite3vfs.File = &ZstdFile{}

func (z *ZstdFile) CheckReservedLock() (bool, error) {
	return false, nil
}

func (z *ZstdFile) Close() error {
	err := z.seekable.Close()

	if closer, ok := z.reader.(io.Closer); ok {
		if cerr := closer.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}

	return err
}

func (z *ZstdFile) DeviceCharacteristics() sqlite3vfs.DeviceCharacteristic {
	return sqlite3vfs.IocapImmutable
}

func (z *ZstdFile) FileSize() (int64, error) {
	return z.size, nil
}

func (z *ZstdFile) Lock(elock sqlite3vfs.LockType) error {
	return nil
}

func (z *ZstdFile) ReadAt(p []byte, off int64) (int, error) {
	return z.seekable.ReadAt(p, off)
}

func (z *ZstdFile) SectorSize() int64 {
	// A whole zstd frame must be decompressed to serve any byte within it, but
	// SQLite reads a read-only immutable database page-by-page regardless of the
	// reported sector size — the intra-frame locality win is captured by the
	// frame caches, not by this value. Reporting 0 keeps SQLite on its default
	// behavior.
	return 0
}

func (z *ZstdFile) Sync(flag sqlite3vfs.SyncType) error {
	return nil
}

func (z *ZstdFile) Truncate(size int64) error {
	return sqlite3vfs.ReadOnlyError
}

func (z *ZstdFile) Unlock(elock sqlite3vfs.LockType) error {
	return nil
}

func (z *ZstdFile) WriteAt(p []byte, off int64) (int, error) {
	return 0, sqlite3vfs.ReadOnlyError
}
