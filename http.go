package sse

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
)

// This file is the HTTP adapter: it bridges net/http to the transport-agnostic
// Reader and Writer, which themselves depend only on io. Keeping the HTTP glue
// here lets the core codec stay free of net/http concerns.

// NewHTTPReader returns a Reader that parses resp.Body after verifying a 200 OK
// response with the text/event-stream media type (§9.2.3 and §9.2.5).
func NewHTTPReader(resp *http.Response) (*Reader, error) {
	if err := checkSSEResponse(resp); err != nil {
		return nil, err
	}
	return NewReader(resp.Body), nil
}

// checkSSEResponse validates that resp has a successful EventSource status,
// advertises the SSE media type, and has a non-nil body. The Content-Type is
// parsed with mime.ParseMediaType so that parameters (e.g. charset) and case are
// handled per RFC 2045 rather than by a raw prefix match.
func checkSSEResponse(resp *http.Response) error {
	if resp == nil {
		return errors.New("sse: http.Response cannot be nil")
	}
	if resp.Body == nil {
		return errors.New("sse: http.Response.Body cannot be nil")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sse: HTTP status must be 200 OK, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		return errors.New("sse: missing Content-Type header")
	}
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return fmt.Errorf("sse: invalid Content-Type %q: %w", ct, err)
	}
	if mediaType != "text/event-stream" {
		return fmt.Errorf("sse: Content-Type must be text/event-stream, got %q", mediaType)
	}
	return nil
}

// NewHTTPWriter returns a Writer for an HTTP response. It sets the SSE response
// headers and flushes after every write so each event reaches the client
// immediately. It panics if rw is nil.
//
// Flushing uses http.ResponseController, which unwraps middleware that wraps the
// ResponseWriter. If the underlying writer does not support flushing, the error
// surfaces from the first Write or Comment call rather than here.
func NewHTTPWriter(rw http.ResponseWriter) *Writer {
	if rw == nil {
		panic("sse: http.ResponseWriter cannot be nil")
	}
	setSSEHeaders(rw.Header())
	return NewWriter(&flushWriter{
		rw: rw,
		rc: http.NewResponseController(rw),
	})
}

// flushWriter flushes the response after every write. SSE connections are
// long-lived, so per-event flushing is essential.
type flushWriter struct {
	rw http.ResponseWriter
	rc *http.ResponseController
}

var _ io.Writer = (*flushWriter)(nil)

func (f *flushWriter) Write(p []byte) (int, error) {
	n, err := f.rw.Write(p)
	if err != nil {
		return n, err
	}
	if err := f.rc.Flush(); err != nil {
		return n, fmt.Errorf("sse: flush: %w", err)
	}
	return n, nil
}

// setSSEHeaders writes the response headers for an SSE endpoint.
//
//   - Content-Type: text/event-stream — the SSE media type (§9.2.5).
//   - Cache-Control: no-cache — set only when the caller has not already
//     supplied a value, to keep intermediaries from buffering the live stream.
//
// No Connection header is set: it is a hop-by-hop header that HTTP/2 forbids and
// that SSE does not require.
func setSSEHeaders(h http.Header) {
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	if h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "no-cache")
	}
}
