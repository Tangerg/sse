package sse

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// nonFlusher wraps http.ResponseWriter without exposing http.Flusher, so that
// NewHTTPWriter can be tested against a ResponseWriter that does not support
// streaming.
type nonFlusher struct {
	http.ResponseWriter
}

func TestSetSSEHeaders(t *testing.T) {
	t.Run("sets all required headers", func(t *testing.T) {
		h := http.Header{}
		setSSEHeaders(h)

		if got := h.Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
			t.Errorf("Content-Type = %q", got)
		}
		if got := h.Get("Connection"); got != "keep-alive" {
			t.Errorf("Connection = %q", got)
		}
		if got := h.Get("Cache-Control"); got != "no-cache" {
			t.Errorf("Cache-Control = %q", got)
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

func TestNewWriter(t *testing.T) {
	t.Run("nil writer panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic, got none")
			}
		}()
		NewWriter(nil)
	})

	t.Run("valid writer", func(t *testing.T) {
		w := NewWriter(&bytes.Buffer{})
		if w == nil {
			t.Error("expected non-nil Writer")
		}
	})
}

func TestNewHTTPWriter(t *testing.T) {
	t.Run("nil ResponseWriter returns error", func(t *testing.T) {
		_, err := NewHTTPWriter(nil)
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("non-flusher ResponseWriter returns error", func(t *testing.T) {
		_, err := NewHTTPWriter(&nonFlusher{httptest.NewRecorder()})
		if err == nil {
			t.Error("expected error for ResponseWriter that does not implement http.Flusher")
		}
	})

	t.Run("valid ResponseWriter sets SSE headers", func(t *testing.T) {
		rr := httptest.NewRecorder()
		w, err := NewHTTPWriter(rr)
		if err != nil {
			t.Fatal(err)
		}
		if w == nil {
			t.Error("expected non-nil Writer")
		}
		if got := rr.Header().Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
			t.Errorf("Content-Type = %q", got)
		}
	})
}

// writeMessage is a helper that encodes msg to a buffer and returns the output.
func writeMessage(t *testing.T, msg Message) string {
	t.Helper()
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.Message(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// writeComment is a helper that encodes comment to a buffer and returns the output.
func writeComment(t *testing.T, comment string) string {
	t.Helper()
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.Comment(context.Background(), comment); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestWriterMessage(t *testing.T) {
	t.Run("data field written", func(t *testing.T) {
		got := writeMessage(t, Message{Data: []byte("hello")})
		if !strings.Contains(got, "data: hello\n") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("event field written", func(t *testing.T) {
		got := writeMessage(t, Message{Event: "update", Data: []byte("hello")})
		if !strings.Contains(got, "event: update\n") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("empty event field omitted", func(t *testing.T) {
		got := writeMessage(t, Message{Data: []byte("hello")})
		if strings.Contains(got, "event:") {
			t.Errorf("empty event should be omitted, got %q", got)
		}
	})

	t.Run("id field written", func(t *testing.T) {
		got := writeMessage(t, Message{ID: "42", Data: []byte("hello")})
		if !strings.Contains(got, "id: 42\n") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("empty id field omitted", func(t *testing.T) {
		got := writeMessage(t, Message{Data: []byte("hello")})
		if strings.Contains(got, "id:") {
			t.Errorf("empty id should be omitted, got %q", got)
		}
	})

	t.Run("retry field written in milliseconds", func(t *testing.T) {
		got := writeMessage(t, Message{Data: []byte("hello"), Retry: 5 * time.Second})
		if !strings.Contains(got, "retry: 5000\n") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("zero retry field omitted", func(t *testing.T) {
		got := writeMessage(t, Message{Data: []byte("hello")})
		if strings.Contains(got, "retry:") {
			t.Errorf("zero retry should be omitted, got %q", got)
		}
	})

	t.Run("multi-line data splits into multiple data fields", func(t *testing.T) {
		got := writeMessage(t, Message{Data: []byte("line1\nline2")})
		if !strings.Contains(got, "data: line1\n") || !strings.Contains(got, "data: line2\n") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("newlines stripped from field values", func(t *testing.T) {
		got := writeMessage(t, Message{Event: "up\ndate", Data: []byte("hello")})
		// The injected newline must not appear inside the event field value.
		if strings.Contains(got, "event: up\ndate") {
			t.Errorf("newline not stripped from event field: %q", got)
		}
	})

	t.Run("frame ends with blank line", func(t *testing.T) {
		got := writeMessage(t, Message{Data: []byte("hello")})
		if !strings.HasSuffix(got, "\n\n") {
			t.Errorf("frame must end with blank line, got %q", got)
		}
	})

	t.Run("empty data field omitted", func(t *testing.T) {
		got := writeMessage(t, Message{})
		if strings.Contains(got, "data:") {
			t.Errorf("empty data should be omitted, got %q", got)
		}
	})
}

func TestWriterComment(t *testing.T) {
	t.Run("comment written as colon-prefixed line", func(t *testing.T) {
		got := writeComment(t, "heartbeat")
		if !strings.Contains(got, ": heartbeat\n") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("comment frame ends with blank line", func(t *testing.T) {
		got := writeComment(t, "ping")
		if !strings.HasSuffix(got, "\n\n") {
			t.Errorf("comment frame must end with blank line, got %q", got)
		}
	})

	t.Run("empty comment writes bare colon line", func(t *testing.T) {
		got := writeComment(t, "")
		if got != ":\n\n" {
			t.Errorf("got %q, want %q", got, ":\n\n")
		}
	})
}

func TestRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
	}{
		{
			name: "data only",
			msg:  Message{Data: []byte("hello")},
		},
		{
			name: "all fields",
			msg: Message{
				ID:    "1",
				Event: "update",
				Data:  []byte("hello world"),
				Retry: 3 * time.Second,
			},
		},
		{
			name: "multi-line data",
			msg:  Message{Data: []byte("line1\nline2\nline3")},
		},
		{
			name: "data ending with newline",
			msg:  Message{Data: []byte("hello\n")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Write.
			var buf bytes.Buffer
			w := NewWriter(&buf)
			if err := w.Message(context.Background(), tt.msg); err != nil {
				t.Fatal(err)
			}

			// Read back.
			r := NewReader(strings.NewReader(buf.String()))
			var msgs []Message
			for msg, err := range r.Messages(context.Background()) {
				if err != nil {
					t.Fatal(err)
				}
				msgs = append(msgs, msg)
			}

			if len(msgs) != 1 {
				t.Fatalf("got %d messages, want 1", len(msgs))
			}

			got := msgs[0]
			if string(got.Data) != string(tt.msg.Data) {
				t.Errorf("data = %q, want %q", got.Data, tt.msg.Data)
			}
			if tt.msg.ID != "" && got.ID != tt.msg.ID {
				t.Errorf("id = %q, want %q", got.ID, tt.msg.ID)
			}
			if tt.msg.Event != "" && got.Event != tt.msg.Event {
				t.Errorf("event = %q, want %q", got.Event, tt.msg.Event)
			}
			if tt.msg.Retry != 0 && got.Retry != tt.msg.Retry {
				t.Errorf("retry = %v, want %v", got.Retry, tt.msg.Retry)
			}
		})
	}
}
