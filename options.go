package sqlitezstd

import (
	"log/slog"
	"time"
)

// Default option values. These are applied by [Register] and by the default
// "zstd" VFS registered in init().
const (
	// DefaultFrameCacheSize is the number of zstd frames cached per opened file
	// (both the compressed bytes and the decompressed output). The upstream
	// seekable reader only caches a single frame, so this cache is what keeps
	// SQLite's scattered page reads from repeatedly re-fetching and
	// re-decompressing the same frames.
	DefaultFrameCacheSize = 64
	// DefaultHTTPTimeout bounds dialing and waiting for response headers on the
	// HTTP(S) path so a hung server cannot block a query indefinitely.
	DefaultHTTPTimeout = 30 * time.Second
	// DefaultHTTPMaxRetries is how many times a transient HTTP failure (network
	// error, 5xx, 429) is retried before the read fails. Reads are idempotent
	// against an immutable source, so retrying is safe.
	DefaultHTTPMaxRetries = 3
)

// Options configures a registered VFS. Construct it with [Option] values passed
// to [Register]; the zero value is not valid (use [Register], which fills in
// defaults).
type Options struct {
	frameCacheSize int
	httpTimeout    time.Duration
	httpMaxRetries int
	logger         *slog.Logger
}

// Option mutates an [Options]. See [WithFrameCacheSize], [WithHTTPTimeout],
// [WithHTTPRetries], and [WithLogger].
type Option func(*Options)

// WithFrameCacheSize sets the number of zstd frames cached per opened file.
// Values <= 0 are ignored (the default is kept).
func WithFrameCacheSize(frames int) Option {
	return func(o *Options) {
		if frames > 0 {
			o.frameCacheSize = frames
		}
	}
}

// WithHTTPTimeout sets the dial and response-header timeout for the HTTP(S)
// path. Values <= 0 are ignored.
func WithHTTPTimeout(d time.Duration) Option {
	return func(o *Options) {
		if d > 0 {
			o.httpTimeout = d
		}
	}
}

// WithHTTPRetries sets the number of retries for transient HTTP failures.
// Negative values are ignored; 0 disables retries.
func WithHTTPRetries(n int) Option {
	return func(o *Options) {
		if n >= 0 {
			o.httpMaxRetries = n
		}
	}
}

// WithLogger sets the logger used to report otherwise-opaque open/read failures
// (the sqlite3vfs interface can only return fixed sentinel errors, so the real
// cause is logged). A nil logger is ignored.
func WithLogger(l *slog.Logger) Option {
	return func(o *Options) {
		if l != nil {
			o.logger = l
		}
	}
}

func defaultOptions() *Options {
	return &Options{
		frameCacheSize: DefaultFrameCacheSize,
		httpTimeout:    DefaultHTTPTimeout,
		httpMaxRetries: DefaultHTTPMaxRetries,
		logger:         slog.Default(),
	}
}

func resolveOptions(opts []Option) *Options {
	o := defaultOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}

	return o
}
