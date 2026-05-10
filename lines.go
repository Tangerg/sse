package sse

import "bytes"

// splitLine is a [bufio.SplitFunc] that splits on every SSE line ending.
//
// Per §9.2.5 the valid end-of-line sequences are:
//
//	end-of-line = ( cr lf / cr / lf )
//	cr          = %x000D ; U+000D CARRIAGE RETURN
//	lf          = %x000A ; U+000A LINE FEED
//
// §9.2.6 further treats a CR not followed by LF and an LF not preceded by CR
// as independent line endings.
//
// Adversarial chunking matters here: when the underlying [io.Reader] hands
// the scanner a buffer that ends with a lone CR, that CR may be the first
// half of an as-yet-unread CRLF pair. The function therefore requests more
// data (returns 0, nil, nil) when the only terminator is a trailing CR and
// atEOF is false. At EOF the same input is treated as a final lone-CR
// terminator. Tokens never include the terminator bytes.
func splitLine(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}

	lfIdx := bytes.IndexByte(data, '\n')
	crIdx := bytes.IndexByte(data, '\r')

	switch {
	case lfIdx < 0 && crIdx < 0:
		// No terminator yet. Flush whatever remains at EOF; otherwise wait.
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil

	case crIdx < 0:
		// LF only.
		return lfIdx + 1, data[:lfIdx], nil

	case lfIdx < 0:
		// CR only. Defer if it is the last byte and more data may follow:
		// the next read could turn this CR into the start of a CRLF pair.
		if crIdx == len(data)-1 && !atEOF {
			return 0, nil, nil
		}
		return crIdx + 1, data[:crIdx], nil

	case crIdx+1 == lfIdx:
		// CRLF pair — consume both, token excludes the CR.
		return lfIdx + 1, data[:crIdx], nil

	case crIdx < lfIdx:
		// Lone CR appears before a later LF on its own line.
		return crIdx + 1, data[:crIdx], nil

	default:
		// lfIdx < crIdx: LF terminates the current line; the CR belongs to
		// a later line.
		return lfIdx + 1, data[:lfIdx], nil
	}
}
