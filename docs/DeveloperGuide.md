# DEVELOPER GUIDE

## Leifsen
### 25/10
 - Added a naive re-implementation of server in Go
 - Documented performance of server
 - Added the CI & Makefile for the repo
## Zheng Hao

## William
### 25/10
 - Added the `internal/protocol` package: message structs, decoders, encoders and validation
 - Added ping/pong ack helpers for detecting slow clients
 - Added unit, harness compatibility and fuzz tests for the protocol
 - Wrote the [protocol specification](protocol_specification.md)

### `internal/protocol`

How to work on the protocol package. For what it does and how to call it, see [protocol_specification.md](protocol_specification.md).

#### Layout

| File | Contents |
| --- | --- |
| `messages.go` | Structs for the five messages and the `Type*` constants |
| `decode.go` | `DecodeHello`, `DecodeSourceMessage`, and the per-message decode functions |
| `fields.go` | JSON object parsing: missing fields, `null`, wrong JSON types, integer and float parsing |
| `validate.go` | Range rules (`segment >= 1`, ...) and `history_seconds` clamping |
| `encode.go` | `CaptionUpdate.Encode`, `EncodeHistory` |
| `ack.go` | `EncodeAck`, `DecodeAck` |
| `errors.go` | `ErrMalformed`, `ErrUnknownType`, `ErrMissingField`, `ErrBadType`, `ErrOutOfRange` |

A message passes through three stages: `fields.go` reads raw JSON values and checks types, `decode.go` assembles the struct, and `validate.go` checks ranges. Each kind of rule lives in exactly one of these files.

#### Design decisions

- **Decode into a map, not a struct.** `json.Unmarshal` into a struct turns a missing field into `0`, so missing and zero look identical. Decoding into `map[string]json.RawMessage` keeps each value raw, so `fields.go` can tell absent, `null`, wrong type and out of range apart. Unknown fields are ignored for free: they are keys nobody looks up.
- **Strict and informational fields.** Fields that decide what happens to a caption (`segment`, `revision`, `source_ts`, `final`, `text`, and the `CLIENT_HELLO` resume fields) reject the message when bad. Fields nobody acts on (`session`, `client_id`, all of `SOURCE_DONE`) fall back to zero instead, so a strange value there can never cause a valid final to be dropped. Final completeness below 99% caps the hidden-performance marks.
- **`orNil` in `decode.go`.** In Go, a nil `*CaptionUpdate` stored in an `any` is not equal to `nil`, so returning one on error would make `msg != nil` true. `orNil` returns an untyped `nil` whenever there is an error. The table tests check `msg == nil` on every rejected input to keep it that way.
- **Encode once, reuse the bytes.** `Encode` is meant to run once per caption; the bytes are then shared by every client and by `EncodeHistory`, which only concatenates them. This keeps the cost of a broadcast independent of how many clients there are.
- **Re-encode instead of forwarding raw source bytes.** Clients always get exactly the seven spec fields, whatever extra fields the source sent.
- **Acks ride on ping/pong.** Harness clients send nothing after their hello, so there is no application ack to read. A ping payload is echoed back in the pong once the client's WebSocket library reads that far, so `EncodeAck`/`DecodeAck` carry a message count through it. The format is 8 bytes big-endian, so anything else (such as a client's own keepalive pong) is rejected by length.
- **Standard library only.** No third-party dependencies in this package.

#### Making changes

Every change must stay additive to spec section 4: never rename, remove or reinterpret an existing message or field.

**Adding a field**
1. Add it to the struct in `messages.go`, with a `json` tag matching the wire name exactly.
2. Read it in `decode.go` with the right helper: `required*` or `optional*` if the server acts on it, `informational*` if not.
3. Add a range rule in `validate.go` if it has one.
4. Add accept and reject cases in `decode_test.go`, and a round-trip case in `encode_test.go`.
5. Update `protocol_specification.md`.

**Adding a validation rule**
1. Type rules go in `fields.go`; range rules go in `validate.go`.
2. Wrap one of the existing sentinel errors and name the field, e.g. `fmt.Errorf("%w: segment must be >= 1", ErrOutOfRange)`. Don't add a new sentinel unless callers need to tell it apart.
3. Add a case to the matching reject table in `decode_test.go`.
4. If the rule adds a guarantee, add it to the checks in `fuzz_test.go`.

**Adding a message type**
1. Add a `Type*` constant and a struct in `messages.go`.
2. Add a `case` to `DecodeHello` (if it can be a first message) or `DecodeSourceMessage` (if the source sends it after its hello).
3. If the server sends it, add an encoder in `encode.go`.
4. Add tests for both acceptance and each rejection.

#### Testing

```sh
go test ./internal/protocol                                          # all tests
go test -race -cover ./internal/protocol                             # race detector and coverage
go test ./internal/protocol -run='^$' -fuzz=FuzzDecode -fuzztime=60s # fuzz the decoders
```

| File | Covers |
| --- | --- |
| `decode_test.go` | Every validation rule: accept and reject tables, `history_seconds` clamping, error messages |
| `encode_test.go` | `Encode` output against the spec example, round trips, `EncodeHistory` |
| `ack_test.go` | Ack round trip, byte order, wrong-length payloads |
| `compat_test.go` | The exact JSON the Python harness sends and reads |
| `fuzz_test.go` | Random input: no panics, only typed errors, guarantees hold |

- **Table tests.** Add a row rather than a new test function when the new case checks an existing rule.
- **`compat_test.go` holds byte-for-byte Python `json.dumps` output** (spaces after `:` and `,`, `\u` escapes). If the harness's message format ever changes, update these strings first.
- **Fuzz failures.** When fuzzing finds a failing input, Go saves it under `internal/protocol/testdata/fuzz/FuzzDecode/`. Fix the bug, then commit that file: plain `go test` then reruns it forever as a regression test.

#### Known limitations

- Integers written as floats above 2⁵³ (e.g. `9007199254740993.0`) lose precision when parsed. Plain integer syntax is exact up to the `int64` limit, and segment numbers never get near either.
- The pong shows how far the client's **WebSocket library** has read, not the application. Python's `websockets` buffers about 16 messages before it stops reading, so a slow client can be that far further behind than the ack shows.
- `EncodeHistory` does not validate its items; it only produces valid JSON if every item came from `Encode`.

## Sabil
