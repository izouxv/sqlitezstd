package sqlitezstd_test

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	seekable "github.com/SaveTheRbtz/zstd-seekable-format-go/pkg"
	"github.com/klauspost/compress/zstd"
)

// defaultFrameSize is the uncompressed size of each zstd frame written by
// compressSeekable. It is intentionally small relative to typical databases so
// fixtures contain many frames, exercising the frame cache and seek behavior.
const defaultFrameSize = 64 * 1024

// compressSeekable reads srcPath and writes a zstd-seekable archive to dstPath,
// using fixed-size frames. This replaces shelling out to `go run
// .../cmd/zstdseek`, making tests hermetic and fast: no toolchain compile, no
// subprocess, no network.
// nolint: cyclop
func compressSeekable(srcPath, dstPath string, frameSize int) error {
	if frameSize <= 0 {
		frameSize = defaultFrameSize
	}

	in, err := os.Open(srcPath) //nolint: gosec
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer in.Close() //nolint: errcheck

	out, err := os.Create(dstPath) //nolint: gosec
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer out.Close() //nolint: errcheck

	enc, err := zstd.NewWriter(nil)
	if err != nil {
		return fmt.Errorf("create encoder: %w", err)
	}
	defer enc.Close() //nolint: errcheck

	writer, err := seekable.NewWriter(out, enc)
	if err != nil {
		return fmt.Errorf("create seekable writer: %w", err)
	}

	buf := make([]byte, frameSize)
	for {
		n, rerr := io.ReadFull(in, buf)
		if n > 0 {
			if _, werr := writer.Write(buf[:n]); werr != nil {
				_ = writer.Close()

				return fmt.Errorf("write frame: %w", werr)
			}
		}

		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}

		if rerr != nil {
			_ = writer.Close()

			return fmt.Errorf("read source: %w", rerr)
		}
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("finalize seekable archive: %w", err)
	}

	if err := out.Close(); err != nil {
		return fmt.Errorf("close destination: %w", err)
	}

	return nil
}

// buildCompressedDB creates a small SQLite database with the given number of
// rows, compresses it into the zstd-seekable format, and returns the path to
// the .zst file. It uses testing.TB (not Gomega) so it can be used from plain
// Go tests such as the concurrency/race test. The temp dir is cleaned up
// automatically.
func buildCompressedDB(tb testing.TB, rows int) string {
	tb.Helper()

	dir := tb.TempDir()
	dbPath := filepath.Join(dir, "test.sqlite")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		tb.Fatalf("open db: %v", err)
	}

	if _, err := db.Exec(`CREATE TABLE entries (id INTEGER PRIMARY KEY, value TEXT);`); err != nil {
		tb.Fatalf("create table: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		tb.Fatalf("begin: %v", err)
	}

	stmt, err := tx.Prepare("INSERT INTO entries (id, value) VALUES (?, ?)")
	if err != nil {
		tb.Fatalf("prepare: %v", err)
	}

	for i := 1; i <= rows; i++ {
		if _, err := stmt.Exec(i, fmt.Sprintf("value-%d", i)); err != nil {
			tb.Fatalf("insert: %v", err)
		}
	}

	_ = stmt.Close()

	if err := tx.Commit(); err != nil {
		tb.Fatalf("commit: %v", err)
	}

	if err := db.Close(); err != nil {
		tb.Fatalf("close db: %v", err)
	}

	zstPath := dbPath + ".zst"
	if err := compressSeekable(dbPath, zstPath, 16*1024); err != nil {
		tb.Fatalf("compress: %v", err)
	}

	return zstPath
}
