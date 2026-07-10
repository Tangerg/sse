# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims
to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
