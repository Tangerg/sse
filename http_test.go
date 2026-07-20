package sse

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newResp builds a minimal *http.Response with the given Content-Type and a
// non-nil empty body.
func newResp(contentType string) *http.Response {
	h := http.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(strings.NewReader(""))}
}

func TestNewHTTPReader(t *testing.T) {
	t.Run("nil response", func(t *testing.T) {
		if _, err := NewHTTPReader(nil); err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("nil body", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}}
		if _, err := NewHTTPReader(resp); err == nil {
			t.Error("expected error for nil Body, got nil")
		}
	})

	t.Run("non-200 status", func(t *testing.T) {
		resp := newResp("text/event-stream")
		resp.StatusCode = http.StatusNoContent
		if _, err := NewHTTPReader(resp); err == nil {
			t.Error("expected error for status 204, got nil")
		}
	})

	t.Run("missing Content-Type", func(t *testing.T) {
		if _, err := NewHTTPReader(newResp("")); err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("wrong Content-Type", func(t *testing.T) {
		if _, err := NewHTTPReader(newResp("application/json")); err == nil {
			t.Error("expected error, got nil")
		}
	})

	// A raw prefix match would wrongly accept this; mime.ParseMediaType does not.
	t.Run("similar-prefix Content-Type rejected", func(t *testing.T) {
		if _, err := NewHTTPReader(newResp("text/event-streaming")); err == nil {
			t.Error("expected error for text/event-streaming, got nil")
		}
	})

	t.Run("malformed Content-Type rejected", func(t *testing.T) {
		if _, err := NewHTTPReader(newResp("text/event-stream; charset")); err == nil {
			t.Error("expected error for malformed media parameter, got nil")
		}
	})

	t.Run("valid text/event-stream", func(t *testing.T) {
		if _, err := NewHTTPReader(newResp("text/event-stream")); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("charset parameter accepted", func(t *testing.T) {
		if _, err := NewHTTPReader(newResp("text/event-stream; charset=utf-8")); err != nil {
			t.Fatal(err)
		}
	})

	// EventSource always decodes the body as UTF-8; a charset label does not
	// select another decoder (mirrors WPT format-utf-8.any.js).
	t.Run("non-UTF-8 charset label is ignored", func(t *testing.T) {
		if _, err := NewHTTPReader(newResp("text/event-stream; charset=windows-1252")); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("trailing semicolon accepted", func(t *testing.T) {
		if _, err := NewHTTPReader(newResp("text/event-stream;")); err != nil {
			t.Fatal(err)
		}
	})

	// Media types are case-insensitive per RFC 2045.
	t.Run("mixed case accepted", func(t *testing.T) {
		if _, err := NewHTTPReader(newResp("Text/Event-Stream")); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSetSSEHeaders(t *testing.T) {
	t.Run("sets Content-Type and Cache-Control", func(t *testing.T) {
		h := http.Header{}
		setSSEHeaders(h)
		if got := h.Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
			t.Errorf("Content-Type = %q", got)
		}
		if got := h.Get("Cache-Control"); got != "no-cache" {
			t.Errorf("Cache-Control = %q", got)
		}
	})

	t.Run("does not set a Connection header", func(t *testing.T) {
		h := http.Header{}
		setSSEHeaders(h)
		if got := h.Get("Connection"); got != "" {
			t.Errorf("Connection = %q, want empty (hop-by-hop, forbidden under HTTP/2)", got)
		}
	})

	t.Run("does not override existing Cache-Control", func(t *testing.T) {
		h := http.Header{}
		h.Set("Cache-Control", "no-store")
		setSSEHeaders(h)
		if got := h.Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want %q", got, "no-store")
		}
	})
}

// nonFlusher hides the underlying ResponseWriter's Flusher (it exposes only the
// ResponseWriter interface and no Unwrap), so flushing through
// http.ResponseController is unsupported.
type nonFlusher struct {
	http.ResponseWriter
}

type errorResponseWriter struct {
	header http.Header
	err    error
}

func (w *errorResponseWriter) Header() http.Header       { return w.header }
func (*errorResponseWriter) WriteHeader(int)             {}
func (w *errorResponseWriter) Write([]byte) (int, error) { return 0, w.err }

func TestNewHTTPWriter(t *testing.T) {
	t.Run("nil ResponseWriter panics", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Error("expected panic, got none")
			}
		}()
		NewHTTPWriter(nil)
	})

	t.Run("valid ResponseWriter sets SSE headers", func(t *testing.T) {
		rr := httptest.NewRecorder()
		w := NewHTTPWriter(rr)
		if w == nil {
			t.Error("expected non-nil Writer")
		}
		if got := rr.Header().Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
			t.Errorf("Content-Type = %q", got)
		}
	})

	// Flushability is not probed at construction; a non-flushable writer surfaces
	// the error on the first write instead.
	t.Run("non-flushable writer errors on first write", func(t *testing.T) {
		w := NewHTTPWriter(&nonFlusher{httptest.NewRecorder()})
		if err := w.Write(Message{Data: []byte("x")}); err == nil {
			t.Error("expected flush error on write, got nil")
		}
	})

	t.Run("ResponseWriter error is propagated", func(t *testing.T) {
		want := errors.New("connection closed")
		rw := &errorResponseWriter{header: make(http.Header), err: want}
		if err := NewHTTPWriter(rw).Write(Message{Data: []byte("x")}); !errors.Is(err, want) {
			t.Errorf("error = %v, want %v", err, want)
		}
	})
}
