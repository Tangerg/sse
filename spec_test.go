package sse

import (
	"strings"
	"testing"
	"time"
)

// Tests in this file verify conformance with the WHATWG HTML Living Standard
// §9.2 (Server-Sent Events). Each test cites the spec clause it exercises so
// reviewers can audit behaviour against the source of truth in spec.md.
//
// Basics already covered by reader_test.go / writer_test.go / lines_test.go
// (BOM stripping at start, line endings, id/event/retry/comment basics,
// leading-space-stripped-once, EOF-mid-event discarded, etc.) are not
// duplicated here.

// ---------------------------------------------------------------------------
// §9.2.6 — field-name comparison, unknown fields, value semantics
// ---------------------------------------------------------------------------

// TestSpec_FieldNameCaseSensitive verifies §9.2.6: "Field names must be
// compared literally, with no case folding performed." Mixed-case variants of
// recognised names are therefore unknown fields and are ignored.
func TestSpec_FieldNameCaseSensitive(t *testing.T) {
	in := "DATA: x\nEvent: y\nID: 1\nRETRY: 1000\n\n"
	msgs, err := collectMessages(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("got %d messages, want 0 (case-mismatched field names must be ignored)", len(msgs))
	}
}

// TestSpec_UnknownFieldIgnored verifies §9.2.6: "Otherwise: The field is
// ignored."
func TestSpec_UnknownFieldIgnored(t *testing.T) {
	msgs, err := collectMessages("foo: bar\nbaz: qux\ndata: hello\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || string(msgs[0].Data) != "hello" {
		t.Errorf("got %v, want one message with data=hello", msgs)
	}
}

// TestSpec_NoLeadingSpaceNotStripped verifies §9.2.6: only one leading SPACE is
// removed from a value, and only if present.
func TestSpec_NoLeadingSpaceNotStripped(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"no space", "data:test\n\n", "test"},
		{"one space", "data: test\n\n", "test"},
		{"two spaces — one preserved", "data:  test\n\n", " test"},
		{"three spaces — two preserved", "data:   test\n\n", "  test"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs, err := collectMessages(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if len(msgs) != 1 {
				t.Fatalf("got %d messages, want 1", len(msgs))
			}
			if got := string(msgs[0].Data); got != tc.want {
				t.Errorf("data = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSpec_ValueContainsColons verifies §9.2.6: the split is on the FIRST colon
// only — subsequent colons belong to the value verbatim.
func TestSpec_ValueContainsColons(t *testing.T) {
	msgs, err := collectMessages("data: a:b:c\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(msgs[0].Data); got != "a:b:c" {
		t.Errorf("data = %q, want %q", got, "a:b:c")
	}
}

// TestSpec_BareDataLineDispatchesEmptyData verifies §9.2.6: a line with no
// colon uses the whole line as the field name and the empty string as value. A
// bare "data" line appends "" + LF, which is non-empty and dispatches an event
// with empty Data.
func TestSpec_BareDataLineDispatchesEmptyData(t *testing.T) {
	msgs, err := collectMessages("data\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if got := string(msgs[0].Data); got != "" {
		t.Errorf("data = %q, want empty string", got)
	}
}

// TestSpec_TwoBareDataLinesProduceNewline verifies §9.2.6: two bare "data"
// lines append "" + LF + "" + LF; dispatch strips the trailing LF, leaving a
// single LF.
func TestSpec_TwoBareDataLinesProduceNewline(t *testing.T) {
	msgs, err := collectMessages("data\ndata\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if got := string(msgs[0].Data); got != "\n" {
		t.Errorf("data = %q, want %q", got, "\n")
	}
}

// ---------------------------------------------------------------------------
// §9.2.6 — id field semantics
// ---------------------------------------------------------------------------

// TestSpec_EmptyIDResetsLastEventID verifies §9.2.6: an "id" field whose value
// is empty is still recorded (it contains no NULL), resetting the
// last-event-ID buffer.
func TestSpec_EmptyIDResetsLastEventID(t *testing.T) {
	in := "id: 1\ndata: first\n\nid\ndata: second\n\ndata: third\n\n"
	msgs, err := collectMessages(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3", len(msgs))
	}
	for i, want := range []string{"1", "", ""} {
		if msgs[i].ID != want {
			t.Errorf("msgs[%d].ID = %q, want %q", i, msgs[i].ID, want)
		}
	}
}

// TestSpec_IDWithEmptyValueAfterColon verifies that "id:" (colon, no value) is
// treated like bare "id" — both reset the last-event-ID buffer.
func TestSpec_IDWithEmptyValueAfterColon(t *testing.T) {
	msgs, err := collectMessages("id: 1\ndata: first\n\nid:\ndata: second\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[0].ID != "1" || msgs[1].ID != "" {
		t.Errorf("IDs = [%q, %q], want [%q, %q]", msgs[0].ID, msgs[1].ID, "1", "")
	}
}

// ---------------------------------------------------------------------------
// §9.2.6 — retry field edge cases
// ---------------------------------------------------------------------------

// TestSpec_RetryRequiresOnlyDigits verifies §9.2.6: "If the field value
// consists of only ASCII digits…" — anything else is ignored, leaving the
// reconnection time unchanged (0 here, as none was set before).
func TestSpec_RetryRequiresOnlyDigits(t *testing.T) {
	cases := []struct{ name, line string }{
		{"empty value", "retry: \n"},
		{"empty value no space", "retry:\n"},
		{"negative", "retry: -100\n"},
		{"plus sign", "retry: +100\n"},
		{"trailing letter", "retry: 100ms\n"},
		{"leading letter", "retry: a100\n"},
		{"fractional", "retry: 1.5\n"},
		{"hex", "retry: 0xff\n"},
		// Overflowing digits followed by a non-digit: must be ignored for the
		// non-digit, not clamped as an overflowing integer.
		{"overflow then non-digit suffix", "retry: 999999999999999999999999x\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs, err := collectMessages(tc.line + "data: x\n\n")
			if err != nil {
				t.Fatal(err)
			}
			if len(msgs) != 1 {
				t.Fatalf("got %d messages, want 1", len(msgs))
			}
			if msgs[0].Retry != 0 {
				t.Errorf("Retry = %v, want 0 (invalid value must be ignored)", msgs[0].Retry)
			}
		})
	}
}

// TestSpec_RetryAcceptsLeadingZeros verifies that a digits-only value is
// interpreted in base ten, which permits leading zeros.
func TestSpec_RetryAcceptsLeadingZeros(t *testing.T) {
	msgs, err := collectMessages("retry: 003000\ndata: x\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if msgs[0].Retry != 3*time.Second {
		t.Errorf("Retry = %v, want 3s", msgs[0].Retry)
	}
}

// TestSpec_RetryPersistsAcrossEvents verifies that the reconnection time is
// stream-level state: unlike the data and event-type buffers it is NOT reset at
// dispatch, so it is reported on every subsequent event (§9.2.6 — dispatch
// resets only the data and event-type buffers).
func TestSpec_RetryPersistsAcrossEvents(t *testing.T) {
	msgs, err := collectMessages("retry: 5000\ndata: first\n\ndata: second\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[0].Retry != 5*time.Second {
		t.Errorf("msgs[0].Retry = %v, want 5s", msgs[0].Retry)
	}
	if msgs[1].Retry != 5*time.Second {
		t.Errorf("msgs[1].Retry = %v, want 5s (retry persists across events)", msgs[1].Retry)
	}
}

// ---------------------------------------------------------------------------
// §9.2.6 — event-type buffer semantics
// ---------------------------------------------------------------------------

// TestSpec_EventTypeClearedBetweenEvents verifies §9.2.6: dispatch sets the
// event type buffer back to the empty string, so a type does not leak between
// events.
func TestSpec_EventTypeClearedBetweenEvents(t *testing.T) {
	msgs, err := collectMessages("event: update\ndata: first\n\ndata: second\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[0].Event != "update" {
		t.Errorf("msgs[0].Event = %q, want %q", msgs[0].Event, "update")
	}
	if msgs[1].Event != defaultEvent {
		t.Errorf("msgs[1].Event = %q, want %q (event type must reset between events)", msgs[1].Event, defaultEvent)
	}
}

// TestSpec_EventTypeOverwrittenWithinEvent verifies that a later "event" field
// overwrites an earlier one within the same dispatch.
func TestSpec_EventTypeOverwrittenWithinEvent(t *testing.T) {
	msgs, err := collectMessages("event: first\nevent: second\ndata: x\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if msgs[0].Event != "second" {
		t.Errorf("Event = %q, want %q", msgs[0].Event, "second")
	}
}

// ---------------------------------------------------------------------------
// §9.2.5 — line endings and BOM rules
// ---------------------------------------------------------------------------

// TestSpec_MixedLineEndings verifies §9.2.5: a stream may freely mix CRLF, lone
// LF, and lone CR.
func TestSpec_MixedLineEndings(t *testing.T) {
	in := "data: a\n\ndata: b\r\n\r\ndata: c\r\r"
	msgs, err := collectMessages(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3", len(msgs))
	}
	for i, want := range []string{"a", "b", "c"} {
		if got := string(msgs[i].Data); got != want {
			t.Errorf("msgs[%d].Data = %q, want %q", i, got, want)
		}
	}
}

// TestSpec_BOMOnlyStrippedAtStart verifies §9.2.6: only ONE leading BOM is
// stripped — a BOM elsewhere is part of the value as ordinary UTF-8.
func TestSpec_BOMOnlyStrippedAtStart(t *testing.T) {
	in := "\uFEFFdata: a\uFEFFb\n\n"
	msgs, err := collectMessages(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if got := string(msgs[0].Data); got != "a\uFEFFb" {
		t.Errorf("data = %q, want %q (interior BOM must be preserved)", got, "a\uFEFFb")
	}
}

// TestSpec_StreamOfOnlyComments verifies that a comment-only stream dispatches
// nothing (empty data buffer is discarded).
func TestSpec_StreamOfOnlyComments(t *testing.T) {
	msgs, err := collectMessages(": one\n: two\n: three\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("got %d messages, want 0 (comments must not dispatch events)", len(msgs))
	}
}

// TestSpec_EmptyStream verifies that empty input produces neither errors nor
// messages.
func TestSpec_EmptyStream(t *testing.T) {
	msgs, err := collectMessages("")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("got %d messages, want 0", len(msgs))
	}
}

// TestSpec_StreamOfOnlyBOM verifies that a BOM-only stream yields nothing.
func TestSpec_StreamOfOnlyBOM(t *testing.T) {
	msgs, err := collectMessages("\uFEFF")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("got %d messages, want 0", len(msgs))
	}
}

// TestSpec_UTF8MultiByteData verifies §9.2.5: multi-byte UTF-8 characters in
// data are preserved verbatim.
func TestSpec_UTF8MultiByteData(t *testing.T) {
	const payload = "你好，世界 — αβγ — 🚀"
	msgs, err := collectMessages("data: " + payload + "\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if got := string(msgs[0].Data); got != payload {
		t.Errorf("data = %q, want %q", got, payload)
	}
}

// ---------------------------------------------------------------------------
// §9.2.6 — verbatim spec examples
// ---------------------------------------------------------------------------

// TestSpec_Example_StockTicker reproduces the canonical stock-ticker example
// from §9.2.6.
func TestSpec_Example_StockTicker(t *testing.T) {
	msgs, err := collectMessages("data: YHOO\ndata: +2\ndata: 10\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	const want = "YHOO\n+2\n10"
	if got := string(msgs[0].Data); got != want {
		t.Errorf("data = %q, want %q", got, want)
	}
	if msgs[0].Event != "message" {
		t.Errorf("event = %q, want %q (default)", msgs[0].Event, "message")
	}
}

// TestSpec_Example_FourBlocks reproduces the four-block example from §9.2.6
// exercising comments, id propagation, id reset by empty value, and the
// single-leading-space rule.
func TestSpec_Example_FourBlocks(t *testing.T) {
	in := ": test stream\n\n" +
		"data: first event\nid: 1\n\n" +
		"data:second event\nid\n\n" +
		"data:  third event\n\n"

	msgs, err := collectMessages(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3", len(msgs))
	}

	want := []Message{
		{ID: "1", Event: "message", Data: []byte("first event")},
		{ID: "", Event: "message", Data: []byte("second event")},
		{ID: "", Event: "message", Data: []byte(" third event")}, // one space preserved
	}
	for i, w := range want {
		if msgs[i].ID != w.ID {
			t.Errorf("msgs[%d].ID = %q, want %q", i, msgs[i].ID, w.ID)
		}
		if msgs[i].Event != w.Event {
			t.Errorf("msgs[%d].Event = %q, want %q", i, msgs[i].Event, w.Event)
		}
		if string(msgs[i].Data) != string(w.Data) {
			t.Errorf("msgs[%d].Data = %q, want %q", i, msgs[i].Data, w.Data)
		}
	}
}

// TestSpec_Example_DataOnlyVariations reproduces the empty-data example from
// §9.2.6: the first block fires an empty-string event, the middle block fires a
// single-newline event, and the last block is discarded (no blank line).
func TestSpec_Example_DataOnlyVariations(t *testing.T) {
	in := "data\n\ndata\ndata\n\ndata:\n"
	msgs, err := collectMessages(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (last block missing blank line is discarded)", len(msgs))
	}
	if got := string(msgs[0].Data); got != "" {
		t.Errorf("msgs[0].Data = %q, want empty string", got)
	}
	if got := string(msgs[1].Data); got != "\n" {
		t.Errorf("msgs[1].Data = %q, want %q", got, "\n")
	}
}

// TestSpec_Example_ColonSpaceEquivalence reproduces the final §9.2.6 example:
// "data:test" and "data: test" fire identical events.
func TestSpec_Example_ColonSpaceEquivalence(t *testing.T) {
	msgs, err := collectMessages("data:test\n\ndata: test\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if string(msgs[0].Data) != string(msgs[1].Data) {
		t.Errorf("msgs[0] = %q, msgs[1] = %q (must be identical)", msgs[0].Data, msgs[1].Data)
	}
	if got := string(msgs[0].Data); got != "test" {
		t.Errorf("data = %q, want %q", got, "test")
	}
}

// ---------------------------------------------------------------------------
// §9.2.5 — Writer wire-format conformance
// ---------------------------------------------------------------------------

// TestSpec_WriterRejectsCRLFInIDAndEvent verifies §9.2.5 ABNF: field values use
// any-char, which excludes CR/LF. Rather than silently stripping them (which
// would corrupt identifiers), the writer rejects such values with an error.
func TestSpec_WriterRejectsCRLFInIDAndEvent(t *testing.T) {
	cases := []struct {
		name string
		msg  Message
	}{
		{"id with LF", Message{ID: "1\n2", Data: []byte("x")}},
		{"id with CR", Message{ID: "1\r2", Data: []byte("x")}},
		{"id with CRLF", Message{ID: "1\r\n2", Data: []byte("x")}},
		{"event with LF", Message{Event: "ev\nent", Data: []byte("x")}},
		{"event with CR", Message{Event: "ev\rent", Data: []byte("x")}},
		{"event with CRLF", Message{Event: "ev\r\nent", Data: []byte("x")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := NewWriter(&strings.Builder{})
			if err := w.Message(tc.msg); err == nil {
				t.Errorf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

// TestSpec_WriterCommentSyntax verifies §9.2.5 comment rule. An empty comment
// is a bare colon line; a non-empty one is space-separated; a multi-line comment
// becomes one comment line per line.
func TestSpec_WriterCommentSyntax(t *testing.T) {
	cases := []struct{ name, comment, want string }{
		{"empty", "", ":\n"},
		{"non-empty", "ping", ": ping\n"},
		{"multi-line split", "a\nb", ": a\n: b\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderComment(t, tc.comment); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSpec_WriterRejectsNULLInID verifies that the writer rejects an id
// containing U+0000 NULL. §9.2.6 requires a receiver to ignore such ids, so
// emitting one would be silently lossy; the writer errors instead.
func TestSpec_WriterRejectsNULLInID(t *testing.T) {
	w := NewWriter(&strings.Builder{})
	if err := w.Message(Message{ID: "bad\x00id", Data: []byte("hello")}); err == nil {
		t.Error("expected error for NULL-containing id, got nil")
	}
}

// TestSpec_WriterDataMultilineRoundTrip verifies that writer splitting and
// reader joining round-trip every combination of SSE line endings inside the
// payload (LF-normalised, since the receiver rejoins with LF).
func TestSpec_WriterDataMultilineRoundTrip(t *testing.T) {
	cases := []struct{ name, data string }{
		{"LF only", "a\nb\nc"},
		{"CRLF only", "a\r\nb\r\nc"},
		{"CR only", "a\rb\rc"},
		{"mixed", "a\nb\r\nc\rd"},
		{"trailing LF", "a\nb\n"},
		{"trailing CRLF", "a\r\nb\r\n"},
		{"trailing CR", "a\rb\r"},
		{"empty interior line", "a\n\nb"},
	}

	normalise := func(s string) string {
		s = strings.ReplaceAll(s, "\r\n", "\n")
		s = strings.ReplaceAll(s, "\r", "\n")
		return s
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wire := writeMessage(t, Message{Data: []byte(tc.data)})
			msgs, err := collectMessages(wire)
			if err != nil {
				t.Fatal(err)
			}
			if len(msgs) != 1 {
				t.Fatalf("got %d messages, want 1", len(msgs))
			}
			got, want := string(msgs[0].Data), normalise(tc.data)
			if got != want {
				t.Errorf("data round-trip: got %q, want %q (normalised from %q)", got, want, tc.data)
			}
		})
	}
}

// TestSpec_WriterFrameTerminatedByBlankLine verifies §9.2.5: every event frame
// ends with a blank line that triggers dispatch on the receiver.
func TestSpec_WriterFrameTerminatedByBlankLine(t *testing.T) {
	got := writeMessage(t, Message{Data: []byte("x")})
	if !strings.HasSuffix(got, "\n\n") {
		t.Errorf("frame must end with blank line: %q", got)
	}
}

// ---------------------------------------------------------------------------
// §9.2.6 — end-of-stream behaviour
// ---------------------------------------------------------------------------

// TestSpec_TrailingDataNoBlankLineDiscarded verifies §9.2.6: an event ending
// without the final blank line is not dispatched.
func TestSpec_TrailingDataNoBlankLineDiscarded(t *testing.T) {
	cases := []struct{ name, in string }{
		{"data only", "data: hello"},
		{"data with LF but no blank line", "data: hello\n"},
		{"id+event+data without blank line", "id: 1\nevent: x\ndata: y"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs, err := collectMessages(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if len(msgs) != 0 {
				t.Errorf("got %d messages, want 0 (incomplete event must be discarded)", len(msgs))
			}
		})
	}
}

// TestSpec_LastEventIDPersistsAfterIncompleteEvent verifies that data dropped
// for lacking a trailing blank line leaves an earlier last-event-ID untouched.
func TestSpec_LastEventIDPersistsAfterIncompleteEvent(t *testing.T) {
	in := "id: 1\ndata: first\n\nid: 2\ndata: orphan"

	r := NewReader(strings.NewReader(in))
	var msgs []Message
	for msg, err := range r.Messages() {
		if err != nil {
			t.Fatal(err)
		}
		msgs = append(msgs, msg)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].ID != "1" {
		t.Errorf("dispatched ID = %q, want %q", msgs[0].ID, "1")
	}
}
