package sse

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Writer control frames
// ---------------------------------------------------------------------------

func TestWriterRetryFrame(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.Retry(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "retry: 5000\n\n" {
		t.Errorf("got %q, want %q", got, "retry: 5000\n\n")
	}
}

func TestWriterRetryZero(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.Retry(0); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "retry: 0\n\n" {
		t.Errorf("got %q, want %q", got, "retry: 0\n\n")
	}
}

func TestWriterRetryNegativeError(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.Retry(-time.Second); err == nil {
		t.Error("expected error for negative delay, got nil")
	}
	if buf.Len() != 0 {
		t.Errorf("nothing should be written on error, got %q", buf.String())
	}
}

// A duration near math.MaxInt64 must not overflow ceilMillis into a negative
// retry, and must round-trip to a positive (clamped) value.
func TestWriterRetryMaxDurationNoOverflow(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.Retry(time.Duration(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "-") {
		t.Fatalf("retry frame contains a negative value: %q", out)
	}

	r := NewReader(strings.NewReader(out))
	if _, err := collectAllFrom(r); err != nil {
		t.Fatal(err)
	}
	d, ok := r.Retry()
	if !ok || d <= 0 {
		t.Errorf("round-trip Retry() = (%v, %v), want a positive clamped duration", d, ok)
	}
}

func TestWriterRetrySubMillisecondRoundsUp(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.Retry(500 * time.Microsecond); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "retry: 1\n\n" {
		t.Errorf("got %q, want %q (sub-ms must round up to 1)", got, "retry: 1\n\n")
	}
}

func TestWriterResetIDFrame(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.ResetID(); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "id:\n\n" {
		t.Errorf("got %q, want %q", got, "id:\n\n")
	}
}

// ---------------------------------------------------------------------------
// Reader getters
// ---------------------------------------------------------------------------

func TestReaderRetryGetter(t *testing.T) {
	t.Run("unset before any retry", func(t *testing.T) {
		r := NewReader(strings.NewReader("data: x\n\n"))
		if _, err := collectAllFrom(r); err != nil {
			t.Fatal(err)
		}
		if d, ok := r.Retry(); ok {
			t.Errorf("Retry() = (%v, %v), want (_, false)", d, ok)
		}
	})

	// A standalone retry frame carries no event, so only the getter surfaces it.
	t.Run("standalone retry frame at end of stream", func(t *testing.T) {
		r := NewReader(strings.NewReader("data: x\n\nretry: 7000\n\n"))
		msgs, err := collectAllFrom(r)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 {
			t.Fatalf("got %d messages, want 1", len(msgs))
		}
		d, ok := r.Retry()
		if !ok || d != 7*time.Second {
			t.Errorf("Retry() = (%v, %v), want (7s, true)", d, ok)
		}
	})
}

func TestReaderLastEventIDGetter(t *testing.T) {
	t.Run("reports last dispatched id", func(t *testing.T) {
		r := NewReader(strings.NewReader("id: 5\ndata: x\n\n"))
		if _, err := collectAllFrom(r); err != nil {
			t.Fatal(err)
		}
		if got := r.LastEventID(); got != "5" {
			t.Errorf("LastEventID() = %q, want %q", got, "5")
		}
	})

	// An id in a trailing event that never dispatched must not change the
	// reconnection state (spec: last event ID string is set at dispatch).
	t.Run("id in incomplete trailing event is ignored", func(t *testing.T) {
		r := NewReader(strings.NewReader("id: 1\ndata: first\n\nid: 2\ndata: orphan"))
		msgs, err := collectAllFrom(r)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 {
			t.Fatalf("got %d messages, want 1", len(msgs))
		}
		if got := r.LastEventID(); got != "1" {
			t.Errorf("LastEventID() = %q, want %q (id:2 never dispatched)", got, "1")
		}
	})

	// A standalone id reset frame updates the reconnection state even though it
	// dispatches no event.
	t.Run("standalone id reset frame", func(t *testing.T) {
		r := NewReader(strings.NewReader("id: 5\ndata: x\n\nid:\n\n"))
		msgs, err := collectAllFrom(r)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 || msgs[0].ID != "5" {
			t.Fatalf("got %v, want one message with id=5", msgs)
		}
		if got := r.LastEventID(); got != "" {
			t.Errorf("LastEventID() = %q, want empty (reset by standalone id: frame)", got)
		}
	})
}

// ---------------------------------------------------------------------------
// MaxEventBytes
// ---------------------------------------------------------------------------

func TestReaderMaxEventBytes(t *testing.T) {
	t.Run("exceeding limit yields ErrEventTooLarge", func(t *testing.T) {
		r := NewReader(strings.NewReader("data: aaaa\ndata: bbbb\n\n"))
		r.MaxEventBytes = 8 // one "aaaa\n" is 5 bytes; the second line trips it
		var gotErr error
		for _, err := range r.Messages() {
			if err != nil {
				gotErr = err
				break
			}
		}
		if !errors.Is(gotErr, ErrEventTooLarge) {
			t.Errorf("error = %v, want ErrEventTooLarge", gotErr)
		}
	})

	// After ErrEventTooLarge the Reader is terminal: a second Messages call must
	// keep reporting the error and must not dispatch the oversized event.
	t.Run("error is terminal, oversized event never dispatches", func(t *testing.T) {
		r := NewReader(strings.NewReader("data: aaaa\ndata: bbbb\n\n"))
		r.MaxEventBytes = 8

		var first error
		var firstMsgs int
		for msg, err := range r.Messages() {
			if err != nil {
				first = err
				break
			}
			_ = msg
			firstMsgs++
		}
		if !errors.Is(first, ErrEventTooLarge) || firstMsgs != 0 {
			t.Fatalf("first call: msgs=%d err=%v, want 0 msgs and ErrEventTooLarge", firstMsgs, first)
		}

		var second error
		var secondMsgs int
		for msg, err := range r.Messages() {
			if err != nil {
				second = err
				break
			}
			_ = msg
			secondMsgs++
		}
		if !errors.Is(second, ErrEventTooLarge) {
			t.Errorf("second call: err = %v, want ErrEventTooLarge (terminal)", second)
		}
		if secondMsgs != 0 {
			t.Errorf("second call: dispatched %d messages, want 0 (oversized event must not resume)", secondMsgs)
		}
	})

	t.Run("within limit passes", func(t *testing.T) {
		r := NewReader(strings.NewReader("data: aaaa\n\n"))
		r.MaxEventBytes = 100
		msgs, err := collectAllFrom(r)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 || string(msgs[0].Data) != "aaaa" {
			t.Errorf("got %v, want one message with data=aaaa", msgs)
		}
	})

	t.Run("zero means unlimited", func(t *testing.T) {
		big := strings.Repeat("data: x\n", 10000) + "\n"
		r := NewReader(strings.NewReader(big))
		r.MaxEventBytes = 0
		msgs, err := collectAllFrom(r)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(msgs) != 1 {
			t.Fatalf("got %d messages, want 1", len(msgs))
		}
	})
}

// ---------------------------------------------------------------------------
// Control-frame round trips
// ---------------------------------------------------------------------------

func TestControlFrameRoundTrip(t *testing.T) {
	t.Run("Writer.Retry to Reader.Retry", func(t *testing.T) {
		var buf bytes.Buffer
		w := NewWriter(&buf)
		if err := w.Retry(7 * time.Second); err != nil {
			t.Fatal(err)
		}
		r := NewReader(&buf)
		msgs, err := collectAllFrom(r)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 0 {
			t.Errorf("got %d messages, want 0 (control frame dispatches nothing)", len(msgs))
		}
		if d, ok := r.Retry(); !ok || d != 7*time.Second {
			t.Errorf("Retry() = (%v, %v), want (7s, true)", d, ok)
		}
	})

	t.Run("Writer.ResetID to Reader.LastEventID", func(t *testing.T) {
		var buf bytes.Buffer
		w := NewWriter(&buf)
		if err := w.Write(Message{ID: "5", Data: []byte("x")}); err != nil {
			t.Fatal(err)
		}
		if err := w.ResetID(); err != nil {
			t.Fatal(err)
		}
		r := NewReader(&buf)
		msgs, err := collectAllFrom(r)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 || msgs[0].ID != "5" {
			t.Fatalf("got %v, want one message with id=5", msgs)
		}
		if got := r.LastEventID(); got != "" {
			t.Errorf("LastEventID() = %q, want empty after ResetID", got)
		}
	})
}
