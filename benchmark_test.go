package sse

import (
	"bytes"
	"strings"
	"testing"
)

// Benchmarks validate suspected hot paths. Run with:
//
//	go test -run=^$ -bench=. -benchmem ./...
//
// Each uses b.SetBytes where applicable so throughput is reported in MB/s.

// ----- Reader -----

var readerSmallEventsInput = strings.Repeat("data: hello\n\n", 1000)

func BenchmarkReader_SmallEvents(b *testing.B) {
	b.SetBytes(int64(len(readerSmallEventsInput)))
	b.ReportAllocs()
	for range b.N {
		sr := NewReader(strings.NewReader(readerSmallEventsInput))
		for _, err := range sr.Messages() {
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

var readerAllFieldsInput = strings.Repeat(
	"id: 1\nevent: update\nretry: 3000\ndata: hello world\n\n", 500)

func BenchmarkReader_AllFields(b *testing.B) {
	b.SetBytes(int64(len(readerAllFieldsInput)))
	b.ReportAllocs()
	for range b.N {
		sr := NewReader(strings.NewReader(readerAllFieldsInput))
		for _, err := range sr.Messages() {
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

var readerMultilineDataInput = strings.Repeat(
	"data: line1\ndata: line2\ndata: line3\ndata: line4\ndata: line5\n\n", 500)

func BenchmarkReader_MultilineData(b *testing.B) {
	b.SetBytes(int64(len(readerMultilineDataInput)))
	b.ReportAllocs()
	for range b.N {
		sr := NewReader(strings.NewReader(readerMultilineDataInput))
		for _, err := range sr.Messages() {
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// readerLargeDataInput is one event whose data field exceeds the default 64 KiB
// scanner buffer, exercising a raised MaxLineBytes.
var readerLargeDataInput = "data: " + strings.Repeat("x", 256*1024) + "\n\n"

func BenchmarkReader_LargeData(b *testing.B) {
	b.SetBytes(int64(len(readerLargeDataInput)))
	b.ReportAllocs()
	for range b.N {
		sr := NewReader(strings.NewReader(readerLargeDataInput))
		sr.MaxLineBytes = 512 * 1024
		for _, err := range sr.Messages() {
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

var readerWithBOMInput = "\uFEFF" + readerSmallEventsInput

func BenchmarkReader_WithBOM(b *testing.B) {
	b.SetBytes(int64(len(readerWithBOMInput)))
	b.ReportAllocs()
	for range b.N {
		sr := NewReader(strings.NewReader(readerWithBOMInput))
		for _, err := range sr.Messages() {
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

// ----- Writer -----

func BenchmarkWriter_SmallMessage(b *testing.B) {
	msg := Message{Event: "update", Data: []byte("hello world")}
	var buf bytes.Buffer
	w := NewWriter(&buf)
	b.ReportAllocs()
	for range b.N {
		buf.Reset()
		if err := w.Write(msg); err != nil {
			b.Fatal(err)
		}
	}
	b.SetBytes(int64(buf.Len()))
}

func BenchmarkWriter_AllFields(b *testing.B) {
	msg := Message{
		ID:    "1",
		Event: "update",
		Data:  []byte("hello world"),
	}
	var buf bytes.Buffer
	w := NewWriter(&buf)
	b.ReportAllocs()
	for range b.N {
		buf.Reset()
		if err := w.Write(msg); err != nil {
			b.Fatal(err)
		}
	}
	b.SetBytes(int64(buf.Len()))
}

func BenchmarkWriter_MultilineData(b *testing.B) {
	msg := Message{Data: []byte("line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8")}
	var buf bytes.Buffer
	w := NewWriter(&buf)
	b.ReportAllocs()
	for range b.N {
		buf.Reset()
		if err := w.Write(msg); err != nil {
			b.Fatal(err)
		}
	}
	b.SetBytes(int64(buf.Len()))
}

var writerLargeData = bytes.Repeat([]byte("x"), 64*1024)

func BenchmarkWriter_LargeData(b *testing.B) {
	msg := Message{Data: writerLargeData}
	var buf bytes.Buffer
	w := NewWriter(&buf)
	b.ReportAllocs()
	for range b.N {
		buf.Reset()
		if err := w.Write(msg); err != nil {
			b.Fatal(err)
		}
	}
	b.SetBytes(int64(buf.Len()))
}

func BenchmarkWriter_Comment(b *testing.B) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	b.ReportAllocs()
	for range b.N {
		buf.Reset()
		if err := w.Comment("heartbeat"); err != nil {
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
			for range b.N {
				_, _, _ = splitLine(data, atEOF)
			}
		})
	}
}
