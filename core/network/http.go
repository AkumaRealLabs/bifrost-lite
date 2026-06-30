// Package network provides shared HTTP retry behavior for upstream requests.
package network

import (
	"errors"
	"io"
	"strings"

	"github.com/valyala/fasthttp"
)

// StaleConnectionRetryIfErr is a RetryIfErr callback that retries requests when the failure
// is due to a stale/dead connection being reused from the pool. This addresses intermittent
// "cannot find whitespace in the first line of response" errors caused by connection reuse
// with leftover chunked transfer encoding data (see: https://github.com/valyala/fasthttp/issues/1743).
//
// By default fasthttp only retries idempotent requests (GET/HEAD/PUT). LLM inference requests
// use POST, so without this they fail immediately on stale connections. Retrying is safe here
// because the error occurs during response header parsing — before the server processes the
// new request, or on a connection the server has already closed.
// maxStaleConnRetries bounds how many times a single request will redial on a stale
// pooled connection. With FIFO pooling, the oldest connection is tried first, and when
// the upstream has a short keep-alive (e.g. vLLM's default 5s) several pooled connections
// can be dead at once - so a single retry can hit a second stale connection and still fail
// (see https://github.com/maximhq/bifrost/issues/4496). A small bound lets the request walk
// past a few dead connections to a live one while staying well under fasthttp's internal
// attempt cap. Retrying is safe here because the failure occurs before the server processes
// the request (during dial / response-header parsing).
const maxStaleConnRetries = 3

func StaleConnectionRetryIfErr(_ *fasthttp.Request, attempts int, err error) (resetTimeout bool, retry bool) {
	if attempts > maxStaleConnRetries {
		return false, false
	}
	if err == nil {
		return false, false
	}
	errStr := strings.ToLower(err.Error())
	// ErrConnectionClosed — server closed the connection before returning the first
	//   response byte. fasthttp converts raw io.EOF to this AFTER the retry loop, so
	//   RetryIfErr normally sees raw io.EOF; we match both to stay robust across versions.
	// io.EOF / io.ErrUnexpectedEOF — server closed the connection.
	// "cannot find whitespace in the first line of response" — stale chunked data in buffer.
	// reset / broken pipe / closed connection variants — server or intermediary closed idle conn.
	if errors.Is(err, fasthttp.ErrConnectionClosed) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		strings.Contains(errStr, "cannot find whitespace") ||
		strings.Contains(errStr, "connection reset by peer") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "use of closed network connection") ||
		strings.Contains(errStr, "server closed connection") {
		return true, true
	}
	return false, false
}
