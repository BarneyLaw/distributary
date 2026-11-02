# DEVELOPER GUIDE

## Leifsen
### 25/10
 - Added a naive re-implementation of server in Go
 - Documented performance of server
 - Added the CI & Makefile for the repo 

### 31/10
- Implement router.go to direct requests to correct handlers
- Implement source.go to handle websocket connection between caption source and the distributary server, includes statistics for captions segments
- Implement the first iteration of session control, including history for new joiners, reconnects and caption segment control

- Take note that we no longer use a single buffer ring. The buffer rings now tracks only the final of each segment. The caption itself is tracked via a map that maps the caption to a certain segment number.

### Session
Shared, bounded caption state. No I/O, no goroutines, no clock (the only time is the newest `source_ts`).
Holds a **ring of finals** (append-only, numbered by `fseq`, capped by count and age) and an **`active` map** (newest provisional per unfinished segment, overwritten by each revision).

* `New(cfg Config) *Session`: `DefaultConfig()` is 90 s retention, 1024 finals, 16 active segments.
* `Apply(u *protocol.CaptionUpdate) Verdict`: source handler only. Returns `Accepted`, `Duplicate`, `Stale`, `AlreadyFinal` or `TooOld`. On `Accepted`, wakes every waiting client.
* `Join(hello *protocol.ClientHello) (history []byte, cur Cursor)`: call once per client. Send `history` first (a complete `HISTORY` frame), then keep `cur` for that client.
* `Pick(cur *Cursor, max int, provisionals bool) (frames [][]byte, wake <-chan struct{}, behind bool)`: the client's next frames. Finals in order first, then the newest provisionals.
  - `max` = send credit; `provisionals=false` in degraded mode.
  - `frames` are pre-encoded: write them as-is, never modify them.
  - Nothing to send: wait on `wake` (it replaces `GetSnapshot().notification`).
  - `behind`: close with 1013; the client reconnects and recovers through `HISTORY`.
* One `Cursor` per client, used by that client's writer goroutine only. 

### Source handler (`internal/source`)
The only way captions enter the server, and the only caller of `Apply`: read, decode, `Apply`, count. Never sends to the source and never waits for clients.
* `New(sess, logger) *Handler`, `(h) Serve(ws, hello)`, `(h) Stats() Stats` (verdict and malformed counters, safe from any goroutine).
* Bad frames are counted and skipped. `SOURCE_DONE` is logged only. A new source replaces the old one.

### Router (`internal/server`)
Single entry point for every connection: upgrade, read the hello within 5 s, dispatch.
* `SOURCE_HELLO` → `Sources.Serve(ws, hello)`; `CLIENT_HELLO` → `Clients.AddClient(ws, hello) error`; bad or missing hello → close 1008.
* The handler owns the connection until it returns; the router then closes it.
* Non-WebSocket HTTP goes to the optional `Static` handler (browser files), otherwise 426.
* **Fanout:** `AddClient` must take the `hello` and call `sess.Join(hello)`.

## Zheng Hao

### 26/10
- Added caption revision replacement and provisional/final styling.
- Split JavaScript into connection, caption state, UI, and startup modules for better readability.
- Modified UI to show live captions visually significantly while (showing up to 3 segments at once on main screen)

### 31/10
- Improved UI styling with a gradient background, panel heading, and footer.
- Added the LR logo, favicon, and basic site metadata.


## William
### 25/10
 - Added the `internal/protocol` package: message structs, decoders, encoders and validation
 - Added ping/pong ack helpers for detecting slow clients
 - Added unit, harness compatibility and fuzz tests for the protocol
 - Wrote the [protocol specification](protocol_specification.md)

### Protocol (`internal/protocol`)
Turns WebSocket bytes into checked Go structs and back. No I/O, no goroutines, no state, so it is safe to call from any goroutine. Full details in [protocol_specification.md](protocol_specification.md).

* `DecodeHello(raw []byte) (any, error)`: first message on a connection. Returns `*SourceHello` or `*ClientHello`; anything else is `ErrUnknownType`.
* `DecodeSourceMessage(raw []byte) (any, error)`: source messages after its hello. Returns `*CaptionUpdate` or `*SourceDone`.
  - On error both return an untyped `nil`, so `msg == nil` checks are safe.
  - A returned `*CaptionUpdate` has every field present, `segment >= 1`, `revision >= 1` and `source_ts > 0`. A returned `*ClientHello` has `history_seconds` clamped to 0–60.
* `(c *CaptionUpdate) Encode() []byte`: encode once per caption, then reuse the bytes for every client.
* `EncodeHistory(items [][]byte) []byte`: builds a `HISTORY` frame from `Encode` output. No items gives `"items":[]`.
* `EncodeAck(sentCount uint64) []byte` / `DecodeAck(payload []byte) (uint64, error)`: puts a message count in a ping and reads it back from the pong, to tell how far behind a client is.
* Errors wrap `ErrMalformed`, `ErrUnknownType`, `ErrMissingField`, `ErrBadType` or `ErrOutOfRange`; compare with `errors.Is`.
* Bad `session`, `client_id` or `SOURCE_DONE` fields become zero instead of rejecting the message. Unknown fields are ignored.
* The protocol does not check caption state (already final, stale, too old). That is `session.Apply`.

## Sabil
### 30/10
- Implement a naive independent client connections based on naive.go
- Create an early documentation for fanout

### Fanout
Fanout is a component that enforce one independent connection per-client and all of its policies.

#### Hub
Hub is the only gateway for other components to communicate with fanout. Hub is instantiated only through NewHub(). The following functions are usable by other components:<br>
* NewHub(session *Session) *hub
  - Instantiates a new hub that is dependent on the passed session.
  - All clients orchestrated by the returned hub will retrieve caption from the passed session.
  - The hub structure is not exported, thus all of its inner structure are non accessible from other components. This also prevents instantiation of hub outside of NewHub().
* (h *hub) AddClient(connection *websocket.Conn) error
  - Add a new client connection to hub.
  - Trying to add a client to a hub that has been shutted down will return an error.
  - AddClient will not return unless there is an error or the client connection is terminated.
* (h *hub) Shutdown()
  - When executed, all connections orchestrated by hub will be terminated.
  - After Shutdown(), the hub will no longer be usable. A NewHub() must be executed.