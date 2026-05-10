package sse

import (
	"testing"
)

func TestSplitLine(t *testing.T) {
	tests := []struct {
		name        string
		data        []byte
		atEOF       bool
		wantAdvance int
		wantToken   string
	}{
		// Terminal conditions.
		{"empty atEOF", []byte{}, true, 0, ""},
		{"empty not atEOF", []byte{}, false, 0, ""},

		// Single-character terminators.
		{"LF only", []byte("hello\nworld"), false, 6, "hello"},
		{"CR only", []byte("hello\rworld"), false, 6, "hello"},
		{"CRLF pair", []byte("hello\r\nworld"), false, 7, "hello"},

		// CR and LF both present but not adjacent.
		{"CR before LF not adjacent", []byte("hel\rlo\nworld"), false, 4, "hel"},
		{"LF before CR not adjacent", []byte("hel\nlo\rworld"), false, 4, "hel"},

		// Empty lines.
		{"empty line LF", []byte("\nhello"), false, 1, ""},
		{"empty line CR", []byte("\rhello"), false, 1, ""},
		{"empty line CRLF", []byte("\r\nhello"), false, 2, ""},

		// EOF with no terminator.
		{"no terminator atEOF", []byte("hello"), true, 5, "hello"},
		{"no terminator not atEOF", []byte("hello"), false, 0, ""},

		// Trailing CR ambiguity: at EOF it's a final terminator; otherwise
		// wait for the next byte to disambiguate against CRLF.
		{"trailing CR atEOF", []byte("hello\r"), true, 6, "hello"},
		{"trailing CR not atEOF", []byte("hello\r"), false, 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			advance, token, err := splitLine(tt.data, tt.atEOF)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if advance != tt.wantAdvance {
				t.Errorf("advance = %d, want %d", advance, tt.wantAdvance)
			}
			if string(token) != tt.wantToken {
				t.Errorf("token = %q, want %q", token, tt.wantToken)
			}
		})
	}
}
