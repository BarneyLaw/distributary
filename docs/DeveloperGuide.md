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

### Fanout (`internal/fanout`)
Fanout is a component that enforce one independent connection per-client and all of its policies.

#### Hub
Hub is the only gateway for other components to communicate with fanout. **The hub struct is not exposed to other components**, a Hub interface is used instead. Hub is instantiated only through NewHub(). The following `fanout` functions are usable by other components:<br>
* `NewHub(session *Session) *hub`
  - Instantiates a new hub that is dependent on the passed session.
  - All clients orchestrated by the returned hub will retrieve caption from the passed session.
  - The hub structure is not exported, thus all of its inner structure are non accessible from other components. This also prevents instantiation of hub outside of NewHub().
* `(h *hub) AddClient(connection *websocket.Conn) error`
  - Adds a new client connection to hub.
  - Trying to add a client to a hub that has been shutted down will return an error.
  - AddClient is a blocking function. It will not return unless there is an error or the client connection is terminated.
  - It is recommended to call AddClient as a go routine.
* `(h *hub) Shutdown()`
  - Shuts a hub down and terminates all connections orchestrated by the hub.
  - After `Shutdown()`, the hub will no longer be usable. Another hub must be instantiated through `NewHub()`.
  - This function wraps `helperShutdown` inside a `sync.Once` to ensure that multiple concurrent calls to `Shutdown()` will yield in only one execution.
Hub also holds all parameters used for enforcing client connection policy. The details can be observed in hub's `struct` definition.

#### Client
Client is responsible to handle independent per-client connection. Every instance of `client` handles the connection and enforces policies independently from other instances. `client` is not exposed to other component to reduce coupling. `client`'s `struct` holds statistics necessary to decide how to enforce the connection policies based on the parameters given in `hub`'s `struct`. Below are useful `client`'s method that are important to understand how `client` works:
* `(c *client) readPump(wg *sync.WaitGroup, ctxCancel context.CancelFunc)`
  - Acts as a sole reader from the websocket connection. It ensures independence between reading and writing task to prevent concurrency problems.
  - The function main responsibility is to handle pong frame. There are 2 types of pong: keep alive pong and acknowledgement pong. For each acknowledgement pong read, the function will compute statistics to determine the status of a client (normal or degraded). Each client status carries its own policies.
  - Helper functions used to compute statisics and to determine client's status are `(c *client) computeSRTT(alpha float64)`, `(c *client) computeMinRTT()`, `(c *client) computeQueueDelay()`, `(c *client) determineStatus()`.
  - This function must be run inside a go routines.
* `(c *client) writePump(wg *sync.WaitGroup, ctx context.Context)` 
  - Acts as a sole writer to the websocket connection.
  - The function responsibilities are to retrieve captions from `Session` through `hub` and to send captions to client based on policies tied to client's status.
  - Helper functions used is `(c *client) write(caption []byte, writerCtx context.Context) error`. The writing process is separated from `writePump()` mainly to improve readibility.
  - This function must be run inside a go routines.
* `(c *client) write(caption []byte, writerCtx context.Context) error`
  - Executes the writing to the websocket connection when called by `writePump()`.
  - Helper functions used is `(c *client) startPingingMode(num uint64, writerCtx context.Context, acked chan struct{}) error`. Pinging mode is when the server sends acknowledgement ping to the client. For normal client, it is sent every `hub.maxInFlight` sequences. For slow client, it is sent for every sequence.
* `(c *client) startPingingMode(num uint64, writerCtx context.Context, acked chan struct{})`
  - Sends acknowledgement ping to the client.
  - This function will wait for `readPump()` to notify that and acknowledgement pong has been received from the client.
  - If no notification from `readPump()` for `hub.pongTimeout` time, the function will return an error. The error will be propagated to `writePump()` which will trigger connection termination.
**Client connection policies based on its status:**
  1. A new client will be given the available caption history as stored by `Session` before anything else.
  2. A normal client will be given either provisional or final new captions as retrieved from `Session`.
  3. A slow/degraded client will be given only final captions until it recovers.
  4. If client is too slow, it will be disconnected.
  5. If client asked for captions beyond `Session`'s retained history, it will be disconnected.
