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

## William

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