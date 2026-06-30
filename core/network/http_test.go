package network

import (
	"fmt"
	"io"
	"testing"

	"github.com/valyala/fasthttp"
)

func TestStaleConnectionRetryIfErr(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		attempts  int
		wantReset bool
		wantRetry bool
	}{
		{name: "whitespace", err: fmt.Errorf(`cannot find whitespace in the first line of response`), attempts: 1, wantReset: true, wantRetry: true},
		{name: "eof", err: io.EOF, attempts: 1, wantReset: true, wantRetry: true},
		{name: "unexpected eof", err: io.ErrUnexpectedEOF, attempts: 1, wantReset: true, wantRetry: true},
		{name: "closed sentinel", err: fasthttp.ErrConnectionClosed, attempts: 1, wantReset: true, wantRetry: true},
		{name: "max attempts", err: io.EOF, attempts: 4, wantReset: false, wantRetry: false},
		{name: "unrelated", err: fmt.Errorf("dial tcp: no such host"), attempts: 1, wantReset: false, wantRetry: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset, retry := StaleConnectionRetryIfErr(nil, tt.attempts, tt.err)
			if reset != tt.wantReset || retry != tt.wantRetry {
				t.Fatalf("got reset=%v retry=%v want reset=%v retry=%v", reset, retry, tt.wantReset, tt.wantRetry)
			}
		})
	}
}
