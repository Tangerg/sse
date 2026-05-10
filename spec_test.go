package sse

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Tests in this file verify conformance with the WHATWG HTML Living Standard
// §9.2 (Server-Sent Events). Each test cites the spec clause it exercises so
// reviewers can audit behaviour against the source of truth in spec.md.
//
// Tests already covered by reader_test.go / writer_test.go / lines_test.go
// (BOM stripping at start, basic line endings, ID/event/retry/comment basics,
// leading-space-stripped-once, EOF-mid-event discarded, etc.) are not
// duplicated here.

// ---------------------------------------------------------------------------
// §9.2.6 — field-name comparison, unknown fields, value semantics
// ---------------------------------------------------------------------------

// TestSpec_FieldNameCaseSensitive verifies §9.2.6: "Field names must be
// compared literally, with no case folding performed." Uppercase or mixed-case
// variants of recognised names must be treated as unknown fields and ignored.
func TestSpec_FieldNameCaseSensitive(t *testing.T) {
	// All four known field names mangled in case; every line is therefore an
	// unknown field whose value is discarded. With no "data" field the event
	// is discarded entirely (§9.2.6 dispatch step 2).
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
// ignored." Random unknown field names must not affect the parser state.
func TestSpec_UnknownFieldIgnored(t *testing.T) {
	in := "foo: bar\nbaz: qux\ndata: hello\n\n"
	msgs, err := collectMessages(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || string(msgs[0].Data) != "hello" {
		t.Errorf("got %v, want one message with data=hello", msgs)
	}
}

// TestSpec_NoLeadingSpaceNotStripped verifies §9.2.6: only one leading SPACE
// is removed from a value, and only if present. "data:test" and "data: test"
// must produce identical results; "data:  test" must keep one leading space.
func TestSpec_NoLeadingSpaceNotStripped(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
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

// TestSpec_ValueContainsColons verifies §9.2.6: "Collect the characters on
// the line after the first U+003A COLON character." The split is on the FIRST
// colon only — subsequent colons belong to the value verbatim.
func TestSpec_ValueContainsColons(t *testing.T) {
	in := "data: a:b:c\n\n"
	msgs, err := collectMessages(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(msgs[0].Data); got != "a:b:c" {
		t.Errorf("data = %q, want %q", got, "a:b:c")
	}
}

// TestSpec_BareDataLineDispatchesEmptyData verifies §9.2.6: a line with no
// colon uses "the whole line as the field name, and the empty string as the
// field value". A bare "data" line therefore appends "" + LF to the data
// buffer, which is non-empty and dispatches an event with empty Data.
func TestSpec_BareDataLineDispatchesEmptyData(t *testing.T) {
	in := "data\n\n"
	msgs, err := collectMessages(in)
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

// TestSpec_TwoBareDataLinesProduceNewline verifies §9.2.6: "Append the field
// value to the data buffer, then append a single U+000A LINE FEED (LF)
// character." Two bare "data" lines append "" + LF + "" + LF; dispatch
// strips the trailing LF, leaving a single LF as the data.
func TestSpec_TwoBareDataLinesProduceNewline(t *testing.T) {
	in := "data\ndata\n\n"
	msgs, err := collectMessages(in)
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

// TestSpec_EmptyIDResetsLastEventID verifies §9.2.6: an "id" field whose
// value is the empty string is still recorded (it does not contain NULL),
// resetting the last-event-ID buffer. Subsequent events therefore carry an
// empty ID until the server sets a new one.
func TestSpec_EmptyIDResetsLastEventID(t *testing.T) {
	// Three events: first sets id to 1; second uses bare "id" line (empty
	// value, no colon) which resets the buffer; third inherits the empty ID.
	in := "id: 1\ndata: first\n\nid\ndata: second\n\ndata: third\n\n"
	msgs, err := collectMessages(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3", len(msgs))
	}
	wantIDs := []string{"1", "", ""}
	for i, want := range wantIDs {
		if msgs[i].ID != want {
			t.Errorf("msgs[%d].ID = %q, want %q", i, msgs[i].ID, want)
		}
	}
}

// TestSpec_IDWithEmptyValueAfterColon verifies that "id:" (colon, no value)
// is treated identically to bare "id" — both produce an empty id value that
// resets the last-event-ID buffer.
func TestSpec_IDWithEmptyValueAfterColon(t *testing.T) {
	in := "id: 1\ndata: first\n\nid:\ndata: second\n\n"
	msgs, err := collectMessages(in)
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
// consists of only ASCII digits…" — anything else (signs, whitespace, empty,
// fractional) must cause the field to be ignored. The previous (already
// dispatched) retry value, if any, is preserved as 0 because retry is reset
// at each dispatch.
func TestSpec_RetryRequiresOnlyDigits(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"empty value", "retry: \n"},
		{"empty value no space", "retry:\n"},
		{"negative", "retry: -100\n"},
		{"plus sign", "retry: +100\n"},
		{"trailing letter", "retry: 100ms\n"},
		{"leading letter", "retry: a100\n"},
		{"fractional", "retry: 1.5\n"},
		{"hex", "retry: 0xff\n"},
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

// TestSpec_RetryAcceptsLeadingZeros verifies that a value matching the
// "ASCII digits" definition is interpreted "as an integer in base ten",
// which permits leading zeros.
func TestSpec_RetryAcceptsLeadingZeros(t *testing.T) {
	in := "retry: 003000\ndata: x\n\n"
	msgs, err := collectMessages(in)
	if err != nil {
		t.Fatal(err)
	}
	if msgs[0].Retry != 3*time.Second {
		t.Errorf("Retry = %v, want 3s", msgs[0].Retry)
	}
}

// TestSpec_RetryClearedBetweenEvents verifies that retry, like the data and
// event-type buffers, is cleared after each dispatch — only the
// last-event-ID buffer persists across events (§9.2.6 dispatch steps).
func TestSpec_RetryClearedBetweenEvents(t *testing.T) {
	in := "retry: 5000\ndata: first\n\ndata: second\n\n"
	msgs, err := collectMessages(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[0].Retry != 5*time.Second {
		t.Errorf("msgs[0].Retry = %v, want 5s", msgs[0].Retry)
	}
	if msgs[1].Retry != 0 {
		t.Errorf("msgs[1].Retry = %v, want 0 (retry must reset between events)", msgs[1].Retry)
	}
}

// ---------------------------------------------------------------------------
// §9.2.6 — event-type buffer semantics
// ---------------------------------------------------------------------------

// TestSpec_EventTypeClearedBetweenEvents verifies §9.2.6 dispatch step 7:
// "Set the data buffer and the event type buffer to the empty string." A
// custom event type from one dispatch must not leak into the next.
func TestSpec_EventTypeClearedBetweenEvents(t *testing.T) {
	in := "event: update\ndata: first\n\ndata: second\n\n"
	msgs, err := collectMessages(in)
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
		t.Errorf("msgs[1].Event = %q, want %q (event type must reset between events)",
			msgs[1].Event, defaultEvent)
	}
}

// TestSpec_EventTypeOverwrittenWithinEvent verifies that subsequent "event"
// fields within the same dispatch overwrite earlier ones — the spec sets the
// buffer to the field value each time; the last value wins on dispatch.
func TestSpec_EventTypeOverwrittenWithinEvent(t *testing.T) {
	in := "event: first\nevent: second\ndata: x\n\n"
	msgs, err := collectMessages(in)
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

// TestSpec_MixedLineEndings verifies §9.2.5: a stream may freely mix CRLF,
// lone LF, and lone CR within a single payload.
func TestSpec_MixedLineEndings(t *testing.T) {
	// Event 1 uses LF, event 2 uses CRLF, event 3 uses CR.
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

// TestSpec_BOMOnlyStrippedAtStart verifies §9.2.6: "The UTF-8 decode
// algorithm strips one leading UTF-8 Byte Order Mark (BOM), if any." Only
// ONE leading BOM is stripped — a BOM elsewhere in the stream is part of
// the field value as ordinary UTF-8.
func TestSpec_BOMOnlyStrippedAtStart(t *testing.T) {
	// Leading BOM stripped; the BOM inside the data field value is preserved.
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

// TestSpec_StreamOfOnlyComments verifies that a stream consisting only of
// comments produces no dispatched events (§9.2.6 dispatch step 2: empty
// data buffer is discarded).
func TestSpec_StreamOfOnlyComments(t *testing.T) {
	in := ": one\n: two\n: three\n\n"
	msgs, err := collectMessages(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("got %d messages, want 0 (comments must not dispatch events)", len(msgs))
	}
}

// TestSpec_EmptyStream verifies that an empty input produces neither errors
// nor messages.
func TestSpec_EmptyStream(t *testing.T) {
	msgs, err := collectMessages("")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("got %d messages, want 0", len(msgs))
	}
}

// TestSpec_StreamOfOnlyBOM verifies that a stream containing only the BOM
// produces no events — after stripping the BOM the remaining stream is empty.
func TestSpec_StreamOfOnlyBOM(t *testing.T) {
	msgs, err := collectMessages("\uFEFF")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("got %d messages, want 0", len(msgs))
	}
}

// TestSpec_UTF8MultiByteData verifies §9.2.5: "Event streams in this format
// must always be encoded as UTF-8." Multi-byte characters in the data field
// are preserved verbatim.
func TestSpec_UTF8MultiByteData(t *testing.T) {
	const payload = "你好，世界 — αβγ — 🚀"
	in := "data: " + payload + "\n\n"
	msgs, err := collectMessages(in)
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

// TestSpec_Example_StockTicker reproduces the canonical example from §9.2.6:
//
//	data: YHOO
//	data: +2
//	data: 10
//
// "The event's data attribute would contain the string 'YHOO\n+2\n10'
// (where '\n' represents a newline)."
func TestSpec_Example_StockTicker(t *testing.T) {
	in := "data: YHOO\ndata: +2\ndata: 10\n\n"
	msgs, err := collectMessages(in)
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
// that exercises comments, id propagation, id reset by empty value, and the
// single-leading-space rule.
//
//	: test stream
//
//	data: first event
//	id: 1
//
//	data:second event
//	id
//
//	data:  third event
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

// TestSpec_Example_DataOnlyVariations reproduces the three-block example
// from §9.2.6 demonstrating empty-data semantics:
//
//	data
//
//	data
//	data
//
//	data:
//
// "The first block fires events with the data set to the empty string …
// The middle block fires an event with the data set to a single newline
// character. The last block is discarded because it is not followed by a
// blank line."
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

// TestSpec_Example_ColonSpaceEquivalence reproduces the final example from
// §9.2.6: "data:test" and "data: test" must fire identical events.
func TestSpec_Example_ColonSpaceEquivalence(t *testing.T) {
	in := "data:test\n\ndata: test\n\n"
	msgs, err := collectMessages(in)
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

// TestSpec_WriterStripsCRLFFromIDAndEvent verifies §9.2.5 ABNF: field values
// use any-char which excludes LF/CR. The writer must not let raw CR or LF
// leak into the wire frame.
func TestSpec_WriterStripsCRLFFromIDAndEvent(t *testing.T) {
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
			got := writeMessage(t, tc.msg)
			// No literal CR/LF outside the framing newlines: the only LFs
			// allowed are line terminators, and there must be exactly one
			// per field plus the trailing blank line. CRs must not appear
			// at all (the writer never emits CRLF).
			if strings.Contains(got, "\r") {
				t.Errorf("frame contains CR: %q", got)
			}
			// Each field is terminated by an LF; the frame ends with a blank
			// line. For these test inputs (id + data) we expect exactly
			// 3 LFs: one after id, one after data, one for the blank line.
			if n := strings.Count(got, "\n"); n != 3 {
				t.Errorf("LF count = %d, want 3 — embedded newlines leaked: %q", n, got)
			}
		})
	}
}

// TestSpec_WriterCommentSyntax verifies §9.2.5 comment rule:
//
//	comment = colon *any-char end-of-line
//
// An empty comment writes a bare colon line; a non-empty comment is
// space-separated for readability while remaining a valid comment.
func TestSpec_WriterCommentSyntax(t *testing.T) {
	cases := []struct {
		name    string
		comment string
		want    string
	}{
		{"empty", "", ":\n\n"},
		{"non-empty", "ping", ": ping\n\n"},
		{"newline stripped", "a\nb", ": ab\n\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderComment(t, tc.comment); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSpec_WriterIDWithNULLLossyRoundTrip documents the spec-mandated lossy
// behaviour for IDs containing U+0000 NULL: §9.2.5 permits NULL in field
// values (any-char includes %x0000), but §9.2.6 requires the parser to
// IGNORE id values containing NULL. The writer therefore emits a frame that
// the parser legally drops the id from — round-trip yields an empty ID.
func TestSpec_WriterIDWithNULLLossyRoundTrip(t *testing.T) {
	msg := Message{ID: "bad\x00id", Data: []byte("hello")}
	wire := writeMessage(t, msg)

	msgs, err := collectMessages(wire)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].ID != "" {
		t.Errorf("ID = %q, want empty (parser must drop NULL-containing ids)", msgs[0].ID)
	}
	if string(msgs[0].Data) != "hello" {
		t.Errorf("Data = %q, want %q", msgs[0].Data, "hello")
	}
}

// TestSpec_WriterDataMultilineRoundTrip verifies that the writer's data
// splitting and the reader's data joining round-trip every combination of
// SSE line endings inside the payload.
func TestSpec_WriterDataMultilineRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"LF only", "a\nb\nc"},
		{"CRLF only", "a\r\nb\r\nc"},
		{"CR only", "a\rb\rc"},
		{"mixed", "a\nb\r\nc\rd"},
		{"trailing LF", "a\nb\n"},
		{"trailing CRLF", "a\r\nb\r\n"},
		{"trailing CR", "a\rb\r"},
		{"empty interior line", "a\n\nb"},
	}

	// Per spec, multi-line data is reassembled with LF separators on the
	// receiving side. The writer therefore must split on every line terminator
	// (CR/LF/CRLF) and the reader must rejoin with LF — comparison uses LF
	// normalisation.
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

// TestSpec_WriterFrameTerminatedByBlankLine verifies §9.2.5: every event is
// terminated by a blank line that triggers dispatch on the receiver. The
// writer must emit "\n\n" at the end of every frame (Message and Comment).
func TestSpec_WriterFrameTerminatedByBlankLine(t *testing.T) {
	t.Run("Message", func(t *testing.T) {
		got := writeMessage(t, Message{Data: []byte("x")})
		if !strings.HasSuffix(got, "\n\n") {
			t.Errorf("frame must end with blank line: %q", got)
		}
	})
	t.Run("Comment", func(t *testing.T) {
		got := renderComment(t, "x")
		if !strings.HasSuffix(got, "\n\n") {
			t.Errorf("comment frame must end with blank line: %q", got)
		}
	})
	t.Run("EmptyComment", func(t *testing.T) {
		got := renderComment(t, "")
		if got != ":\n\n" {
			t.Errorf("empty-comment frame = %q, want %q", got, ":\n\n")
		}
	})
}

// ---------------------------------------------------------------------------
// §9.2.6 — end-of-stream behaviour
// ---------------------------------------------------------------------------

// TestSpec_TrailingDataNoBlankLineDiscarded verifies §9.2.6: "If the file
// ends in the middle of an event, before the final empty line, the
// incomplete event is not dispatched."
func TestSpec_TrailingDataNoBlankLineDiscarded(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
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

// TestSpec_LastEventIDPersistsAfterIncompleteEvent verifies that data
// dropped because it lacked a trailing blank line still leaves the
// last-event-ID buffer untouched if it was set earlier (as the spec only
// resets it on an explicit empty-id field).
func TestSpec_LastEventIDPersistsAfterIncompleteEvent(t *testing.T) {
	// First event sets id=1 and dispatches; second event sets id=2 and data
	// but lacks a blank line, so it is discarded. We only see one message.
	in := "id: 1\ndata: first\n\nid: 2\ndata: orphan"

	r := NewReader(strings.NewReader(in))
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
	if msgs[0].ID != "1" {
		t.Errorf("dispatched ID = %q, want %q", msgs[0].ID, "1")
	}
}
