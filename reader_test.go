package sse

import (
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}

// collectMessages reads all messages from a raw SSE string.
func collectMessages(input string) ([]Message, error) {
	r := NewReader(strings.NewReader(input))

	var msgs []Message
	for msg, err := range r.Messages() {
		if err != nil {
			return msgs, err
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

func TestStripBOM(t *testing.T) {
	t.Run("BOM stripped", func(t *testing.T) {
		r, err := stripBOM(strings.NewReader("\uFEFFhello"))
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(r)
		if string(got) != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})

	t.Run("no BOM passes through", func(t *testing.T) {
		r, err := stripBOM(strings.NewReader("hello"))
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(r)
		if string(got) != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})

	t.Run("empty reader", func(t *testing.T) {
		r, err := stripBOM(strings.NewReader(""))
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(r)
		if len(got) != 0 {
			t.Errorf("expected empty, got %q", got)
		}
	})

	t.Run("read error is wrapped", func(t *testing.T) {
		want := errors.New("read failed")
		if _, err := stripBOM(errorReader{err: want}); !errors.Is(err, want) {
			t.Fatalf("error = %v, want wrapped %v", err, want)
		}
	})
}

func TestDecodeUTF8(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{"valid fast path", []byte("hello 世界"), "hello 世界"},
		{"valid widths after malformed byte", []byte{0xff, 'A', 0xc2, 0xa2, 0xe1, 0x80, 0x80, 0xf1, 0x80, 0x80, 0x80}, "�A¢က\U00040000"},
		{"E0 lower boundary", []byte{0xe0, 0x80, 0x80}, "���"},
		{"ED upper boundary", []byte{0xed, 0xa0, 0x80}, "���"},
		{"F0 lower boundary", []byte{0xf0, 0x80, 0x80, 0x80}, "����"},
		{"F4 upper boundary", []byte{0xf4, 0x90, 0x80, 0x80}, "����"},
		{"continuation error reprocesses byte", []byte{0xe1, 0x80, 'A'}, "�A"},
		{"truncated sequence at EOF", []byte{0xf1, 0x80, 0x80}, "�"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(decodeUTF8(tt.in)); got != tt.want {
				t.Errorf("decodeUTF8(% x) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNewReader(t *testing.T) {
	t.Run("nil reader panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic, got none")
			}
		}()
		NewReader(nil)
	})

	t.Run("valid reader", func(t *testing.T) {
		r := NewReader(strings.NewReader(""))
		if r == nil {
			t.Error("expected non-nil Reader")
		}
	})

	t.Run("reads messages", func(t *testing.T) {
		r := NewReader(strings.NewReader("data: hello\n\n"))
		var msgs []Message
		for msg, err := range r.Messages() {
			if err != nil {
				t.Fatal(err)
			}
			msgs = append(msgs, msg)
		}
		if len(msgs) != 1 || string(msgs[0].Data) != "hello" {
			t.Errorf("unexpected messages: %v", msgs)
		}
	})

	t.Run("BOM read error is yielded", func(t *testing.T) {
		want := errors.New("read failed")
		r := NewReader(errorReader{err: want})
		var got error
		for _, err := range r.Messages() {
			got = err
		}
		if !errors.Is(got, want) {
			t.Fatalf("error = %v, want wrapped %v", got, want)
		}
	})
}

func TestReaderMaxLineBytes(t *testing.T) {
	large := strings.Repeat("x", 128*1024)
	input := "data: " + large + "\n\n"

	for name, ending := range map[string]string{
		"LF":   "\n",
		"CR":   "\r",
		"CRLF": "\r\n",
	} {
		t.Run("exact limit with "+name, func(t *testing.T) {
			const max = 16
			data := strings.Repeat("x", max-len("data: "))
			r := NewReader(strings.NewReader("data: " + data + ending + ending))
			r.MaxLineBytes = max
			msgs, err := collectAllFrom(r)
			if err != nil {
				t.Fatalf("line at exact limit: %v", err)
			}
			if len(msgs) != 1 || string(msgs[0].Data) != data {
				t.Errorf("got %v, want one message with data=%q", msgs, data)
			}
		})
	}

	t.Run("one byte over limit fails", func(t *testing.T) {
		const max = 16
		data := strings.Repeat("x", max-len("data: ")+1)
		r := NewReader(strings.NewReader("data: " + data + "\n\n"))
		r.MaxLineBytes = max
		if _, err := collectAllFrom(r); !errors.Is(err, ErrLineTooLong) {
			t.Fatalf("error = %v, want ErrLineTooLong", err)
		}
	})

	// A line larger than the scanner buffer surfaces via bufio; it must be
	// normalised to the same ErrLineTooLong as the in-loop check.
	t.Run("default limit rejects oversized line", func(t *testing.T) {
		r := NewReader(strings.NewReader(input))
		var gotErr error
		for _, err := range r.Messages() {
			gotErr = err
		}
		if !errors.Is(gotErr, ErrLineTooLong) {
			t.Errorf("error = %v, want ErrLineTooLong", gotErr)
		}
	})

	t.Run("raised limit accepts large line", func(t *testing.T) {
		r := NewReader(strings.NewReader(input))
		r.MaxLineBytes = 256 * 1024
		var msgs []Message
		for msg, err := range r.Messages() {
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			msgs = append(msgs, msg)
		}
		if len(msgs) != 1 {
			t.Fatalf("got %d messages, want 1", len(msgs))
		}
		if string(msgs[0].Data) != large {
			t.Errorf("data length = %d, want %d", len(msgs[0].Data), len(large))
		}
	})

	t.Run("zero falls back to default", func(t *testing.T) {
		r := NewReader(strings.NewReader("data: hello\n\n"))
		r.MaxLineBytes = 0
		msgs, err := collectAllFrom(r)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 || string(msgs[0].Data) != "hello" {
			t.Errorf("unexpected messages: %v", msgs)
		}
	})
}

func collectAllFrom(r *Reader) ([]Message, error) {
	var msgs []Message
	for msg, err := range r.Messages() {
		if err != nil {
			return msgs, err
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

func TestReaderMessagesCalledTwice(t *testing.T) {
	// A second call must reuse the existing scanner; once the stream is
	// exhausted it yields nothing without error.
	r := NewReader(strings.NewReader("id: 1\ndata: hello\n\n"))

	first, err := collectAllFrom(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatalf("first call: got %d messages, want 1", len(first))
	}

	second, err := collectAllFrom(r)
	if err != nil {
		t.Fatalf("second call: unexpected error: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("second call: got %d messages, want 0", len(second))
	}
}

func TestReaderReadPrimitive(t *testing.T) {
	r := NewReader(strings.NewReader(": heartbeat\n\nretry: 1500\n\ndata: first\n\ndata: second\n\n"))

	first, err := r.read()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(first.Data); got != "first" {
		t.Errorf("first Data = %q, want %q", got, "first")
	}
	if retry, ok := r.Retry(); !ok || retry != 1500*time.Millisecond {
		t.Errorf("Retry() = (%v, %v), want (1.5s, true)", retry, ok)
	}

	second, err := r.read()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(second.Data); got != "second" {
		t.Errorf("second Data = %q, want %q", got, "second")
	}

	if _, err := r.read(); err != io.EOF {
		t.Errorf("third read error = %v, want io.EOF", err)
	}
	if _, err := r.read(); err != io.EOF {
		t.Errorf("read after EOF error = %v, want io.EOF", err)
	}
}

func TestReaderReadPrimitiveSharesPosition(t *testing.T) {
	r := NewReader(strings.NewReader("data: first\n\ndata: second\n\n"))
	if _, err := r.read(); err != nil {
		t.Fatal(err)
	}

	msgs, err := collectAllFrom(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || string(msgs[0].Data) != "second" {
		t.Errorf("Messages after read = %v, want only second event", msgs)
	}
}

func TestReaderMessages(t *testing.T) {
	t.Run("basic data field", func(t *testing.T) {
		msgs, err := collectMessages("data: hello\n\n")
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 || string(msgs[0].Data) != "hello" {
			t.Fatalf("got %v, want one message with data=hello", msgs)
		}
	})

	t.Run("default event type is message", func(t *testing.T) {
		msgs, _ := collectMessages("data: hello\n\n")
		if msgs[0].Event != defaultEvent {
			t.Errorf("event = %q, want %q", msgs[0].Event, defaultEvent)
		}
	})

	t.Run("named event field", func(t *testing.T) {
		msgs, _ := collectMessages("event: update\ndata: hello\n\n")
		if msgs[0].Event != "update" {
			t.Errorf("event = %q, want %q", msgs[0].Event, "update")
		}
	})

	t.Run("id field", func(t *testing.T) {
		msgs, _ := collectMessages("id: 42\ndata: hello\n\n")
		if msgs[0].ID != "42" {
			t.Errorf("id = %q, want %q", msgs[0].ID, "42")
		}
	})

	t.Run("id persists across events", func(t *testing.T) {
		msgs, _ := collectMessages("id: 1\ndata: first\n\ndata: second\n\n")
		if len(msgs) != 2 {
			t.Fatalf("got %d messages, want 2", len(msgs))
		}
		if msgs[1].ID != "1" {
			t.Errorf("second event id = %q, want %q", msgs[1].ID, "1")
		}
	})

	t.Run("id containing null is ignored", func(t *testing.T) {
		msgs, _ := collectMessages("id: bad\x00id\ndata: hello\n\n")
		if msgs[0].ID != "" {
			t.Errorf("id = %q, want empty (null-containing id must be ignored)", msgs[0].ID)
		}
	})

	t.Run("retry field parsed as milliseconds", func(t *testing.T) {
		r := NewReader(strings.NewReader("retry: 3000\ndata: hello\n\n"))
		if _, err := collectAllFrom(r); err != nil {
			t.Fatal(err)
		}
		if retry, ok := r.Retry(); !ok || retry != 3*time.Second {
			t.Errorf("Retry() = (%v, %v), want (3s, true)", retry, ok)
		}
	})

	t.Run("retry with non-digit characters is ignored", func(t *testing.T) {
		r := NewReader(strings.NewReader("retry: 3s\ndata: hello\n\n"))
		if _, err := collectAllFrom(r); err != nil {
			t.Fatal(err)
		}
		if retry, ok := r.Retry(); ok {
			t.Errorf("Retry() = (%v, %v), want (_, false)", retry, ok)
		}
	})

	t.Run("overflowing retry is clamped, not wrapped", func(t *testing.T) {
		// A value far beyond int64 nanoseconds must not become negative.
		huge := strings.Repeat("9", 25)
		r := NewReader(strings.NewReader("retry: " + huge + "\ndata: hello\n\n"))
		if _, err := collectAllFrom(r); err != nil {
			t.Fatal(err)
		}
		if retry, ok := r.Retry(); !ok || retry != time.Duration(math.MaxInt64) {
			t.Errorf("Retry() = (%v, %v), want (%v, true)", retry, ok, time.Duration(math.MaxInt64))
		}
	})

	t.Run("retry duration boundary", func(t *testing.T) {
		tests := []struct {
			name string
			ms   uint64
			want time.Duration
		}{
			{"largest whole-millisecond duration", maxRetryMS, time.Duration(maxRetryMS) * time.Millisecond},
			{"one millisecond over duration range", maxRetryMS + 1, time.Duration(math.MaxInt64)},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				r := NewReader(strings.NewReader("retry: " + strconv.FormatUint(tt.ms, 10) + "\n"))
				if _, err := collectAllFrom(r); err != nil {
					t.Fatal(err)
				}
				if got, ok := r.Retry(); !ok || got != tt.want {
					t.Errorf("Retry() = (%v, %v), want (%v, true)", got, ok, tt.want)
				}
			})
		}
	})

	t.Run("comment lines are ignored", func(t *testing.T) {
		msgs, _ := collectMessages(": this is a comment\ndata: hello\n\n")
		if len(msgs) != 1 || string(msgs[0].Data) != "hello" {
			t.Errorf("got %v, want one message with data=hello", msgs)
		}
	})

	t.Run("empty data buffer suppresses dispatch", func(t *testing.T) {
		msgs, _ := collectMessages("event: ping\n\n")
		if len(msgs) != 0 {
			t.Errorf("got %d messages, want 0 (no data field)", len(msgs))
		}
	})

	t.Run("incomplete event at EOF is discarded", func(t *testing.T) {
		msgs, _ := collectMessages("data: hello")
		if len(msgs) != 0 {
			t.Errorf("got %d messages, want 0 (no trailing blank line)", len(msgs))
		}
	})

	t.Run("multi-line data joined with newlines", func(t *testing.T) {
		msgs, _ := collectMessages("data: line1\ndata: line2\ndata: line3\n\n")
		if want := "line1\nline2\nline3"; string(msgs[0].Data) != want {
			t.Errorf("data = %q, want %q", msgs[0].Data, want)
		}
	})

	t.Run("BOM at start of stream is stripped", func(t *testing.T) {
		msgs, _ := collectMessages("\uFEFFdata: hello\n\n")
		if len(msgs) != 1 || string(msgs[0].Data) != "hello" {
			t.Errorf("got %v, want one message with data=hello", msgs)
		}
	})

	t.Run("CRLF line endings accepted", func(t *testing.T) {
		msgs, _ := collectMessages("data: hello\r\n\r\n")
		if len(msgs) != 1 || string(msgs[0].Data) != "hello" {
			t.Errorf("got %v, want one message with data=hello", msgs)
		}
	})

	t.Run("CR-only line endings accepted", func(t *testing.T) {
		msgs, _ := collectMessages("data: hello\r\r")
		if len(msgs) != 1 || string(msgs[0].Data) != "hello" {
			t.Errorf("got %v, want one message with data=hello", msgs)
		}
	})

	t.Run("multiple sequential events", func(t *testing.T) {
		msgs, _ := collectMessages("data: a\n\ndata: b\n\ndata: c\n\n")
		if len(msgs) != 3 {
			t.Fatalf("got %d messages, want 3", len(msgs))
		}
		for i, want := range []string{"a", "b", "c"} {
			if string(msgs[i].Data) != want {
				t.Errorf("msgs[%d].Data = %q, want %q", i, msgs[i].Data, want)
			}
		}
	})
}
