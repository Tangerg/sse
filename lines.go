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

	i := bytes.IndexAny(data, "\r\n")
	if i < 0 {
		// No terminator yet. Flush whatever remains at EOF; otherwise wait.
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil
	}

	// A trailing CR is ambiguous until another byte arrives: it could be a lone
	// terminator or the first half of CRLF.
	if data[i] == '\r' && i+1 == len(data) && !atEOF {
		return 0, nil, nil
	}

	advance = i + 1
	if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
		advance++
	}
	return advance, data[:i], nil
}
