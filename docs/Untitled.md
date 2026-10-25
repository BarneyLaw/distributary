# LiveRecall Go Server Design

Oct 19, 2026 · @leifsen

Proposal: a single Go binary, launched by `solution_server.py`, that keeps one shared bounded caption state and gives each client its own writer goroutine. The writer always sends pending finals first, then only the newest provisional revision, paced by a ping/pong acknowledgement clock. There are no per-client message queues. Built from the Assignment 4 spec and the starter kit; for team discussion, not final.

## Architecture at a glance

&#91;embedded content: server architecture · shared state above, one goroutine pair per client below\]

Only the shared state, above the line, grows with the lecture, and it is capped. Everything below it is a few integers per client, so a slow client costs no more memory than a fast one.

## Packaging

The evaluator still runs `python solution_server.py --port 8765`, so that file becomes a five-line launcher that replaces itself with the Go binary. Any language is allowed, but the start command is not negotiable, and a server that fails to start scores 0/30 on hidden performance.

```python
import os, sys, platform
arch = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[platform.machine()]
binary = os.path.join(os.path.dirname(os.path.abspath(__file__)), "bin", f"liverecall-linux-{arch}")
os.chmod(binary, 0o755)  # zip extraction can drop the executable bit
os.execv(binary, [binary] + sys.argv[1:])
```

- **Use `os.execv`, not `subprocess`.** `run_demo.py` stops the server with SIGTERM between runs. With `subprocess`, the signal kills only the Python parent, the Go child keeps port 8765, and the next run fails to bind. `execv` makes the Go process *be* the PID the harness signals.
- **Commit prebuilt static binaries** for linux/amd64 and linux/arm64 (`CGO_ENABLED=0`). Don't build at startup: the evaluation VM may have no Go toolchain or no internet, and a cold `go build` can exceed the 10-second readiness limit.
- **No vendoring.** The evaluator runs the committed binaries and never builds, so `go.sum` pinning the dependencies is enough.
- **Flags:** Go's `flag` package accepts `--port` as written. Every other flag must default to the best configuration, because the evaluator passes nothing else.
- **`requirements.txt`** keeps `websockets`, since the public harness needs it, even though the server no longer does.
- **One port for everything:** a plain `GET /` serves the browser client from `client/`, and a WebSocket upgrade on any path is a source or student connection. The harness connects to `ws://127.0.0.1:8765` with no path, so `/` must accept upgrades.

## Shared session state

The source goroutine is the only writer of one `Session` struct. Client writers only read it, briefly, under a `sync.RWMutex`, and never hold the lock across a network write.

**What it holds**

- **Caption** (immutable once created): segment, revision, `source_ts`, final, text, plus its compact JSON bytes, serialized once and shared by every client. Under load this saves N-1 `json.Marshal` calls per update.
- **Finals ring:** a fixed-capacity circular buffer of finalized captions. Each final gets a monotonic sequence number `fseq`, which is what client cursors point at. Retention is bounded twice: by time (about 90 s of `source_ts`, which covers the 60 s recovery requirement with margin) and by a hard count cap (for example 1,024).
- **Active segments:** `map[segment]*Caption` holding only the newest provisional revision. It normally has one entry, because the source finishes a segment before starting the next, but it is capped (for example 16) in case a source breaks that pattern.
- **Finalized index:** `map[segment]fseq` for O(1) "is this segment final?" checks. Entries are deleted when the ring evicts them.

**Accepting a `CAPTION_UPDATE`**, checked in order:

1. Missing or mistyped required fields: drop and count it. Unknown fields are ignored, per spec 4.1.
2. Segment already finalized: drop. A final is durable.
3. Segment older than the oldest retained final: drop as too old.
4. Revision not greater than the stored revision for that segment: drop as stale or duplicate.
5. Otherwise accept. A final is appended to the ring and its active entry deleted; a provisional replaces the active entry.

**Waking clients without blocking the source:** the session holds a `changed chan struct{}`. Each accepted update closes it and installs a fresh one (broadcast by close). Every client writer waiting on the old channel wakes at once, and the source does O(1) work however many clients exist. No client can ever slow ingestion, which the naive starter could not guarantee.

## Per-client delivery

Each connection gets two goroutines and a few integers of state, never a message queue. That is what makes per-client memory O(1) and slow-client isolation structural rather than tuned.

- **Reader goroutine:** keeps reading frames so the pong handler and close detection fire. Anything else the client sends after `CLIENT_HELLO` is ignored.
- **Writer goroutine:** the only goroutine that writes data frames (gorilla allows one concurrent writer). It owns the client's cursors: `finalCursor` (last `fseq` sent) and `sentRev` (last revision sent per active segment).

**Writer loop**

```go
for {
    if credit := window - (sendSeq - ackedSeq.Load()); credit > 0 {
        batch := session.Pick(cursors, credit) // RLock: finals after finalCursor first, in order,
                                               // then the newest provisional per active segment
                                               // whose revision > sentRev and not yet finalized
        writeAll(batch)                        // advances cursors and sendSeq
        if len(batch) > 0 { ping(sendSeq) }    // 8-byte payload = messages sent so far
    }
    select {
    case <-session.Changed(): // new caption state
    case <-ackCh:             // pong arrived, credit returned
    case <-keepalive.C:
    case <-ctx.Done():
        return
    }
}
```

**Ping/pong acknowledgement clock.** The pong handler stores the echoed `sendSeq` in `ackedSeq` and nudges `ackCh`. So `sendSeq - ackedSeq` counts messages still sitting in network buffers. This works with the unmodified harness client, because the Python websockets client answers a ping only when its reader reaches that ping in the byte stream, and it stops reading once 16 frames are queued unread. Browsers answer pings automatically too.

**Conflation falls out for free.** While the window is full the writer sends nothing, and the active-segment slot in shared state keeps being overwritten. When credit returns, the writer sends only the newest revision. Finals are never dropped, because they are read from the shared ring by cursor rather than copied per client. A fast client always has credit, so it receives every revision and keeps Normal Update Coverage at 100%.

**Window size** (start at W = 8 messages, then tune). It must exceed what the source produces in one round trip on an impaired network, or healthy clients get throttled. Example: 100 ms netem delay each way gives a 200 ms RTT, during which the public source produces about 1 message.

**Degraded mode for the backlog the server can't see.** Pongs keep flowing until the client library's 16-frame queue fills, so a slow reader can still sit about 16 messages behind (roughly 8 s at 2 messages/s). The second signal is queueing delay: smoothed pong RTT minus the minimum RTT seen on that connection.

1. Delay above a high threshold (start at 250 ms): enter degraded mode and send finals plus at most one provisional per segment. Finals arrive at 1.33/s, under a 2/s reader's rate, so its library queue drains.
2. Delay below a low threshold for about 2 s: return to full revisions. The gap between the two thresholds stops the mode flapping.

This matches spec section 10: overload handling switches on because of real lag and switches off when the client catches up. Expect to tune it, since without enough hysteresis a slow reader will oscillate between modes.

**Hard limits**

- `SetWriteDeadline` (about 10 s) on every write. A write blocked that long means the client stopped reading entirely, so close the connection. This bounds goroutines and memory for dead clients.
- Small kernel send buffer via `TCPConn.SetWriteBuffer` (for example 16 KB), so less stale data hides on the server side. The receiver's buffers stay outside our control, which is why the ack clock exists.
- Cursor fell off the end of the ring (client more than about 90 s behind): close with code 1013. Both the harness and our browser client reconnect and resume from `HISTORY`.

## Reconnect and late join

`HISTORY` and the live stream are stitched at one `fseq` taken under one lock, so a final can't fall into a gap or arrive twice. Late join is the same path with `last_segment = 0`.

On `CLIENT_HELLO`, under a single read lock:

1. Clamp `history_seconds` to 0 to 60 and treat missing resume fields as 0.
2. Select ring finals with `segment > last_segment` and `source_ts` within `history_seconds` of the newest `source_ts` seen. Measuring against the source's clock instead of the server's means clock skew between machines can't empty the window.
3. Set `finalCursor` to the ring's newest `fseq` at that moment. Any final committed afterwards has a higher `fseq` and goes out live.
4. Leave `sentRev` empty, so the writer sends the current provisional right after `HISTORY`.

The writer's first frame is always `HISTORY`. It is built by concatenating the stored JSON bytes of each item (`{"type":"HISTORY","items":[` + items + `]}`), so a reconnect storm costs one memory copy of about 16 KB per client, not re-marshalling. As a defensive extra check, the live path also skips any final with `segment <= last_segment`.

**Browser client, for the same contract:** keep finals keyed by segment, with their `source_ts`, for at least 60 s, which also serves "What did I miss?" locally. Resume with the newest final held, merge `HISTORY` by segment (which deduplicates by construction), and reconnect with jittered exponential backoff (about 0.25 s doubling to 4 s) so a mass disconnect doesn't hit the server all at once.

## Invariants

These six properties are the design's correctness claims. Each needs a unit test in `internal/session`, and every team member should be able to point to the line that enforces it, since that is what the viva asks.

1. **Monotonic revisions:** for each client and segment, revisions sent strictly increase. Enforced by `sentRev` and acceptance rule 4.
2. **Finals are terminal:** once a client has a segment's final, nothing more about that segment is sent to it.
3. **No final skipped:** every final retained in the ring after a client's `finalCursor` is sent while the client stays connected, in `fseq` order.
4. **Valid HISTORY:** finals only, oldest to newest, all with `segment > last_segment`, all inside the requested window.
5. **Bounded memory:** server memory is O(ring capacity + number of clients). There is no per-client message buffer anywhere in our code.
6. **Source never blocks:** ingesting an update does O(1) work regardless of client count or client speed.

## Experiments

Build the comparisons into the binary from day one as flags, so every before/after is the same build and the same harness run. The defaults must be the final design, because the evaluator passes only `--port`.

| Flag | What it switches off | Comparison it gives the report | Public test |
| --- | --- | --- | --- |
| `--mode=naive` | Everything: direct broadcast, like the starter | Baseline vs final | T3 slow readers |
| `--flow=off` | Ack clock and conflation (unbounded per-client queue) | "Conventional queue-based" vs ours, the spec's bar for full marks on the advanced challenges | T3, and T3 with `bandwidth_2mbit.sh` |
| `--degraded=off` | Queueing-delay signal and degraded mode | Value of the second lag signal for slow readers | T3 |
| `--compress=on` | Turns on permessage-deflate | Bytes per caption under constrained bandwidth | `bandwidth_2mbit.sh` |
| `--window`, `--qdelay-high`, `--qdelay-low` | Not a switch; tuning values | Parameter sweeps for the report | T3, T5 |

Baseline reference: the starter server on a 30 s T3 run (one run, cloud container) gave slow clients P50 7.2 s and 3.45% fresh, with completeness 95.28%. Rerun it with `--runs 3` on our Ubuntu VM for the report.

**Reproducing the bandwidth challenge:** `bandwidth_2mbit.sh` shapes all loopback traffic as one shared 250 KB/s pipe. At about 200 B per message and 4 messages/s, that pipe saturates at roughly 300 clients, so 100 public clients won't show overload. Run it with 500 or more.

**Compression caveat:** gorilla only supports permessage-deflate without context takeover, and roughly 200-byte JSON messages compress poorly without context. Measure before relying on it.

**Observability:** log one stats line per second to stderr: clients, clients in degraded mode, max inflight, provisionals coalesced, ring size, heap, goroutines. Optionally add `--debug-addr` (off by default) for pprof. Run `monitor_server.py` alongside for RSS. These logs also cover the viva's "show logs/traces" questions.

## Repo layout and ownership

The package boundaries follow the sections above, so four people can work in parallel against one interface: `Session.Pick` and `Session.Snapshot`. `internal/session` does no I/O and is entirely caption-specific, which keeps `internal/client` close to the generic conflating fan-out core if we spin it out later.

```
submission_root/
├── solution_server.py     # execv launcher (Packaging)
├── requirements.txt       # websockets, for the public harness
├── go.mod  go.sum
├── cmd/liverecall/        # main: flags, HTTP + WebSocket upgrade, signals
├── internal/protocol/     # message types, decode/validate, compact encode
├── internal/session/      # ring, active segments, accept rules, Pick, Snapshot
├── internal/client/       # reader/writer goroutines, ack clock, degraded mode
├── internal/stats/        # per-second stats line, optional pprof
├── bin/                   # liverecall-linux-amd64, liverecall-linux-arm64
├── client/                # index.html, app.js
├── experiments/           # run scripts, raw CSV/JSON, plotting
├── Makefile               # build, cross-compile, test
└── README.md  AI_USAGE.md  CONTRIBUTIONS.md  report.pdf
```

| Owner | Area | Main deliverables |
| --- | --- | --- |
| Member A | `internal/session`, `internal/protocol` | Accept rules, ring, `Pick`, HISTORY snapshot, invariant unit tests |
| Member B | `internal/client`, `cmd/liverecall` | Writer loop, ack clock, degraded mode, write deadlines, flags |
| Member C | `client/` | Revision replacement, 60 s local history, 15/30/60 s recall, reconnect and resume, connection-state UI, demo video |
| Member D | Launcher, `Makefile`, `experiments/` | Cross-compiled binaries, `submission_check.py` passing, T1 to T5 at 3 runs each, tc/netem runs, before/after figures |

The report is shared: each owner writes the section for their area. Because the viva is individual, everyone reviews at least one other person's pull request in `session` or `client`.

## Milestones

There are 16 days to the deadline, and the riskiest assumption gets tested first.

- [ ] Spike: confirm that pong timing tracks the Python harness client's read position under `--slow-ms 500` (Member B) · Oct 21, 2026
- [ ] End to end: source to Go server to harness and browser, accept rules, correct `HISTORY`, `submission_check.py` passing through the launcher · Oct 25, 2026
- [ ] Browser client complete: replacement, recall views, reconnect and resume · Oct 28, 2026
- [ ] Ack clock and degraded mode in; T1 to T4 clean at 100 clients · Oct 30, 2026
- [ ] Experiments: T1 to T5 at 3 runs each, before/after via flags, bandwidth run at 500 or more clients · Nov 1, 2026
- [ ] Report, demo video, README, AI\_USAGE, CONTRIBUTIONS, `submission_check.py --final` · Nov 3, 2026
- [ ] Submit on Canvas · Nov 4, 2026
- [ ] Book the viva slot now; demo and viva · Nov 6, 2026

If the spike fails, both lag signals go with it, since they share the pong. The fallback is a small kernel send buffer plus write blocking as the overload trigger, which detects lag later. Decide before the end-to-end milestone.

## Open questions and risks

The first item matters most for scoring and needs the professor's answer before anyone builds it.

| Question | Why it matters | Proposed default | Decided by |
| --- | --- | --- | --- |
| May the server send `HISTORY` mid-stream as a batch of finals? | A reader slower than the finals rate (below 1.33 messages/s) can never keep up at one final per frame. The harness counts recovered `HISTORY` items toward completeness. But on reconnected or late clients each extra `HISTORY` adds a `recovery_ms` sample measured from connect time, which inflates recovery P95. | Don't build it until confirmed | Professor |
| On a long reconnect, honor `history_seconds` strictly, or send every retained final after `last_segment`? | Strict follows the request's meaning; sending more raises completeness after long outages | Strict; ask alongside the first question | Professor |
| Can all four of us defend Go code in the individual viva? | 20 marks, individually assessed; any member can be asked about any code path | Decide in this meeting; Go is only worth it if everyone can read `session` and `client` | Team |
| Which WebSocket library? | Shapes ping handling, compression and broadcast cost | `gorilla/websocket`: `PreparedMessage` for broadcast, explicit ping payloads, `WriteControl` safe to call concurrently. Alternative `coder/websocket` has a context API and context-takeover compression. | Team |
| What CPU architecture is the evaluation VM? | Wrong binary means the server can't start | Ship amd64 and arm64; confirm if convenient | Professor (optional) |
| What happens if the source reconnects and segment numbers restart? | Tests restart the server per run, so it's unlikely, but it would freeze ingestion | A `SOURCE_HELLO` whose segments fall below the newest final resets the session | Team |
| Window and queueing-delay thresholds | Too tight throttles healthy clients under netem delay; too loose lets slow readers lag | Start at W = 8, 250 ms high, low around 100 ms; settle by sweep | Members B and D |
