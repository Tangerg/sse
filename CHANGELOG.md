# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims
to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.0.6] - 2026-08-01

### Changed

- Refactored `Reader` around an unexported single-event parsing primitive while
  keeping `Messages` as the only public consumption API and preserving its
  behavior.
- Simplified line splitting to select the first CR/LF terminator directly while
  preserving adversarial CRLF chunk-boundary handling.
- Replaced the two-pass retry validation/conversion with one saturating decimal
  parser that still validates every byte after overflow.

### Tests

- Pinned the minimum-version CI lane to Go 1.23.0, the exact version promised by
  `go.mod`, rather than floating to the latest Go 1.23 patch release.

## [0.0.5] - 2026-07-21

### Documentation

- Added Go Reference and CI status badges to the README.

## [0.0.4] - 2026-07-21

### Added

- Exported `ErrLineTooLong`; a line exceeding `Reader.MaxLineBytes` now reports it
  (including the `bufio.ErrTooLong` case), matchable with `errors.Is` and
  symmetric with `ErrEventTooLarge`.
- Runnable, output-verified `Example` functions (reading, writing, comments,
  retry/reset control frames, reconnection state, and the HTTP reader/writer) so
  pkg.go.dev renders documentation that `go test` checks.

### Changed

- Replaced the `int(^uint(0)>>1)` bit-trick with `math.MaxInt` (no behavior
  change).
- Extracted the reader's lazy scanner setup into `initScanner` and flattened the
  dispatch loop's nesting (no behavior change).

## [0.0.3] - 2026-07-20

### Changed (breaking)

- Removed `Message.Retry`; retry is stream-level state exposed exclusively by
  `Reader.Retry` and `Writer.Retry`.
- Renamed `Writer.Message` to `Writer.Write`.
- `NewHTTPWriter` now returns only `*Writer`; nil input is a programmer error and
  panics consistently with `NewWriter`.
- Lowered the minimum Go version from 1.26 to 1.23, the first release with
  range-over-function iterators.

### Fixed

- `Reader.MaxLineBytes` now accepts a line whose content is exactly at the
  configured limit for LF, CR, and CRLF endings.
- `Reader` now applies the Encoding Standard's UTF-8 replacement decoder,
  including stripping exactly one leading BOM; malformed subsequences become
  `U+FFFD` instead of leaking invalid bytes to messages.
- `NewHTTPReader` now rejects non-200 responses as required by EventSource
  response processing.
- `Writer` now rejects invalid UTF-8 before writing, so it cannot emit an event
  stream that violates the protocol's UTF-8 requirement.

### Tests

- Protocol vectors now cover the WHATWG examples and Web Platform Tests field
  parsing/BOM cases, malformed UTF-8, field state transitions, EOF behavior,
  MIME/status validation, size limits, and transport failures.
- The package test suite reaches 100% statement coverage.

## [0.0.2] - 2026-07-10

This release is a breaking redesign of the v0.0.1 API into a small, reliable
single-package SSE codec plus a light HTTP adapter.

### Changed (breaking)

- Reading is now `Reader.Messages() iter.Seq2[Message, error]` — the
  `context.Context` parameter was removed. To interrupt a blocked read, close
  the underlying reader; to stop consuming, break the range loop.
- Writing is now `Writer.Message(Message) error` and `Writer.Comment(string) error`
  without a `context.Context` parameter.
- `Writer.Message` always emits a `data:` line, so an event carrying only a type
  is dispatched by the receiver (previously an empty `Data` produced no event).
- `Reader.MaxLineBytes` (field) replaces the `*Size` constructors.
- `retry` is treated as stream-level state per §9.2.6: it persists across events
  and is reported on every `Message`, rather than being reset on each dispatch.
- Minimum Go version raised to 1.26.

### Added

- `Reader.LastEventID()` and `Reader.Retry()` expose reconnection state — set
  even by standalone `id:`/`retry:` frames that dispatch no event.
- `Reader.MaxEventBytes` and `ErrEventTooLarge` bound the total data buffered for
  a single event (checked before buffering; the error is terminal).
- `Writer.Retry(time.Duration)` and `Writer.ResetID()` emit standalone control
  frames.
- `LICENSE` (MIT), CI workflow, and fuzz tests (reader, round-trip, chunking).

### Fixed

- `Content-Type` is parsed with `mime.ParseMediaType` (case-insensitive, params
  handled) instead of a raw prefix match.
- HTTP flushing uses `http.ResponseController` (unwraps middleware, surfaces
  flush errors); the hop-by-hop `Connection` header is no longer set.
- `Writer` rejects an `ID` containing CR/LF/NUL, an `Event` containing CR/LF, and
  a negative `Retry`, rather than silently altering or omitting them.
- `retry` values are validated as complete ASCII-digit strings before parsing,
  and an overflowing value is clamped instead of wrapping negative.
- Short writes are reported as `io.ErrShortWrite`.

## [0.0.1]

Initial release.

[0.0.2]: https://github.com/Tangerg/sse/releases/tag/v0.0.2
[0.0.1]: https://github.com/Tangerg/sse/releases/tag/v0.0.1
[Unreleased]: https://github.com/Tangerg/sse/compare/v0.0.6...HEAD
[0.0.6]: https://github.com/Tangerg/sse/compare/v0.0.5...v0.0.6
[0.0.5]: https://github.com/Tangerg/sse/compare/v0.0.4...v0.0.5
[0.0.4]: https://github.com/Tangerg/sse/compare/v0.0.3...v0.0.4
[0.0.3]: https://github.com/Tangerg/sse/compare/v0.0.2...v0.0.3
