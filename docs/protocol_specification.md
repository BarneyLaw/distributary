# Protocol Specification

This document describes the `internal/protocol` package: the message structs, the decoders and encoders, the validation rules, and the ping/pong ack helpers. It covers what each function takes, what it returns, and what a returned value is guaranteed to contain.

- **Import:** `github.com/BarneyLaw/distributary/internal/protocol`
- **Code:** [`internal/protocol/`](../internal/protocol/)
- **Tests:** `go test ./internal/protocol`

## 1. Scope

The package only describes, decodes and encodes messages. It does no I/O, starts no goroutines and keeps no state, so every function is safe to call from any goroutine.

It does not check anything that needs caption state, such as whether a segment is already final or a revision is stale. Those decisions are outside the protocol package.

## 2. Messages

All messages are in the form of JSON objects sent as WebSocket text frames, each with a `"type"` field. Each `CAPTION_UPDATE` carries the text of an entire revision corresponding to a segment.

| `type` | Direction | Go type |
| --- | --- | --- |
| `SOURCE_HELLO` | source → server | `SourceHello` |
| `CAPTION_UPDATE` | source → server, server → client | `CaptionUpdate` |
| `SOURCE_DONE` | source → server | `SourceDone` |
| `CLIENT_HELLO` | client → server | `ClientHello` |
| `HISTORY` | server → client | `History` |

The type strings are available as the constants `TypeSourceHello`, `TypeCaptionUpdate`, `TypeSourceDone`, `TypeClientHello` and `TypeHistory`. Every struct has a `Type string` field holding one of them.

### SOURCE_HELLO

```json
{"type": "SOURCE_HELLO", "session": "L1"}
```

| JSON | Go field | Go type | Required | Meaning |
| --- | --- | --- | --- | --- |
| `session` | `Session` | `string` | no | Session id. Not checked. |

### CAPTION_UPDATE

```json
{"type": "CAPTION_UPDATE", "session": "L1", "segment": 42, "revision": 3,
 "source_ts": 1760000000.123, "final": true, "text": "TCP uses cumulative acknowledgements."}
```

| JSON | Go field | Go type | Required | Meaning |
| --- | --- | --- | --- | --- |
| `session` | `Session` | `string` | no | Session id |
| `segment` | `Segment` | `int64` | yes | Caption segment, ≥ 1 |
| `revision` | `Revision` | `int64` | yes | Version within the segment, ≥ 1 |
| `source_ts` | `SourceTS` | `float64` | yes | Unix seconds on the source's clock, > 0 |
| `final` | `Final` | `bool` | yes | `true` means the segment is complete, `false` means that there are more incoming revisions for the segment.|
| `text` | `Text` | `string` | yes | Whole caption text of this revision. May be empty. |

### SOURCE_DONE

```json
{"type": "SOURCE_DONE", "session": "L1", "source_ts": 1760000060.0, "segments": 80}
```

| JSON | Go field | Go type | Required | Meaning |
| --- | --- | --- | --- | --- |
| `session` | `Session` | `string` | no | Session id |
| `source_ts` | `SourceTS` | `float64` | no | Send time |
| `segments` | `Segments` | `int64` | no | Number of final segments emitted |

The spec marks this as a test-control message.

### CLIENT_HELLO

```json
{"type": "CLIENT_HELLO", "client_id": 17, "session": "L1",
 "last_segment": 38, "last_revision": 3, "history_seconds": 30}
```

| JSON | Go field | Go type | Required | Default | Meaning |
| --- | --- | --- | --- | --- | --- |
| `client_id` | `ClientID` | `int64` | no | 0 | Client id |
| `session` | `Session` | `string` | no | `""` | Session id |
| `last_segment` | `LastSegment` | `int64` | no | 0 | Newest finalized segment the client holds, ≥ 0 |
| `last_revision` | `LastRevision` | `int64` | no | 0 | Revision of that final, ≥ 0 |
| `history_seconds` | `HistorySeconds` | `float64` | no | 0 | Recovery window requested, limited to 0–60 |

Per the spec, a new client sends `last_segment = 0` and `last_revision = 0`.

### HISTORY

```json
{"type": "HISTORY", "items": [ {CAPTION_UPDATE}, {CAPTION_UPDATE} ]}
```

| JSON | Go field | Go type | Meaning |
| --- | --- | --- | --- |
| `items` | `Items` | `[]CaptionUpdate` | Caption objects. An empty list is encoded as `[]`, never `null`. |

`EncodeHistory` builds this frame from already-encoded caption bytes. The `History` struct is there for parsing a `HISTORY` frame, for example in tests.

## 3. API

Decoders are needed because network carries bytes, but the server's logic needs Go values. These decoders allow you to convert between the two.

```go
func DecodeHello(raw []byte) (any, error)         // *SourceHello | *ClientHello
func DecodeSourceMessage(raw []byte) (any, error) // *CaptionUpdate | *SourceDone
func (c *CaptionUpdate) Encode() []byte           // compact JSON, "type" always set
func EncodeHistory(items [][]byte) []byte         // HISTORY frame from Encode() bytes
func EncodeAck(seq uint64) []byte                 // 8-byte ping payload
func DecodeAck(payload []byte) (uint64, error)    // seq back from a pong payload

const MaxHistorySeconds = 60
const AckSize = 8
```

### DecodeHello

Decodes the first message on a connection. Returns `*SourceHello` or `*ClientHello`. Any other `type` returns `ErrUnknownType`.

```go
msg, err := protocol.DecodeHello(raw)
if err != nil {
	// not a valid hello
}
switch m := msg.(type) {
case *protocol.SourceHello:
	// ...
case *protocol.ClientHello:
	// m.LastSegment, m.LastRevision, m.HistorySeconds are already normalised
}
```

### DecodeSourceMessage

Decodes a message from the source after its hello. Returns `*CaptionUpdate` or `*SourceDone`. Any other `type`, including a hello, returns `ErrUnknownType`.

On error, both decoders return an untyped `nil`, so `msg == nil` checks are reliable.

### Encode

Returns compact JSON containing exactly the seven `CAPTION_UPDATE` fields. It always sets `"type":"CAPTION_UPDATE"`, does not modify its receiver, and does not escape HTML characters (`<` stays `<`). Unknown fields from the original message are not included.

The output is meant to be computed once per caption and reused, so the same bytes can go to many clients without marshalling again.

### EncodeHistory

Joins already-encoded captions as `{"type":"HISTORY","items":[` + items separated by `,` + `]}`, in the order given. It does not re-marshal or validate the items, so it expects bytes produced by `Encode`. A `nil` or empty slice gives `"items":[]`.

### EncodeAck and DecodeAck

These helpers put a sequence number in a WebSocket ping payload and read it back from the pong, which lets the server detect slow readers.

- `EncodeAck(seq)` returns 8 bytes: `seq` as a big-endian `uint64`.
- `DecodeAck(payload)` returns that number. A payload that is not exactly 8 bytes returns `ErrMalformed`.

```text
Server sends:   msg1  msg2  msg3  [ping: 3]  msg4  msg5  [ping: 5]
Client reads:   msg1  msg2  msg3  [ping: 3] → replies pong: 3
                (client is slow, hasn't reached msg4 yet)

Server knows:   sent = 5, acked = 3  →  2 messages in flight
```

## 4. Errors

Every error wraps one of these and names the field involved, for example `protocol: required field missing: text`. Compare with `errors.Is`.

| Error | Meaning |
| --- | --- |
| `ErrMalformed` | Not JSON, or not a JSON object. From `DecodeAck`: payload is not 8 bytes. |
| `ErrUnknownType` | `type` is missing, not a string, unknown, or not valid for this decoder |
| `ErrMissingField` | A required field is absent or `null` |
| `ErrBadType` | A field has the wrong JSON type, e.g. `"segment": "42"` |
| `ErrOutOfRange` | A value is outside its allowed range |

Decoding never panics, whatever the input.

## 5. Guarantees

A value returned **without error** already satisfies these.

**`*CaptionUpdate`**
- `segment`, `revision`, `source_ts`, `final` and `text` were all present with the right JSON types.
- `Segment ≥ 1`, `Revision ≥ 1`, and `SourceTS` is finite and `> 0`.
- `Type` is `CAPTION_UPDATE`. Unknown fields have been dropped.

**`*ClientHello`**
- `LastSegment ≥ 0` and `LastRevision ≥ 0`. A missing field is 0.
- `HistorySeconds` is in `[0, 60]`.

**`*SourceHello` and `*SourceDone`**
- Always returned when the `type` matches. A bad value in one of their fields becomes the zero value instead of rejecting the message.

**Not checked:**
- whether the segment is already finalized
- whether the revision is newer than one seen before
- whether the segment is too old
- whether the session id is `L1`

## 6. Validation rules

| Rule | Reason |
| --- | --- |
| Unknown fields are ignored on every message. | Spec 4.1 says the source may send extra fields. |
| A JSON `null` is treated as absent. | `"segment": null` and a missing `segment` mean the same thing. |
| Integers accept whole-number JSON (`42`, `42.0`, `1e1`) and reject `42.5`, `"42"` and `true`. | JSON has one number type, and some senders write integers as floats. |
| Numbers must be finite, and integers must fit in `int64`. | Rules out overflow and NaN. |
| Keys are case-sensitive: `SEGMENT` is not `segment`. | Matches the spec and Python's `json`. |
| For a repeated key, the last value wins. | Matches Python's `json.loads`, which the harness uses. |
| Fields that matter (`segment`, `revision`, `source_ts`, `final`, `text`, `last_segment`, `last_revision`, `history_seconds`) reject the message on a wrong type or range. | A bad value in one of these can't be safely guessed. |
| Fields that are only informational (`session`, `client_id`, everything in `SOURCE_DONE`) fall back to the zero value. | A bad value in a field nobody acts on shouldn't cause a valid caption to be dropped. |

## 7. Testing

- Table tests cover every rule above: each missing field, each wrong type, zero and negative values, unknown fields, and the `history_seconds` clamp.
- Round-trip tests check that `Encode` output decodes back to the same values, and that it matches the spec's example byte for byte.
- Compatibility tests decode the exact JSON that the harness's `caption_source.py` and `load_clients.py` send, and check that `Encode` and `EncodeHistory` output parses the way the harness reads it.
- `FuzzDecode` feeds random input to both decoders and checks they never panic, only return the errors above, and that accepted values meet the guarantees in section 5.

