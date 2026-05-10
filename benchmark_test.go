package sse

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// Benchmarks exist to validate (or invalidate) suspected hot paths before any
// performance work. Run with:
//
//	go test -run=^$ -bench=. -benchmem ./...
//
// Each benchmark uses b.SetBytes where applicable so throughput is reported
// in MB/s, making before/after comparisons direct.

// ----- Reader -----

// readerSmallEventsInput holds many short events to amortise NewReader cost
// and surface the per-event hot path.
var readerSmallEventsInput = strings.Repeat("data: hello\n\n", 1000)

func BenchmarkReader_SmallEvents(b *testing.B) {
	ctx := context.Background()
	b.SetBytes(int64(len(readerSmallEventsInput)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sr := NewReader(strings.NewReader(readerSmallEventsInput))
		for _, err := range sr.Messages(ctx) {
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// readerAllFieldsInput exercises every parseLine branch (id, event, retry, data)
// on every event.
var readerAllFieldsInput = strings.Repeat(
	"id: 1\nevent: update\nretry: 3000\ndata: hello world\n\n", 500)

func BenchmarkReader_AllFields(b *testing.B) {
	ctx := context.Background()
	b.SetBytes(int64(len(readerAllFieldsInput)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sr := NewReader(strings.NewReader(readerAllFieldsInput))
		for _, err := range sr.Messages(ctx) {
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// readerMultilineDataInput stresses the data buffer accumulation path.
var readerMultilineDataInput = strings.Repeat(
	"data: line1\ndata: line2\ndata: line3\ndata: line4\ndata: line5\n\n", 500)

func BenchmarkReader_MultilineData(b *testing.B) {
	ctx := context.Background()
	b.SetBytes(int64(len(readerMultilineDataInput)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sr := NewReader(strings.NewReader(readerMultilineDataInput))
		for _, err := range sr.Messages(ctx) {
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// readerLargeDataInput is one event whose data field exceeds the default
// 64 KiB scanner buffer, exercising NewReaderSize.
var readerLargeDataInput = "data: " + strings.Repeat("x", 256*1024) + "\n\n"

func BenchmarkReader_LargeData(b *testing.B) {
	ctx := context.Background()
	b.SetBytes(int64(len(readerLargeDataInput)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sr := NewReaderSize(strings.NewReader(readerLargeDataInput), 512*1024)
		for _, err := range sr.Messages(ctx) {
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// readerWithBOMInput measures the cost of the stripBOM step relative to the
// non-BOM hot path (compare against BenchmarkReader_SmallEvents).
var readerWithBOMInput = "\uFEFF" + readerSmallEventsInput

func BenchmarkReader_WithBOM(b *testing.B) {
	ctx := context.Background()
	b.SetBytes(int64(len(readerWithBOMInput)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sr := NewReader(strings.NewReader(readerWithBOMInput))
		for _, err := range sr.Messages(ctx) {
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// ----- Writer -----

func BenchmarkWriter_SmallMessage(b *testing.B) {
	ctx := context.Background()
	msg := Message{Event: "update", Data: []byte("hello world")}
	var buf bytes.Buffer
	w := NewWriter(&buf)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		if err := w.Message(ctx, msg); err != nil {
			b.Fatal(err)
		}
	}
	b.SetBytes(int64(buf.Len()))
}

func BenchmarkWriter_AllFields(b *testing.B) {
	ctx := context.Background()
	msg := Message{
		ID:    "1",
		Event: "update",
		Data:  []byte("hello world"),
		Retry: 3 * time.Second,
	}
	var buf bytes.Buffer
	w := NewWriter(&buf)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		if err := w.Message(ctx, msg); err != nil {
			b.Fatal(err)
		}
	}
	b.SetBytes(int64(buf.Len()))
}

// BenchmarkWriter_MultilineData stresses writeData's per-call bufio.Scanner
// allocation — one of my speculative hot-path claims.
func BenchmarkWriter_MultilineData(b *testing.B) {
	ctx := context.Background()
	msg := Message{Data: []byte("line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8")}
	var buf bytes.Buffer
	w := NewWriter(&buf)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		if err := w.Message(ctx, msg); err != nil {
			b.Fatal(err)
		}
	}
	b.SetBytes(int64(buf.Len()))
}

// BenchmarkWriter_LargeData measures cost dominated by data copy, not framing.
var writerLargeData = bytes.Repeat([]byte("x"), 64*1024)

func BenchmarkWriter_LargeData(b *testing.B) {
	ctx := context.Background()
	msg := Message{Data: writerLargeData}
	var buf bytes.Buffer
	w := NewWriter(&buf)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		if err := w.Message(ctx, msg); err != nil {
			b.Fatal(err)
		}
	}
	b.SetBytes(int64(buf.Len()))
}

// BenchmarkWriter_NewlinesInValue exercises newlineStripper's slow path.
// Compare against BenchmarkWriter_SmallMessage (no newlines in field values)
// to size the cost of the strings.NewReplacer call.
func BenchmarkWriter_NewlinesInValue(b *testing.B) {
	ctx := context.Background()
	msg := Message{Event: "up\ndate\rwith\r\nnewlines", Data: []byte("hello world")}
	var buf bytes.Buffer
	w := NewWriter(&buf)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		if err := w.Message(ctx, msg); err != nil {
			b.Fatal(err)
		}
	}
	b.SetBytes(int64(buf.Len()))
}

func BenchmarkWriter_Comment(b *testing.B) {
	ctx := context.Background()
	var buf bytes.Buffer
	w := NewWriter(&buf)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		if err := w.Comment(ctx, "heartbeat"); err != nil {
			b.Fatal(err)
		}
	}
	b.SetBytes(int64(buf.Len()))
}

// ----- splitLine -----

func BenchmarkSplitLine(b *testing.B) {
	cases := []struct {
		name string
		data []byte
	}{
		{"LF", []byte("hello world\nrest of buffer")},
		{"CRLF", []byte("hello world\r\nrest of buffer")},
		{"CR", []byte("hello world\rrest of buffer")},
		{"no terminator atEOF", []byte("hello world")},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			data := tc.data
			atEOF := tc.name == "no terminator atEOF"
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _, _ = splitLine(data, atEOF)
			}
		})
	}
}
