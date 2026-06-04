package sqlitezstd

import (
	"log/slog"
	"net/http"
	"time"
)

// Default option values. These are applied by [Register] and by the default
// "zstd" VFS registered in init().
const (
	// DefaultFrameCacheSize is the number of decoded zstd frames cached per
	// opened file.
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
	httpCacheBytes int64
	httpPageSize   int
	roundTripper   http.RoundTripper
	logger         *slog.Logger
}

// Option mutates an [Options]. See [WithFrameCacheSize], [WithHTTPTimeout],
// [WithHTTPRetries], and [WithLogger].
type Option func(*Options)

// WithFrameCacheSize sets the number of decoded zstd frames cached per opened file.
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

// WithHTTPCacheSize enables an in-memory, page-aligned read cache for the
// HTTP(S) path, bounded to roughly maxBytes. It coalesces the contiguous run of
// missing pages covering a read into a single Range GET and serves adjacent
// frames from cache, drastically cutting request count for remote scans. A
// value <= 0 (the default) disables the cache, preserving the original
// one-GET-per-frame behavior. Has no effect on the local-file path.
func WithHTTPCacheSize(maxBytes int64) Option {
	return func(o *Options) {
		o.httpCacheBytes = maxBytes
	}
}

// WithHTTPPageSize sets the coalescing/read-ahead page size for the HTTP cache
// (see [WithHTTPCacheSize]). Values <= 0 keep the default of
// [DefaultHTTPPageSize].
func WithHTTPPageSize(bytes int) Option {
	return func(o *Options) {
		if bytes > 0 {
			o.httpPageSize = bytes
		}
	}
}

// WithRoundTripper sets the base http.RoundTripper used for the HTTP(S) path.
// The library still wraps it with retry and Range-response validation, so a
// caller can supply, for example, a request-signing transport for authenticated
// buckets without losing those protections. A nil transport is ignored.
func WithRoundTripper(rt http.RoundTripper) Option {
	return func(o *Options) {
		if rt != nil {
			o.roundTripper = rt
		}
	}
}

// WithHTTPClient is a convenience that uses the client's Transport as the base
// round-tripper (see [WithRoundTripper]). A nil client is ignored.
func WithHTTPClient(c *http.Client) Option {
	return func(o *Options) {
		if c != nil && c.Transport != nil {
			o.roundTripper = c.Transport
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
		httpPageSize:   DefaultHTTPPageSize,
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
