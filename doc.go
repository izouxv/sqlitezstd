// Package sqlitezstd provides a read-only SQLite VFS for opening Zstandard
// "seekable" compressed SQLite database files, either from the local filesystem
// or over HTTP(S) using range requests.
//
// Importing the package for its side effects registers a VFS named "zstd":
//
//	import _ "github.com/jtarchie/sqlitezstd"
//
//	db, err := sql.Open("sqlite3", "path/to/db.sqlite.zst?vfs=zstd")
//
// The source database must first be compressed into the zstd seekable format
// (see the README). The VFS is strictly read-only: writes, journals, and WAL
// files are rejected.
//
// For HTTP(S) sources, pass an http:// or https:// URL as the filename. The
// server must support HTTP range requests (responding 206 with a Content-Range
// header).
//
// Use [Register] to register the VFS under a different name with tuned
// [Options] (frame-cache size, HTTP timeouts, retries, logger).
package sqlitezstd
