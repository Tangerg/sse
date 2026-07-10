package sse

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

// FuzzReader checks that parsing arbitrary bytes never panics and always
// terminates. Correctness of specific outputs is covered by the table tests;
// this guards the state machine against malformed input.
func FuzzReader(f *testing.F) {
	seeds := []string{
		"",
		"\uFEFF",
		"data: hello\n\n",
		"event: x\ndata: y\nid: 1\nretry: 100\n\n",
		": comment\n\n",
		"data\r\ndata\rdata\n\n",
		"id: a\x00b\ndata: x\n\n",
		"retry: 99999999999999999999999\n\n",
		"data: no trailing blank line",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, in []byte) {
		r := NewReader(bytes.NewReader(in))
		r.MaxLineBytes = len(in) + 16 // never fail solely on the line-size limit
		for _, err := range r.Messages() {
			if err != nil {
				break
			}
		}
	})
}

// dripReader returns at most n bytes per Read, forcing tokens (and line endings
// such as CRLF) to straddle Read boundaries.
type dripReader struct {
	data []byte
	n    int
}

func (d *dripReader) Read(p []byte) (int, error) {
	if len(d.data) == 0 {
		return 0, io.EOF
	}
	k := min(len(p), min(d.n, len(d.data)))
	copy(p, d.data[:k])
	d.data = d.data[k:]
	return k, nil
}

// FuzzChunking checks that parsing is independent of how the underlying reader
// chunks the bytes: a byte-at-a-time (or small-chunk) reader must yield the same
// events as a whole-buffer reader. This exercises splitLine's CR/LF/CRLF
// boundary handling under adversarial framing.
func FuzzChunking(f *testing.F) {
	for _, s := range []string{
		"data: a\r\n\r\n",
		"data: a\rdata: b\r\n\r\n",
		"data: x\n\ndata: y\r\r",
		"\uFEFFdata: bom\n\n",
		"id: 1\ndata: hi\r\n\r\n",
	} {
		f.Add([]byte(s), 1)
	}

	// collect returns a canonical string covering every observable field of every
	// event plus the final reconnection state, so chunking must not change ID,
	// Retry, LastEventID, or the trailing Retry either.
	collect := func(r *Reader) (string, error) {
		var b strings.Builder
		for msg, err := range r.Messages() {
			if err != nil {
				return b.String(), err
			}
			fmt.Fprintf(&b, "id=%q event=%q data=%q retry=%d\n", msg.ID, msg.Event, msg.Data, msg.Retry)
		}
		d, ok := r.Retry()
		fmt.Fprintf(&b, "final: lastID=%q retry=%d,%t", r.LastEventID(), d, ok)
		return b.String(), nil
	}

	f.Fuzz(func(t *testing.T, in []byte, chunk int) {
		if chunk < 1 {
			chunk = 1
		}

		whole := NewReader(bytes.NewReader(in))
		whole.MaxLineBytes = len(in) + 16
		want, wErr := collect(whole)

		dripped := NewReader(&dripReader{data: append([]byte(nil), in...), n: chunk})
		dripped.MaxLineBytes = len(in) + 16
		got, gErr := collect(dripped)

		if (wErr == nil) != (gErr == nil) {
			t.Fatalf("error mismatch: whole=%v dripped=%v", wErr, gErr)
		}
		if want != got {
			t.Errorf("chunking changed the result (in=%q chunk=%d):\nwhole:\n%s\ndripped:\n%s", in, chunk, want, got)
		}
	})
}

// FuzzRoundTrip checks that any Data payload survives a write→read cycle, up to
// the spec-mandated normalisation of line endings to LF (the writer splits on
// CR/LF/CRLF and the reader rejoins with LF).
func FuzzRoundTrip(f *testing.F) {
	for _, s := range []string{"", "hello", "a\nb", "a\r\nb", "a\rb", "line\n", "\n", "a\x00b", "🚀"} {
		f.Add([]byte(s))
	}

	normalise := func(b []byte) string {
		s := string(b)
		s = strings.ReplaceAll(s, "\r\n", "\n")
		s = strings.ReplaceAll(s, "\r", "\n")
		return s
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var buf bytes.Buffer
		w := NewWriter(&buf)
		if err := w.Message(Message{Data: data}); err != nil {
			t.Fatalf("Message: %v", err)
		}

		r := NewReader(&buf)
		r.MaxLineBytes = len(data) + 16
		msgs, err := collectAllFrom(r)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if len(msgs) != 1 {
			t.Fatalf("got %d messages, want 1", len(msgs))
		}
		if got, want := string(msgs[0].Data), normalise(data); got != want {
			t.Errorf("round-trip: got %q, want %q (from %q)", got, want, data)
		}
	})
}
