# SSE Specification Reference

**WHATWG HTML Living Standard — Server-Sent Events**
<https://html.spec.whatwg.org/multipage/server-sent-events.html>

---

#### 9.2.5 Parsing an event stream[](https://html.spec.whatwg.org/multipage/server-sent-events.html#parsing-an-event-stream)

This event stream format's [MIME type](https://mimesniff.spec.whatwg.org/#mime-type) is `[text/event-stream](https://html.spec.whatwg.org/multipage/iana.html#text/event-stream)`.

The event stream format is as described by the `stream` production of the following ABNF, the character set for which is Unicode. [[ABNF]](https://html.spec.whatwg.org/multipage/references.html#refsABNF)
    
    
    stream        = [ bom ] *event
    event         = *( comment / field ) end-of-line
    comment       = colon *any-char end-of-line
    field         = 1*name-char [ colon [ space ] *any-char ] end-of-line
    end-of-line   = ( cr lf / cr / lf )
    
    ; characters
    lf            = %x000A ; U+000A LINE FEED (LF)
    cr            = %x000D ; U+000D CARRIAGE RETURN (CR)
    space         = %x0020 ; U+0020 SPACE
    colon         = %x003A ; U+003A COLON (:)
    bom           = %xFEFF ; U+FEFF BYTE ORDER MARK
    name-char     = %x0000-0009 / %x000B-000C / %x000E-0039 / %x003B-10FFFF
                    ; a [scalar value](https://infra.spec.whatwg.org/#scalar-value) other than U+000A LINE FEED (LF), U+000D CARRIAGE RETURN (CR), or U+003A COLON (:)
    any-char      = %x0000-0009 / %x000B-000C / %x000E-10FFFF
                    ; a [scalar value](https://infra.spec.whatwg.org/#scalar-value) other than U+000A LINE FEED (LF) or U+000D CARRIAGE RETURN (CR)

Event streams in this format must always be encoded as UTF-8. [[ENCODING]](https://html.spec.whatwg.org/multipage/references.html#refsENCODING)

Lines must be separated by either a U+000D CARRIAGE RETURN U+000A LINE FEED (CRLF) character pair, a single U+000A LINE FEED (LF) character, or a single U+000D CARRIAGE RETURN (CR) character.

Since connections established to remote servers for such resources are expected to be long-lived, UAs should ensure that appropriate buffering is used. In particular, while line buffering with lines are defined to end with a single U+000A LINE FEED (LF) character is safe, block buffering or line buffering with different expected line endings can cause delays in event dispatch.

#### 9.2.6 Interpreting an event stream[](https://html.spec.whatwg.org/multipage/server-sent-events.html#event-stream-interpretation)

Streams must be decoded using the [UTF-8 decode](https://encoding.spec.whatwg.org/#utf-8-decode) algorithm.

The [UTF-8 decode](https://encoding.spec.whatwg.org/#utf-8-decode) algorithm strips one leading UTF-8 Byte Order Mark (BOM), if any.

The stream must then be parsed by reading everything line by line, with a U+000D CARRIAGE RETURN U+000A LINE FEED (CRLF) character pair, a single U+000A LINE FEED (LF) character not preceded by a U+000D CARRIAGE RETURN (CR) character, and a single U+000D CARRIAGE RETURN (CR) character not followed by a U+000A LINE FEED (LF) character being the ways in which a line can end.

When a stream is parsed, a data buffer, an event type buffer, and a last event ID buffer must be associated with it. They must be initialized to the empty string.

Lines must be processed, in the order they are received, as follows:

If the line is empty (a blank line)
    

[Dispatch the event](https://html.spec.whatwg.org/multipage/server-sent-events.html#dispatchMessage), as defined below.

If the line starts with a U+003A COLON character (:)
    

Ignore the line.

If the line contains a U+003A COLON character (:)
    

Collect the characters on the line before the first U+003A COLON character (:), and let field be that string.

Collect the characters on the line after the first U+003A COLON character (:), and let value be that string. If value starts with a U+0020 SPACE character, remove it from value.

[Process the field](https://html.spec.whatwg.org/multipage/server-sent-events.html#processField) using the steps described below, using field as the field name and value as the field value.

Otherwise, the string is not empty but does not contain a U+003A COLON character (:)
    

[Process the field](https://html.spec.whatwg.org/multipage/server-sent-events.html#processField) using the steps described below, using the whole line as the field name, and the empty string as the field value.

Once the end of the file is reached, any pending data must be discarded. (If the file ends in the middle of an event, before the final empty line, the incomplete event is not dispatched.)

* * *

The steps to process the field given a field name and a field value depend on the field name, as given in the following list. Field names must be compared literally, with no case folding performed.

If the field name is "event"
    

Set the event type buffer to the field value.

If the field name is "data"
    

Append the field value to the data buffer, then append a single U+000A LINE FEED (LF) character to the data buffer.

If the field name is "id"
    

If the field value does not contain U+0000 NULL, then set the last event ID buffer to the field value. Otherwise, ignore the field.

If the field name is "retry"
    

If the field value consists of only [ASCII digits](https://infra.spec.whatwg.org/#ascii-digit), then interpret the field value as an integer in base ten, and set the event stream's [reconnection time](https://html.spec.whatwg.org/multipage/server-sent-events.html#concept-event-stream-reconnection-time) to that integer. Otherwise, ignore the field.

Otherwise
    

The field is ignored.

When the user agent is required to dispatch the event, the user agent must process the data buffer, the event type buffer, and the last event ID buffer using steps appropriate for the user agent.

For web browsers, the appropriate steps to [dispatch the event](https://html.spec.whatwg.org/multipage/server-sent-events.html#dispatchMessage) are as follows:

  1. Set the [last event ID string](https://html.spec.whatwg.org/multipage/server-sent-events.html#concept-event-stream-last-event-id) of the event source to the value of the last event ID buffer. The buffer does not get reset, so the [last event ID string](https://html.spec.whatwg.org/multipage/server-sent-events.html#concept-event-stream-last-event-id) of the event source remains set to this value until the next time it is set by the server.

  2. If the data buffer is an empty string, set the data buffer and the event type buffer to the empty string and return.

  3. If the data buffer's last character is a U+000A LINE FEED (LF) character, then remove the last character from the data buffer.

  4. Let event be the result of [creating an event](https://dom.spec.whatwg.org/#concept-event-create) using `[MessageEvent](https://html.spec.whatwg.org/multipage/comms.html#messageevent)`, in the [relevant realm](https://html.spec.whatwg.org/multipage/webappapis.html#concept-relevant-realm) of the `[EventSource](https://html.spec.whatwg.org/multipage/server-sent-events.html#eventsource)` object.

  5. Initialize event's `[type](https://dom.spec.whatwg.org/#dom-event-type)` attribute to "`[message](https://html.spec.whatwg.org/multipage/indices.html#event-message)`", its `[data](https://html.spec.whatwg.org/multipage/comms.html#dom-messageevent-data)` attribute to data, its `[origin](https://html.spec.whatwg.org/multipage/comms.html#dom-messageevent-origin)` attribute to the [serialization](https://html.spec.whatwg.org/multipage/browsers.html#ascii-serialisation-of-an-origin) of the [origin](https://url.spec.whatwg.org/#concept-url-origin) of the event stream's final URL (i.e., the URL after redirects), and its `[lastEventId](https://html.spec.whatwg.org/multipage/comms.html#dom-messageevent-lasteventid)` attribute to the [last event ID string](https://html.spec.whatwg.org/multipage/server-sent-events.html#concept-event-stream-last-event-id) of the event source.

  6. If the event type buffer has a value other than the empty string, change the [type](https://dom.spec.whatwg.org/#dom-event-type) of the newly created event to equal the value of the event type buffer.

  7. Set the data buffer and the event type buffer to the empty string.

  8. [Queue a task](https://html.spec.whatwg.org/multipage/webappapis.html#queue-a-task) which, if the `[readyState](https://html.spec.whatwg.org/multipage/server-sent-events.html#dom-eventsource-readystate)` attribute is set to a value other than `[CLOSED](https://html.spec.whatwg.org/multipage/server-sent-events.html#dom-eventsource-closed)`, [dispatches](https://dom.spec.whatwg.org/#concept-event-dispatch) the newly created event at the `[EventSource](https://html.spec.whatwg.org/multipage/server-sent-events.html#eventsource)` object.

If an event doesn't have an "id" field, but an earlier event did set the event source's [last event ID string](https://html.spec.whatwg.org/multipage/server-sent-events.html#concept-event-stream-last-event-id), then the event's `[lastEventId](https://html.spec.whatwg.org/multipage/comms.html#dom-messageevent-lasteventid)` field will be set to the value of whatever the last seen "id" field was.

For other user agents, the appropriate steps to [dispatch the event](https://html.spec.whatwg.org/multipage/server-sent-events.html#dispatchMessage) are implementation dependent, but at a minimum they must set the data and event type buffers to the empty string before returning.

The following event stream, once followed by a blank line:
    
    
    data: YHOO
    data: +2
    data: 10

...would cause an event `[message](https://html.spec.whatwg.org/multipage/indices.html#event-message)` with the interface `[MessageEvent](https://html.spec.whatwg.org/multipage/comms.html#messageevent)` to be dispatched on the `[EventSource](https://html.spec.whatwg.org/multipage/server-sent-events.html#eventsource)` object. The event's `[data](https://html.spec.whatwg.org/multipage/comms.html#dom-messageevent-data)` attribute would contain the string "`YHOO\n+2\n10`" (where "`\n`" represents a newline).

This could be used as follows: 
    
    
    var stocks = new EventSource("https://stocks.example.com/ticker.php");
    stocks.onmessage = function (event) {
      var data = event.data.split('\n');
      updateStocks(data[0], data[1], data[2]);
    };

...where `updateStocks()` is a function defined as:
    
    
    function updateStocks(symbol, delta, value) { ... }

...or some such.

The following stream contains four blocks. The first block has just a comment, and will fire nothing. The second block has two fields with names "data" and "id" respectively; an event will be fired for this block, with the data "first event", and will then set the last event ID to "1" so that if the connection died between this block and the next, the server would be sent a ``[Last-Event-ID](https://html.spec.whatwg.org/multipage/server-sent-events.html#last-event-id)`` header with the value ``1``. The third block fires an event with data "second event", and also has an "id" field, this time with no value, which resets the last event ID to the empty string (meaning no ``[Last-Event-ID](https://html.spec.whatwg.org/multipage/server-sent-events.html#last-event-id)`` header will now be sent in the event of a reconnection being attempted). Finally, the last block just fires an event with the data " third event" (with a single leading space character). Note that the last still has to end with a blank line, the end of the stream is not enough to trigger the dispatch of the last event.
    
    
    : test stream
    
    data: first event
    id: 1
    
    data:second event
    id
    
    data:  third event
    

The following stream fires two events:
    
    
    data
    
    data
    data
    
    data:

The first block fires events with the data set to the empty string, as would the last block if it was followed by a blank line. The middle block fires an event with the data set to a single newline character. The last block is discarded because it is not followed by a blank line.

The following stream fires two identical events:
    
    
    data:test
    
    data: test
    

This is because the space after the colon is ignored if present.

#### 9.2.7 Authoring notes[](https://html.spec.whatwg.org/multipage/server-sent-events.html#authoring-notes)

Legacy proxy servers are known to, in certain cases, drop HTTP connections after a short timeout. To protect against such proxy servers, authors can include a comment line (one starting with a ':' character) every 15 seconds or so.

Authors wishing to relate event source connections to each other or to specific documents previously served might find that relying on IP addresses doesn't work, as individual clients can have multiple IP addresses (due to having multiple proxy servers) and individual IP addresses can have multiple clients (due to sharing a proxy server). It is better to include a unique identifier in the document when it is served and then pass that identifier as part of the URL when the connection is established.

Authors are also cautioned that HTTP chunking can have unexpected negative effects on the reliability of this protocol, in particular if the chunking is done by a different layer unaware of the timing requirements. If this is a problem, chunking can be disabled for serving event streams.

Clients that support HTTP's per-server connection limitation might run into trouble when opening multiple pages from a site if each page has an `[EventSource](https://html.spec.whatwg.org/multipage/server-sent-events.html#eventsource)` to the same domain. Authors can avoid this using the relatively complex mechanism of using unique domain names per connection, or by allowing the user to enable or disable the `[EventSource](https://html.spec.whatwg.org/multipage/server-sent-events.html#eventsource)` functionality on a per-page basis, or by sharing a single `[EventSource](https://html.spec.whatwg.org/multipage/server-sent-events.html#eventsource)` object using a [shared worker](https://html.spec.whatwg.org/multipage/workers.html#sharedworkerglobalscope).