# SQLiteZSTD: Read-Only Access to Compressed SQLite Files

> [!IMPORTANT]
> A new version of this extension written in C is now available.
> This C version offers the advantage of being usable across different
> platforms, languages, and runtimes. It is not publicly available and is
> provided under a one-time fee in perpetuity license with support. The original
> Go version will remain freely available. For more information about the C
> extension, please email jtarchie@gmail.com.

## Description

SQLiteZSTD provides a tool for accessing SQLite databases compressed with
[Zstandard seekable (zstd)](https://github.com/facebook/zstd/blob/216099a73f6ec19c246019df12a2877dada45cca/contrib/seekable_format/zstd_seekable_compression_format.md)
in a read-only manner. Its functionality is based on the
[SQLite3 Virtual File System (VFS) in Go](https://github.com/psanford/sqlite3vfs).

Please note, SQLiteZSTD is specifically designed for reading data and **does not
support write operations**.

## Features

1. Read-only access to Zstd-compressed SQLite databases.
2. Interface through SQLite3 VFS.
3. The compressed database is seekable, facilitating ease of access.

## Usage

Your database needs to be compressed in the seekable Zstd format. I recommend
using this [CLI tool](github.com/SaveTheRbtz/zstd-seekable-format-go):

```bash
go get -a github.com/SaveTheRbtz/zstd-seekable-format-go/...
go run github.com/SaveTheRbtz/zstd-seekable-format-go/cmd/zstdseek \
    -f <dbPath> \
    -o <dbPath>.zst
```

### Choosing a frame size

The `-c min:avg:max` option controls the size of each zstd *frame* (in KiB). The
frame is the unit of random access: to read any byte, the whole frame containing
it must be fetched and decompressed. SQLiteZSTD caches recently used frames (see
[Configuration](#configuration)), so frame size is the main tuning knob:

- **Too large** — more bytes fetched/decompressed per read, and frames whose
  *compressed* size exceeds 128 MiB are rejected outright by the reader.
- **Too small** — worse compression ratio and more per-frame overhead.

A frame size on the order of tens to a few hundred KiB (for example
`-c 16:32:64`) is a reasonable starting point; align it to your read locality and
measure.

Below is an example of how to use SQLiteZSTD in a Go program:

```go
import (
    _ "github.com/jtarchie/sqlitezstd"
)

db, err := sql.Open("sqlite3", "<path-to-your-file>?vfs=zstd")
if err != nil {
    panic(fmt.Sprintf("Failed to open database: %s", err))
}

conn, err := db.Conn(context.Background())
if err != nil {
    panic(fmt.Sprintf("Failed to get connection: %s", err))
}
defer conn.Close()

// PRAGMAs are not persisted across `database/sql` pooled connections;
// this ensures the setting applies to the connection you query on.
_, err = conn.ExecContext(context.Background(), `PRAGMA temp_store = memory;`)
if err != nil {
    panic(fmt.Sprintf("Failed to set PRAGMA: %s", err))
}

// Use conn for subsequent operations to ensure PRAGMA is applied
```

In this Go code example:

- The `sql.Open()` function takes as a parameter the path to the compressed
  SQLite database, appended with a query string with `vfs=zstd` to use the VFS.
- `PRAGMA temp_store = memory` ensures the read-only VFS is not asked to create
  temporary files on disk (which it cannot do).

### Connections and concurrency

The VFS is safe to use from multiple connections concurrently — the tests and
benchmarks open many connections against one database. Each connection allocates
its own decompression reader and frame cache, so the trade-off of a larger
connection pool is **memory and duplicated decompression**, not correctness.
Tune `db.SetMaxOpenConns(...)` to balance parallelism against memory; there is no
requirement to limit it to a single connection.

### Reading over HTTP(S)

Pass an `http://` or `https://` URL as the filename to read a remote database
without downloading it in full — only the bytes needed for each query are
fetched using HTTP range requests:

```go
db, err := sql.Open("sqlite3", "https://example.com/data.sqlite.zst?vfs=zstd")
```

The server **must** support HTTP range requests (responding `206 Partial
Content` with a `Content-Range` header); a server that ignores `Range` is
rejected rather than silently returning wrong data. Each opened connection makes
one small request to determine the file size, then fetches frames on demand.
Frames are cached per connection, so repeated reads do not re-hit the network.

#### Coalescing reads for remote (S3/CDN) databases

By default each frame is fetched in its own Range GET. For high-latency stores
like S3 a query can fire many small GETs. Enable an in-memory, page-aligned read
cache to coalesce the contiguous run of missing pages behind a read into a
single GET (default page size 64 KiB) and to serve adjacent frames from cache:

```go
import sqlitezstd "github.com/jtarchie/sqlitezstd"

// Register a cache-enabled VFS once (e.g. at startup). DSN query params are
// stripped before the VFS sees the path, so configuration lives on the named
// VFS, not the URL.
err := sqlitezstd.Register("zstdcache",
    sqlitezstd.WithHTTPCacheSize(64<<20), // ~64 MiB of coalesced pages per open
)

db, _ := sql.Open("sqlite3", "https://bucket.example.com/segment.sqlite.zst?vfs=zstdcache")
```

In practice this collapses a remote query's request count by an order of
magnitude — a full-table-scan test issues **125 Range GETs without the cache vs
9 with it (~14× fewer)**. The cache is per opened file and bounded by the
configured byte cap (LRU eviction), so memory stays bounded. Tune the page size
with `WithHTTPPageSize`.

For authenticated buckets, supply a signing transport with
`WithRoundTripper`/`WithHTTPClient`; the library still wraps it with timeout,
retry, and range-validation.

### Build tags

Databases that use SQLite extensions such as FTS5 or R*Tree require building your
binary with the matching `mattn/go-sqlite3` build tag, e.g.:

```bash
go build -tags fts5 ./...
```

### Configuration

Importing the package registers a `zstd` VFS with sensible defaults. To tune the
frame-cache size, HTTP timeout, retry count, HTTP read cache
(`WithHTTPCacheSize`/`WithHTTPPageSize`), transport
(`WithRoundTripper`/`WithHTTPClient`), or logger, register your own named VFS and
reference it via `?vfs=<name>`:

```go
import sqlitezstd "github.com/jtarchie/sqlitezstd"

err := sqlitezstd.Register("zstd-tuned",
    sqlitezstd.WithFrameCacheSize(128),
    sqlitezstd.WithHTTPTimeout(10*time.Second),
    sqlitezstd.WithHTTPRetries(5),
)
// ...
db, _ := sql.Open("sqlite3", "https://example.com/data.sqlite.zst?vfs=zstd-tuned")
```

## Performance

Here's a simple benchmark comparing performance between reading from an
uncompressed vs. a compressed SQLite database, involving the insertion of 10k
records and retrieval of the `MAX` value (without an index) and FTS5.

```
BenchmarkReadUncompressedSQLite-4              	  159717	      7459 ns/op	     473 B/op	      15 allocs/op
BenchmarkReadUncompressedSQLiteFTS5Porter-4    	    2478	    471685 ns/op	     450 B/op	      15 allocs/op
BenchmarkReadUncompressedSQLiteFTS5Trigram-4   	     100	  10449792 ns/op	     542 B/op	      16 allocs/op
BenchmarkReadCompressedSQLite-4                	  266703	      3877 ns/op	    2635 B/op	      15 allocs/op
BenchmarkReadCompressedSQLiteFTS5Porter-4      	    2335	    487430 ns/op	   33992 B/op	      16 allocs/op
BenchmarkReadCompressedSQLiteFTS5Trigram-4     	      48	  21235303 ns/op	45970431 B/op	     148 allocs/op
BenchmarkReadCompressedHTTPSQLite-4            	  284820	      4341 ns/op	    3312 B/op	      15 allocs/op
```
