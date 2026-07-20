package sse

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) {
	return len(p) - 1, nil
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
		if w := NewWriter(&bytes.Buffer{}); w == nil {
			t.Error("expected non-nil Writer")
		}
	})
}

// writeMessage encodes msg to a buffer and returns the output.
func writeMessage(t *testing.T, msg Message) string {
	t.Helper()
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.Write(msg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.String()
}

// renderComment encodes comment to a buffer and returns the output.
func renderComment(t *testing.T, comment string) string {
	t.Helper()
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.Comment(comment); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	return buf.String()
}

func TestWriterWrite(t *testing.T) {
	t.Run("data field written", func(t *testing.T) {
		if got := writeMessage(t, Message{Data: []byte("hello")}); !strings.Contains(got, "data: hello\n") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("event field written", func(t *testing.T) {
		if got := writeMessage(t, Message{Event: "update", Data: []byte("hello")}); !strings.Contains(got, "event: update\n") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("empty event field omitted", func(t *testing.T) {
		if got := writeMessage(t, Message{Data: []byte("hello")}); strings.Contains(got, "event:") {
			t.Errorf("empty event should be omitted, got %q", got)
		}
	})

	t.Run("id field written", func(t *testing.T) {
		if got := writeMessage(t, Message{ID: "42", Data: []byte("hello")}); !strings.Contains(got, "id: 42\n") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("empty id field omitted", func(t *testing.T) {
		if got := writeMessage(t, Message{Data: []byte("hello")}); strings.Contains(got, "id:") {
			t.Errorf("empty id should be omitted, got %q", got)
		}
	})

	t.Run("multi-line data splits into multiple data fields", func(t *testing.T) {
		got := writeMessage(t, Message{Data: []byte("line1\nline2")})
		if !strings.Contains(got, "data: line1\n") || !strings.Contains(got, "data: line2\n") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("empty data still emits a data line so the event dispatches", func(t *testing.T) {
		// Message carrying only a type must still produce a dispatchable event.
		got := writeMessage(t, Message{Event: "refresh"})
		if !strings.Contains(got, "\ndata:\n") && !strings.HasPrefix(got, "event: refresh\ndata:\n") {
			t.Errorf("expected an empty data line, got %q", got)
		}
	})

	t.Run("frame ends with blank line", func(t *testing.T) {
		if got := writeMessage(t, Message{Data: []byte("hello")}); !strings.HasSuffix(got, "\n\n") {
			t.Errorf("frame must end with blank line, got %q", got)
		}
	})

	t.Run("invalid ID returns error", func(t *testing.T) {
		var buf bytes.Buffer
		w := NewWriter(&buf)
		for _, id := range []string{"a\nb", "a\rb", "a\x00b"} {
			if err := w.Write(Message{ID: id, Data: []byte("x")}); err == nil {
				t.Errorf("ID %q: expected error, got nil", id)
			}
		}
		if buf.Len() != 0 {
			t.Errorf("nothing should be written on error, got %q", buf.String())
		}
	})

	t.Run("invalid event returns error", func(t *testing.T) {
		var buf bytes.Buffer
		w := NewWriter(&buf)
		for _, ev := range []string{"a\nb", "a\rb"} {
			if err := w.Write(Message{Event: ev, Data: []byte("x")}); err == nil {
				t.Errorf("Event %q: expected error, got nil", ev)
			}
		}
	})

	t.Run("invalid UTF-8 is rejected without writing", func(t *testing.T) {
		tests := []struct {
			name string
			msg  Message
		}{
			{"ID", Message{ID: string([]byte{0xff}), Data: []byte("x")}},
			{"event", Message{Event: string([]byte{0xff}), Data: []byte("x")}},
			{"data", Message{Data: []byte{0xff}}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var buf bytes.Buffer
				if err := NewWriter(&buf).Write(tt.msg); err == nil {
					t.Fatal("expected invalid UTF-8 error, got nil")
				}
				if buf.Len() != 0 {
					t.Errorf("wrote %q on validation error", buf.Bytes())
				}
			})
		}
	})
}

func TestWriterComment(t *testing.T) {
	t.Run("comment written as colon-prefixed line", func(t *testing.T) {
		if got := renderComment(t, "heartbeat"); got != ": heartbeat\n" {
			t.Errorf("got %q, want %q", got, ": heartbeat\n")
		}
	})

	t.Run("empty comment writes bare colon line", func(t *testing.T) {
		if got := renderComment(t, ""); got != ":\n" {
			t.Errorf("got %q, want %q", got, ":\n")
		}
	})

	t.Run("multi-line comment splits into multiple comment lines", func(t *testing.T) {
		if got := renderComment(t, "a\nb"); got != ": a\n: b\n" {
			t.Errorf("got %q, want %q", got, ": a\n: b\n")
		}
	})

	t.Run("CRLF is one line ending", func(t *testing.T) {
		if got := renderComment(t, "a\r\nb"); got != ": a\n: b\n" {
			t.Errorf("got %q, want %q", got, ": a\n: b\n")
		}
	})

	t.Run("trailing line ending preserves empty line", func(t *testing.T) {
		for _, comment := range []string{"a\n", "a\r", "a\r\n"} {
			if got := renderComment(t, comment); got != ": a\n:\n" {
				t.Errorf("Comment(%q) = %q, want %q", comment, got, ": a\n:\n")
			}
		}
	})

	t.Run("invalid UTF-8 is rejected without writing", func(t *testing.T) {
		var buf bytes.Buffer
		err := NewWriter(&buf).Comment(string([]byte{0xff}))
		if err == nil {
			t.Fatal("expected invalid UTF-8 error, got nil")
		}
		if buf.Len() != 0 {
			t.Errorf("wrote %q on validation error", buf.Bytes())
		}
	})
}

func TestWriterPropagatesWriteFailures(t *testing.T) {
	want := errors.New("write failed")
	operations := []struct {
		name string
		do   func(*Writer) error
	}{
		{"Write", func(w *Writer) error { return w.Write(Message{Data: []byte("x")}) }},
		{"Comment", func(w *Writer) error { return w.Comment("ping") }},
		{"Retry", func(w *Writer) error { return w.Retry(1) }},
		{"ResetID", func(w *Writer) error { return w.ResetID() }},
	}
	for _, op := range operations {
		t.Run(op.name, func(t *testing.T) {
			if err := op.do(NewWriter(errorWriter{err: want})); !errors.Is(err, want) {
				t.Errorf("error = %v, want %v", err, want)
			}
		})
	}

	t.Run("short write", func(t *testing.T) {
		err := NewWriter(shortWriter{}).Write(Message{Data: []byte("x")})
		if !errors.Is(err, io.ErrShortWrite) {
			t.Errorf("error = %v, want io.ErrShortWrite", err)
		}
	})
}

func TestRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
	}{
		{"data only", Message{Data: []byte("hello")}},
		{"all fields", Message{ID: "1", Event: "update", Data: []byte("hello world")}},
		{"multi-line data", Message{Data: []byte("line1\nline2\nline3")}},
		{"data ending with newline", Message{Data: []byte("hello\n")}},
		{"NUL in event and data", Message{Event: "x\x00y", Data: []byte("a\x00b")}},
		{"type only, empty data", Message{Event: "refresh"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := NewWriter(&buf)
			if err := w.Write(tt.msg); err != nil {
				t.Fatal(err)
			}

			msgs, err := collectMessages(buf.String())
			if err != nil {
				t.Fatal(err)
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
			wantEvent := tt.msg.Event
			if wantEvent == "" {
				wantEvent = defaultEvent
			}
			if got.Event != wantEvent {
				t.Errorf("event = %q, want %q", got.Event, wantEvent)
			}
		})
	}
}
