package sse

import "bytes"

// trimTrailingCR removes a single trailing CR (U+000D) from data, if present.
// This is used to strip the CR from CRLF and standalone-CR line endings after
// the line terminator itself has been sliced off.
func trimTrailingCR(data []byte) []byte {
	if len(data) > 0 && data[len(data)-1] == cr[0] {
		return data[0 : len(data)-1]
	}
	return data
}

// splitLine is a bufio.SplitFunc that recognises all three SSE line endings:
// CRLF ("\r\n"), lone LF ("\n"), and lone CR ("\r").
// The returned token never includes the line terminator itself.
func splitLine(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}

	lfIdx := bytes.IndexByte(data, lf[0])
	crIdx := bytes.IndexByte(data, cr[0])

	if lfIdx >= 0 && crIdx >= 0 {
		// Both CR and LF are present — check for CRLF pair first.
		if lfIdx == crIdx+1 {
			// CRLF: advance past "\r\n", trim the CR from the token.
			return lfIdx + 1, trimTrailingCR(data[0:lfIdx]), nil
		}

		// CR and LF appear independently; consume whichever comes first.
		i := min(lfIdx, crIdx)
		return i + 1, trimTrailingCR(data[0:i]), nil
	}

	// Only one terminator is present; consume it.
	if i := max(lfIdx, crIdx); i >= 0 {
		return i + 1, trimTrailingCR(data[0:i]), nil
	}

	// No terminator found. If at EOF, return the remaining data as-is.
	if atEOF {
		return len(data), data, nil
	}

	// Request more data.
	return 0, nil, nil
}
