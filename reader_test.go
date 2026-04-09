package sse

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// collectMessages is a helper that reads all messages from a raw SSE string.
func collectMessages(input string) ([]Message, error) {
	r := NewReader(strings.NewReader(input))

	var msgs []Message
	for msg, err := range r.Messages(context.Background()) {
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

	t.Run("custom bufSize applied", func(t *testing.T) {
		r := NewReader(strings.NewReader("data: hello\n\n"), 1024*1024)
		var msgs []Message
		for msg, err := range r.Messages(context.Background()) {
			if err != nil {
				t.Fatal(err)
			}
			msgs = append(msgs, msg)
		}
		if len(msgs) != 1 || string(msgs[0].Data) != "hello" {
			t.Errorf("unexpected messages: %v", msgs)
		}
	})
}

func TestNewHTTPReader(t *testing.T) {
	t.Run("nil response", func(t *testing.T) {
		_, err := NewHTTPReader(nil)
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("missing Content-Type", func(t *testing.T) {
		resp := &http.Response{
			Header: http.Header{},
			Body:   io.NopCloser(strings.NewReader("")),
		}
		_, err := NewHTTPReader(resp)
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("wrong Content-Type", func(t *testing.T) {
		resp := &http.Response{
			Header: http.Header{"Content-Type": []string{"application/json"}},
			Body:   io.NopCloser(strings.NewReader("")),
		}
		_, err := NewHTTPReader(resp)
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("valid text/event-stream", func(t *testing.T) {
		resp := &http.Response{
			Header: http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:   io.NopCloser(strings.NewReader("")),
		}
		_, err := NewHTTPReader(resp)
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("text/event-stream with charset accepted", func(t *testing.T) {
		resp := &http.Response{
			Header: http.Header{"Content-Type": []string{"text/event-stream; charset=utf-8"}},
			Body:   io.NopCloser(strings.NewReader("")),
		}
		_, err := NewHTTPReader(resp)
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestReaderMessages(t *testing.T) {
	t.Run("basic data field", func(t *testing.T) {
		msgs, err := collectMessages("data: hello\n\n")
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 {
			t.Fatalf("got %d messages, want 1", len(msgs))
		}
		if string(msgs[0].Data) != "hello" {
			t.Errorf("data = %q, want %q", msgs[0].Data, "hello")
		}
	})

	t.Run("default event type is message", func(t *testing.T) {
		msgs, err := collectMessages("data: hello\n\n")
		if err != nil {
			t.Fatal(err)
		}
		if msgs[0].Event != defaultEvent {
			t.Errorf("event = %q, want %q", msgs[0].Event, defaultEvent)
		}
	})

	t.Run("named event field", func(t *testing.T) {
		msgs, err := collectMessages("event: update\ndata: hello\n\n")
		if err != nil {
			t.Fatal(err)
		}
		if msgs[0].Event != "update" {
			t.Errorf("event = %q, want %q", msgs[0].Event, "update")
		}
	})

	t.Run("id field", func(t *testing.T) {
		msgs, err := collectMessages("id: 42\ndata: hello\n\n")
		if err != nil {
			t.Fatal(err)
		}
		if msgs[0].ID != "42" {
			t.Errorf("id = %q, want %q", msgs[0].ID, "42")
		}
	})

	t.Run("id persists across events", func(t *testing.T) {
		msgs, err := collectMessages("id: 1\ndata: first\n\ndata: second\n\n")
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 2 {
			t.Fatalf("got %d messages, want 2", len(msgs))
		}
		if msgs[1].ID != "1" {
			t.Errorf("second event id = %q, want %q", msgs[1].ID, "1")
		}
	})

	t.Run("id containing null is ignored", func(t *testing.T) {
		msgs, err := collectMessages("id: bad\x00id\ndata: hello\n\n")
		if err != nil {
			t.Fatal(err)
		}
		if msgs[0].ID != "" {
			t.Errorf("id = %q, want empty (null-containing id must be ignored)", msgs[0].ID)
		}
	})

	t.Run("retry field parsed as milliseconds", func(t *testing.T) {
		msgs, err := collectMessages("retry: 3000\ndata: hello\n\n")
		if err != nil {
			t.Fatal(err)
		}
		if msgs[0].Retry != 3*time.Second {
			t.Errorf("retry = %v, want %v", msgs[0].Retry, 3*time.Second)
		}
	})

	t.Run("retry with non-digit characters is ignored", func(t *testing.T) {
		msgs, err := collectMessages("retry: 3s\ndata: hello\n\n")
		if err != nil {
			t.Fatal(err)
		}
		if msgs[0].Retry != 0 {
			t.Errorf("retry = %v, want 0", msgs[0].Retry)
		}
	})

	t.Run("comment lines are ignored", func(t *testing.T) {
		msgs, err := collectMessages(": this is a comment\ndata: hello\n\n")
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 {
			t.Fatalf("got %d messages, want 1", len(msgs))
		}
		if string(msgs[0].Data) != "hello" {
			t.Errorf("data = %q, want %q", msgs[0].Data, "hello")
		}
	})

	t.Run("empty data buffer suppresses dispatch", func(t *testing.T) {
		msgs, err := collectMessages("event: ping\n\n")
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 0 {
			t.Errorf("got %d messages, want 0 (no data field)", len(msgs))
		}
	})

	t.Run("incomplete event at EOF is discarded", func(t *testing.T) {
		msgs, err := collectMessages("data: hello")
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 0 {
			t.Errorf("got %d messages, want 0 (no trailing blank line)", len(msgs))
		}
	})

	t.Run("multi-line data joined with newlines", func(t *testing.T) {
		msgs, err := collectMessages("data: line1\ndata: line2\ndata: line3\n\n")
		if err != nil {
			t.Fatal(err)
		}
		want := "line1\nline2\nline3"
		if string(msgs[0].Data) != want {
			t.Errorf("data = %q, want %q", msgs[0].Data, want)
		}
	})

	t.Run("field with no colon uses whole line as name", func(t *testing.T) {
		// "data" with no colon → field=data, value=""
		msgs, err := collectMessages("data\n\n")
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 {
			t.Fatalf("got %d messages, want 1", len(msgs))
		}
		if string(msgs[0].Data) != "" {
			t.Errorf("data = %q, want empty string", msgs[0].Data)
		}
	})

	t.Run("leading space after colon stripped once", func(t *testing.T) {
		// "data:test" and "data: test" must produce the same value.
		msgs1, _ := collectMessages("data:test\n\n")
		msgs2, _ := collectMessages("data: test\n\n")
		if string(msgs1[0].Data) != string(msgs2[0].Data) {
			t.Errorf("data without space %q != data with space %q", msgs1[0].Data, msgs2[0].Data)
		}
	})

	t.Run("BOM at start of stream is stripped", func(t *testing.T) {
		msgs, err := collectMessages("\uFEFFdata: hello\n\n")
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 {
			t.Fatalf("got %d messages, want 1", len(msgs))
		}
		if string(msgs[0].Data) != "hello" {
			t.Errorf("data = %q, want %q", msgs[0].Data, "hello")
		}
	})

	t.Run("CRLF line endings accepted", func(t *testing.T) {
		msgs, err := collectMessages("data: hello\r\n\r\n")
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 || string(msgs[0].Data) != "hello" {
			t.Errorf("got %v, want one message with data=hello", msgs)
		}
	})

	t.Run("CR-only line endings accepted", func(t *testing.T) {
		msgs, err := collectMessages("data: hello\r\r")
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 || string(msgs[0].Data) != "hello" {
			t.Errorf("got %v, want one message with data=hello", msgs)
		}
	})

	t.Run("multiple sequential events", func(t *testing.T) {
		msgs, err := collectMessages("data: a\n\ndata: b\n\ndata: c\n\n")
		if err != nil {
			t.Fatal(err)
		}
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
