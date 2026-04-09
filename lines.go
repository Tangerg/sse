package sse

import "bytes"

// trimTrailingCR removes a single trailing U+000D CR from data, if present.
//
// The scanner hands us tokens up to (but not including) the terminator byte.
// For CRLF lines the LF is the terminator, leaving a dangling CR at the end
// of the token. This helper strips it so callers always receive clean content.
func trimTrailingCR(data []byte) []byte {
	if len(data) > 0 && data[len(data)-1] == cr[0] {
		return data[0 : len(data)-1]
	}
	return data
}

// splitLine is a [bufio.SplitFunc] that splits on every SSE line ending.
//
// The spec (§9.2.5) defines three valid end-of-line sequences:
//
//	end-of-line = ( cr lf / cr / lf )
//	cr          = %x000D ; U+000D CARRIAGE RETURN
//	lf          = %x000A ; U+000A LINE FEED
//
// §9.2.6 further states that a CR not followed by LF and an LF not preceded
// by CR are each independent line endings. The function handles the ambiguity
// by scanning for both bytes and consuming whichever terminator comes first:
//
//  1. If both CR and LF are present and adjacent (CR immediately before LF),
//     consume the CRLF pair and strip the trailing CR from the token.
//  2. If both are present but not adjacent, consume the one that appears first.
//  3. If only one is present, consume it.
//  4. At EOF with no terminator, return the remaining bytes as the final token.
//
// The returned token never includes the terminator itself.
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
