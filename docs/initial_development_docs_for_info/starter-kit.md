# Starter Kit Reference

What every file in the professor's starter kit does, how the pieces fit together during a test run, and what each one means for our Go server.

Source: `CS3103_A4_LiveRecall_Student_Starter_Updated` (the kit committed at the root of this repo) and the Assignment 4 spec (last updated 17 Oct 2026). Where this doc says "measured", the number comes from runs in a cloud Linux container, not our Ubuntu VM. Rerun before quoting anything in the report.

## Contents

1. [At a glance](#at-a-glance)
2. [Things that will bite us](#things-that-will-bite-us)
3. [How a public test run fits together](#how-a-public-test-run-fits-together)
4. [Protocol quick reference](#protocol-quick-reference)
5. [Components](#components)
6. [Measured baselines](#measured-baselines)

## At a glance

The kit has three kinds of file: a naive baseline we replace, a test harness we must not change, and guides and templates.

| File | Role | Can we change it? |
| --- | --- | --- |
| `solution_server.py` | Naive broadcaster and the **required evaluator entry point** | Replace the body; keep `python solution_server.py --port N` working |
| `client/index.html`, `client/app.js` | Naive browser UI | Replace (graded TODOs) |
| `requirements.txt` | Python dependencies (`websockets>=13,<17`) | Edit, but keep `websockets` because the harness needs it |
| `caption_source.py` | Deterministic caption producer | **No** |
| `load_clients.py` | Simulated students; writes the results CSV | **No** |
| `run_demo.py` | One-command public test runner | **No** |
| `analyse_results.py` | Computes all public metrics | **No** (spec: "without modifying its measurement logic") |
| `submission_check.py` | Startup and file-structure check | **No** |
| `monitor_server.py` | Samples server memory (RSS) | **No** |
| `network_profiles/*.sh` | `tc` impairment presets | Keep the originals; add our own scripts beside them |
| `sample_transcript.txt` | 10 sentences (identical to the source's built-in defaults) | No need |
| `START_HERE.md`, `PUBLIC_TESTS.md`, `DEMO_GUIDE.md`, `PROTOCOL_EXAMPLES.md`, `NETWORKING_PRIMER.md`, `UBUNTU_SETUP.md`, `FILES.md` | Guides | Read only |
| `README_TEMPLATE.md`, `AI_USAGE_TEMPLATE.md`, `CONTRIBUTIONS_TEMPLATE.md` | Templates for required deliverables | Fill in as `README.md`, `AI_USAGE.md`, `CONTRIBUTIONS.md` |
| `bonus_asr/` | Optional ASR bonus guidance | Ignore unless we attempt the bonus |

The spec says "Do not modify instructor test programs or assume knowledge of hidden timing/parameters." The hidden evaluator uses its own copies, so editing ours would only make our public numbers wrong.

## Things that will bite us

Each of these comes from reading the harness code or from a measurement. They are ordered by how much damage they can do.

1. **The 2 Mbit/s profile permanently stalls any TCP segment over about 4 KB.** `bandwidth_2mbit.sh` installs `tbf ... burst 32kbit`, which is a 4,000-byte bucket. Loopback's MTU is 65,536, so TCP happily sends bigger segments, and `tbf` drops them, retransmissions included, forever. Measured with the profile applied:
   - single writes of 200 B, 3,000 B and 3,900 B arrive; writes of 4,200 B, 8,000 B, 16 KB and 64 KB never arrive (6 s timeout, `tbf` reported 137 drops);
   - 200 back-to-back 200 B writes also stall after 8,600 B, because TCP merges queued small writes into one large segment once a queue forms;
   - setting `TCP_MAXSEG` to 1400 on the server's **listening** socket fixes every case: the 16 KB write took 0.05 s and the 200-write burst 0.17 s.

   A 60-second `HISTORY` is about 16 KB, and queueing is exactly the bandwidth challenge's situation, so without the clamp, reconnecting clients under constrained bandwidth would never recover. In Go, set the option through `net.ListenConfig{Control: ...}` with `syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_MAXSEG, 1400)`. The hidden evaluator may shape traffic differently, but the clamp costs nothing.

2. **`monitor_server.py` finds the server by searching process command lines for `solution_server.py`.** If our launcher `exec`s a Go binary, the command line changes and the monitor (and possibly the hidden memory check) finds nothing. Fix: keep `solution_server.py` as `argv[0]` when exec'ing: `os.execv(binary, ["solution_server.py"] + sys.argv[1:])`.

3. **The harness resumes from the highest final it has seen.** If we ever send segment 41's final before segment 40's, a client that disconnects in between reports `last_segment=41`, and `HISTORY` (which excludes everything up to `last_segment`) can never give it segment 40. Finals must go out to each client in order with no gaps.

4. **`run_demo.py` only works with its siblings beside it.** It requires `solution_server.py`, `load_clients.py`, `caption_source.py` and `analyse_results.py` in its own directory, and writes its CSV and JSON results there too. This constrains how we reorganize the repo (see `project-structure.md`).

5. **Our server must keep reading every connection.** Harness clients send their own WebSocket ping every 20 s and drop the connection if no pong arrives within 20 s. In Go that means a reader goroutine per connection that never stops while the connection lives.

6. **Closing a client's connection is counted as an error.** When the server closes a connection, the harness logs a `CONNECT_ERROR` row, waits 0.4 s and reconnects. Our "client fell too far behind, disconnect it" policy will show up in "Connection errors".

7. **"Normal Update Coverage" in the public output counts every client, including slow ones,** and assumes exactly 3 revisions per segment. Under T3 our conflation will legitimately push it below 100%. The spec's hidden metric only counts unconstrained operation; the report should say so.

8. **100 clients do not saturate 2 Mbit/s.** All loopback traffic shares one 250 KB/s bucket, and 100 clients at about 4 messages/s each use roughly 80 KB/s. Measured: the naive server under the profile stays 100% fresh at 100 clients but falls to 0.6% fresh at 400. Bandwidth experiments need 300 or more clients.

9. **Completeness and fresh-final ratios are per client.** The denominator is `segments × clients`. A final recovered through `HISTORY` counts toward completeness but is almost never "fresh" (under 500 ms since `source_ts`), so disconnects always cost some fresh-final ratio.

## How a public test run fits together

One `python run_demo.py ...` invocation runs this sequence once per `--runs`:

1. Start `python -u solution_server.py --port 8765` (output hidden with `--quiet-server`).
2. Wait up to 10 s for the port to accept a plain TCP connection. It does not check WebSocket readiness here.
3. Start `load_clients.py`, which opens all N student connections; each sends `CLIENT_HELLO` and receives `HISTORY`.
4. Sleep `--source-start-delay` (1 s), then run `caption_source.py --segments S --quiet` until it has sent S segments.
5. Wait for `load_clients.py` to reach its `--duration` and write the CSV.
6. Run `analyse_results.py` on the CSV, print the summary, write `<stem>[_runNN].json`.
7. Send SIGTERM to the server (SIGKILL after 3 s), sleep 0.25 s, start the next run.

After all runs it prints the median of the key metrics. The number of segments S is chosen so the source finishes about 2 s before the clients stop:

```
S = max(4, floor(max(3, duration - source_start_delay - 2) / 0.75))
```

For example, T2 (`--duration 40`) sends 49 segments, so each client should see 49 finals and 147 revisions.

## Protocol quick reference

All messages are JSON text frames on one WebSocket. The source and students connect to the same port, and the first message decides which one a connection is.

| Message | Direction | Fields | Rules that matter |
| --- | --- | --- | --- |
| `SOURCE_HELLO` | source → server | `session` | First message on the source connection. One source, session `L1`. |
| `CAPTION_UPDATE` | source → server, server → client | `session`, `segment`, `revision`, `source_ts`, `final`, `text` | Segments start at 1 and increase. Revisions increase per segment; never assume 3. A final is durable: no older revision may overwrite it. Ignore unknown fields. |
| `SOURCE_DONE` | source → server | `session`, `source_ts`, `segments` | Test control only. No need to forward it. |
| `CLIENT_HELLO` | client → server | `client_id`, `session`, `last_segment`, `last_revision`, `history_seconds` | `last_*` is the newest **finalized** state the client holds (0, 0 for a new client). `history_seconds` is at most 60. |
| `HISTORY` | server → client | `items`: list of `CAPTION_UPDATE` objects | The server's reply to every `CLIENT_HELLO`, possibly empty. Finals only, oldest to newest, nothing the client already reported. |

A caption is always a whole revision, so there is no choice to make about streaming by byte or by word. The only freedom we have is *which* revisions each client gets, and when.

## Components

### `solution_server.py`: naive baseline and required entry point

**What it does.** It is about 150 lines of `asyncio` and `websockets`:

- **Hello handling:** waits up to 5 s for a hello message and closes with code 1008 if it isn't valid.
- **Source path:** forwards every `CAPTION_UPDATE` to every connected client with one `asyncio.gather` per message. It doesn't validate, store or coalesce anything, and it ignores `SOURCE_DONE`.
- **Client path:** replies with an empty `HISTORY`, adds the client to a global set, and reads until it disconnects.
- **Server setup:** binds `0.0.0.0` with `max_size=2**20`, `ping_interval=20` and `ping_timeout=20`.

**What it deliberately leaves out** (its own docstring): revision state and deduplication, history and recovery, bounded per-client queues, slow-client isolation, semantic coalescing, and bandwidth handling.

**Why it fails.** The source handler awaits every client's send. Once one client's socket buffers fill, ingestion stalls for everyone. Until then the backlog sits invisibly in kernel buffers. Measured on T3: normal clients stayed fresh, but slow clients were 7.2 s behind at the median after only 30 s (see [Measured baselines](#measured-baselines)).

**What we keep.** The evaluator command, `python solution_server.py --port <int>`, which must start without input and listen within 10 s. Binding `0.0.0.0` is recommended. The file becomes our launcher for the Go binary.

### `caption_source.py`: deterministic producer

**What it does.** It connects as the source, sends `SOURCE_HELLO`, then for each segment sends its revisions and finishes with `SOURCE_DONE`. It never reads anything from the server.

- Each sentence becomes up to 3 revisions: the first third of its words, the first two-thirds, then the whole sentence (`final=true`). A sentence under 4 words becomes a single final-only revision, which is a concrete reason not to assume 3 revisions.
- `source_ts` is `time.time()` at the moment of sending, so caption age is measured on the wall clock of the same machine.
- With default timing a segment takes 0.75 s: **4 messages/s, of which 1.33/s are finals.**

| Flag | Default | Meaning |
| --- | --- | --- |
| `--uri` | `ws://127.0.0.1:8765` | Server address |
| `--session` | `L1` | Session id |
| `--transcript` | built-in 10 sentences | File with one sentence per line (`#` lines skipped) |
| `--segments` | none | Send exactly this many segments, cycling the sentences (used by `run_demo.py`) |
| `--duration` | none | Keep cycling for about this many seconds (use 120 for the browser demo) |
| `--repeat` | 1 | Transcript cycles when neither of the above is given |
| `--revision-delay` | 0.20 s | Gap between revisions of one segment |
| `--segment-gap` | 0.35 s | Gap after a final before the next segment |
| `--quiet` | off | Don't print each event |

**For us:** turning `--revision-delay` down is the easy way to make bursty or high-rate workloads for our own stress tests. The hidden evaluator varies "caption revision rate and burstiness".

### `load_clients.py`: simulated students

**What it does.** It runs N WebSocket clients in one Python process and logs every event to a CSV that `analyse_results.py` reads.

- **Connection:** `websockets.connect(uri, max_size=2**20, ping_interval=20, ping_timeout=20)`. The library offers permessage-deflate compression by default and queues at most 16 unread frames before it stops reading the socket.
- **Hello:** each connection sends `CLIENT_HELLO` with the newest final it holds and `--history-seconds`.
- **Receiving:** polls `recv()` with a timeout of at most 1 s so it can check its schedule. It only understands `CAPTION_UPDATE` and `HISTORY`; any other message type is silently ignored, so protocol extensions are invisible to the harness.
- **Bookkeeping:** tracks the highest revision accepted per segment, shared across live updates and `HISTORY`, and advances its resume point only on accepted finals.

**Roles.** Client ids are shuffled with `--seed`, then split into disjoint groups: slow first, then disconnecting, then late.

| Role | Behaviour |
| --- | --- |
| Slow | Sleeps `--slow-ms` after every `CAPTION_UPDATE` it reads, and once after any non-empty `HISTORY` message. At 500 ms it reads 2 messages/s, half the source rate. |
| Disconnecting | Once, at `--disconnect-at` seconds after the global start, closes cleanly, sleeps `--disconnect-seconds`, then reconnects with its resume state. |
| Late | Starts `--late-seconds` (plus up to 0.25 s of jitter) after the others. |

Every client stops at the same global end time (`start + --duration`). On any connection exception it logs `CONNECT_ERROR`, waits 0.4 s and reconnects.

**How each received caption is classified** (the `status` column):

| Status | Meaning |
| --- | --- |
| `accepted` | Live update with a revision newer than any seen for that segment |
| `recovered` | `HISTORY` final that is new to this client |
| `duplicate` | Same revision already seen (live or history) |
| `stale` | Lower revision than one already seen |
| `invalid_history_nonfinal` | A `HISTORY` item with `final=false` |

**CSV columns:** `client_id, slow, disconnecting, late, connection_no, event_type, segment, revision, final, source_ts, receive_ts, latency_ms, status, recovery_ms`. `event_type` is one of `CONNECT`, `FORCED_DISCONNECT`, `CAPTION_UPDATE`, `HISTORY`, `HISTORY_ITEM`, `INVALID_JSON`, `CONNECT_ERROR`.

| Flag | Default | Note |
| --- | --- | --- |
| `--uri` | `ws://127.0.0.1:8765` | |
| `--session` | `L1` | |
| `--clients` | 100 | |
| `--duration` | 60 s | |
| `--output` | `results.csv` | |
| `--seed` | 3103 | Fixes role assignment |
| `--history-seconds` | 30 | Must be 0 to 60 |
| `--slow-percent`, `--slow-ms` | 0, 500 | |
| `--disconnect-percent`, `--disconnect-at`, `--disconnect-seconds` | 0, 30, 5 | `run_demo.py` overrides `--disconnect-at` to 10 by default |
| `--late-percent`, `--late-seconds` | 0, 10 | `run_demo.py` overrides `--late-seconds` to 5 by default |

**For us:** the "slow" client is slow at the application layer. TCP and the library's 16-frame queue absorb a lot before our server feels any backpressure, which is why the design uses a ping/pong acknowledgement clock rather than waiting for writes to block.

### `run_demo.py`: one-command test runner

**What it does:** the sequence in [How a public test run fits together](#how-a-public-test-run-fits-together). It passes through every `load_clients.py` role flag and reports medians across runs for: P50, P95 and P99 caption age; fresh caption ratio; fresh final ratio; final completeness; normal update coverage; normal-client P95; slow-client P95; and recovery P95.

| Flag | Default | Note |
| --- | --- | --- |
| `--clients`, `--duration` | 20, 20 s | Duration must exceed 2 s |
| `--runs` (alias `--repeat`) | 1 | Use 3 for reported results; the server restarts each run |
| `--port` | 8765 | |
| `--output-stem` | `demo_results` | Files are `<stem>.csv/json`, or `<stem>_runNN.csv/json` when `--runs > 1` |
| `--source-segments` | computed | Overrides the formula above |
| `--history-seconds` | 30 | |
| `--fresh-threshold-ms` | 500 | |
| `--seed` | 3103 | |
| role flags | as above | `--disconnect-at 10`, `--late-seconds 5` by default |
| `--source-start-delay` | 1.0 s | |
| `--quiet-server` | off | Hides server output |

**For us:** it tells the analyser to expect `segments × 3` updates. Results are written beside `run_demo.py`, so add `*.csv` and `*.json` in the harness folder to `.gitignore`. Late joiners are not part of T1 to T5, but `--late-percent` lets us test them.

### `analyse_results.py`: the metrics

**What it does.** It reads one CSV and computes the summary. It can also run standalone: `python analyse_results.py results.csv --expected-finals 49 --expected-updates 147 --json-output out.json`. Percentiles use linear interpolation.

| Metric | Computed from | What moves it |
| --- | --- | --- |
| P50 / P95 / P99 caption age | `receive_ts - source_ts` of every **accepted live** `CAPTION_UPDATE`, all clients, provisionals and finals | Stale messages sitting in buffers. Not sending obsolete revisions improves it. History items, duplicates and stale rows are excluded. |
| Fresh Caption Ratio | Same rows, share at or under the threshold (500 ms) | Same as above |
| Fresh Final Caption Ratio | Per (client, segment), the earliest accepted or recovered final; share at or under the threshold, divided by `segments × clients` | Getting finals out first. Finals recovered after a disconnect are rarely fresh. |
| Final completeness | Unique finals per client, live or recovered, divided by `segments × clients` | The correctness gate: below 99% caps hidden performance at 15/30, below 95% at 5/30. |
| Normal Update Coverage | Unique (segment, revision) pairs per client from **live** updates only, divided by `segments × 3 × clients` | Conflation lowers it, so it must only happen under real lag. Slow clients are included in the public number. |
| Slow-reader isolation | Caption age stats split by the `slow` flag: P50, P95 and fresh share for normal and slow clients | A slow client must not affect normal ones. |
| Recovery P50 / P95 | `HISTORY` receive time minus connect time, only for reconnects (`connection_no > 1`) and late clients | Building and sending `HISTORY` quickly after a reconnect. The analyser itself warns that a fast empty `HISTORY` is not correct recovery. |
| Duplicates, stale, invalid history | Status counts | Must be 0. |
| Connection errors | `CONNECT_ERROR` rows | Includes connections our server closes on purpose. |

### `submission_check.py`: compatibility check

**What it does.**

1. Checks, relative to the **current working directory**, that `solution_server.py`, `requirements.txt`, `client/index.html` and `client/app.js` exist. With `--final` it also requires `README.md`, `AI_USAGE.md`, `CONTRIBUTIONS.md` and `report.pdf`.
2. Starts `python -u solution_server.py --port 9876` (`--port` overrides the port).
3. Within 10 s, opens a real WebSocket, sends a `CLIENT_HELLO`, and waits up to 0.4 s for any reply.
4. Prints PASS, then stops the server with SIGTERM.

It does not test correctness or performance. Run it from the repo root (`python harness/submission_check.py` after the reorganization) so the file checks look in the right place.

### `monitor_server.py`: memory sampler

**What it does.** Linux only. It scans `/proc` for the first process whose command line contains `--match` (default `solution_server.py`), or takes `--pid`. It prints that process's `VmRSS` every `--interval` (1 s) for `--duration` (60 s) and writes `elapsed_s, rss_mb` to `--output` (`memory_rss.csv`). PUBLIC_TESTS suggests running it alongside T3 to show memory stays bounded.

**For us:** see gotcha 2 about keeping `solution_server.py` in our process's command line. Go's own memory stats in our per-second log line complement it.

### `network_profiles/`: impairment presets

Every script takes the interface as `$1` (default `lo`), calls `sudo` itself, and **replaces** any existing root qdisc, so profiles don't stack. On loopback every packet, data and ACKs alike, passes through the qdisc, so a delay applies in both directions: round-trip time is about twice the configured delay.

| Script | `tc` setting | Effect on loopback |
| --- | --- | --- |
| `delay_100ms.sh` | `netem delay 100ms 20ms distribution normal` | About 200 ms RTT with jitter |
| `delay_loss.sh` | `netem delay 120ms 30ms distribution normal loss 3%` | About 240 ms RTT plus 3% packet loss. A lost segment is resent by fast retransmit (about one extra RTT) or, for sparse small messages that trigger too few duplicate ACKs, by a tail-loss probe or retransmission timeout (Linux minimum RTO 200 ms). Meanwhile TCP holds back everything behind the lost segment (head-of-line blocking). That is why P95 and P99 jump while every message still arrives. |
| `loss_3pct.sh` | `netem loss 3%` | Loss alone: same retransmission and head-of-line effect, no added delay |
| `reorder.sh` | `netem delay 20ms reorder 20% 50%` | 20% of packets (50% correlated) skip the 20 ms delay, so packets arrive out of order. TCP restores order, at a latency cost. |
| `bandwidth_2mbit.sh` | `tbf rate 2mbit burst 32kbit latency 400ms` | One shared 250 KB/s pipe for all loopback traffic. Packets wait up to 400 ms and are then dropped. Segments over about 4 KB are always dropped (gotcha 1). |
| `reset.sh` | deletes the root qdisc | **Always run it after an experiment.** Impairment on `lo` affects everything on the machine. |

To combine delay and a bandwidth cap, write a custom command (for example `netem delay 50ms rate 2mbit`) rather than running two scripts. These profiles are examples; the hidden workload is not disclosed.

### `client/`: naive browser UI

- **`index.html`:** a connection status line, a `#live` caption area, three buttons (`data-seconds` 30, 15 and 60) and a `#recall` panel.
- **`app.js`:** hard-codes `ws://127.0.0.1:8765`. It makes a random 6-digit `client_id` stored in `sessionStorage` (per tab), always sends `last_segment: 0`, appends every revision as a new line, only logs `HISTORY` to the console, has no reconnect, and the recall buttons print a TODO.

The graded TODOs listed in the file: replace revisions instead of appending; keep at least 60 s of finalized captions locally; a 30 s default recall view with 15/30/60 s options; automatic reconnect with correct resume state; `HISTORY` merging and deduplication; and a usable UI.

`DEMO_GUIDE.md` serves this folder with `python -m http.server 8000`. We plan to serve it from the Go server on the same port and derive the WebSocket URL from `location`, which removes the hard-coded address.

### `requirements.txt` and `sample_transcript.txt`

- `requirements.txt`: `websockets>=13,<17` (we tested 16.1.1). After the rewrite our server no longer needs it, but the harness does, so it stays.
- `sample_transcript.txt`: the same 10 sentences as `caption_source.py`'s defaults, all 9 or more words long, so every public segment has exactly 3 revisions.

### Guides, templates and bonus

| File | What it covers |
| --- | --- |
| `START_HERE.md` | What the kit provides, what is deliberately unsolved, and quick-start commands |
| `PUBLIC_TESTS.md` | Exact T1 to T5 commands, what to look for in each, and the required before/after comparison |
| `DEMO_GUIDE.md` | The three-terminal browser demo and the checklist it must show |
| `PROTOCOL_EXAMPLES.md` | Message sequences for normal connection, reconnect and late join |
| `NETWORKING_PRIMER.md` | TCP under WebSocket, asyncio, per-client queues, backpressure, bounded state, semantic coalescing |
| `UBUNTU_SETUP.md` | Packages, a virtual environment, and the optional Multipass VM |
| `FILES.md` | One-line manifest of the kit |
| `README_TEMPLATE.md` | Sections our README must have: setup, server, browser, tests, demo, design notes |
| `AI_USAGE_TEMPLATE.md` | Tools used, one useful suggestion, one wrong suggestion and its fix, responsibility statement |
| `CONTRIBUTIONS_TEMPLATE.md` | Per-member responsibilities and agreed percentages, totalling 100% |
| `bonus_asr/README.md` | Optional ASR bonus: must use the same server and client, with dependencies kept out of `requirements.txt` |

## Measured baselines

These are reference numbers for the provided naive server, from one run each in a cloud container. They are not report-grade; rerun them on our VM with `--runs 3`.

| Setup | Result |
| --- | --- |
| T3-style: 100 clients, 10% slow at 500 ms, 30 s | Normal clients P95 8.8 ms, 100% fresh. Slow clients P50 7.2 s, 3.45% fresh. Completeness 95.28%. |
| `bandwidth_2mbit.sh`, 100 clients, 30 s | P99 39 ms, 100% fresh, 100% complete: the pipe is not saturated |
| `bandwidth_2mbit.sh`, 400 clients, 30 s | P50 805 ms, 0.6% fresh, completeness 97.81% (below the 99% gate), 9,597 packets dropped by `tbf` |

At 400 clients the Python harness itself may add some delay, so compare our server against the baseline on the same machine rather than against these numbers.
