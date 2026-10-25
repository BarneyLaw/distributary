# Project Structure

Proposed layout for `distributary`: a Go server behind the required Python entry point, the professor's harness kept unmodified in its own folder, and everything else under `docs/` and `experiments/`.

## Constraints that shape the layout

1. **The root is fixed by the evaluator.** `python solution_server.py --port 8765` must work from the root, and `submission_check.py` expects `solution_server.py`, `requirements.txt`, `client/index.html` and `client/app.js` there. At submission the root also needs `README.md`, `AI_USAGE.md`, `CONTRIBUTIONS.md` and `report.pdf`.
2. **Harness files stay byte-for-byte unmodified.** `run_demo.py` only works with `solution_server.py`, `load_clients.py`, `caption_source.py` and `analyse_results.py` in its own folder, so the harness folder gets a symlink to the root launcher.
3. **The evaluation VM may have no Go toolchain.** Prebuilt static binaries are committed in `bin/`, and the launcher only `exec`s one.

## Layout

```
distributary/
├── solution_server.py        # launcher: exec bin/distributary-linux-<arch>
├── requirements.txt          # websockets (harness only)
├── go.mod  go.sum            # Go 1.27; go.sum pins dependencies (no vendor/: the evaluator never builds)
├── Makefile                  # build, bins, test, check, experiment shortcuts
├── web.go                    # //go:embed client: browser files baked into the binary
│
├── cmd/distributary/         # main: flags, listener (MSS clamp), HTTP + WebSocket routing
├── internal/
│   ├── protocol/             # message structs, decode + validate, compact encode
│   ├── session/              # shared caption state: finals ring, active segments, accept rules
│   ├── fanout/               # one connection per client + its window state, writer/reader loops
│   └── stats/                # per-second stats line, optional pprof
├── bin/                      # distributary-linux-amd64, distributary-linux-arm64 (committed)
├── client/                   # index.html, app.js, style.css (stays at root: required path)
│
├── harness/                  # professor's files, unmodified
│   ├── caption_source.py  load_clients.py  run_demo.py  analyse_results.py
│   ├── submission_check.py  monitor_server.py  sample_transcript.txt
│   ├── network_profiles/
│   ├── solution_server.py -> ../solution_server.py   # symlink, run_demo needs it here
│   └── SHA256SUMS            # proves the files are untouched
├── experiments/
│   ├── profiles/             # our own tc scripts (combined delay + bandwidth, etc.)
│   ├── run/                  # scripts for T1 to T5 and before/after sweeps
│   └── results/              # summaries committed; raw CSV gitignored
├── docs/
│   ├── starter-kit.md        # what each professor-provided file does
│   ├── project-structure.md  # this file
│   ├── design.md             # server design (was Untitled.md)
│   ├── plan.md               # stack and open questions (moved from root)
│   └── starter/              # professor's guides and templates, unmodified
├── .github/workflows/ci.yml  # vet, test -race, build both arches, submission_check
├── README.md  AI_USAGE.md  CONTRIBUTIONS.md  report.pdf  LICENSE
```

## What each Go package owns

| Package | Owns | Must not |
| --- | --- | --- |
| `cmd/distributary` | Flags (defaults = final design), listener with `TCP_MAXSEG` 1400, routing `GET /` to the embedded client and upgrades to `fanout`, SIGTERM shutdown | Contain protocol or delivery logic |
| `internal/protocol` | JSON shapes for the five message types, validation, compact encoding | Know about connections |
| `internal/session` | The master window: bounded finals ring (each final gets an `fseq`), newest provisional per active segment, accept rules, `HISTORY` snapshot | Do any I/O; it is pure and unit-tested |
| `internal/fanout` | Per-connection state and goroutines (below) | Hold its own copy of captions |
| `internal/stats` | Counters and the per-second log line | Block the hot path |

**Per-client window state lives in `fanout`, and it is a handful of integers, not a buffer:**

- `finalCursor`: the last `fseq` sent; pending finals are read straight from the shared ring
- `sentRev`: the last revision sent per active segment
- `sendSeq`, `ackedSeq`: messages sent and messages acknowledged by pong, so in flight = `sendSeq - ackedSeq` ("SEQ - ACK > n" in `plan.md`)
- `mode`, plus smoothed and minimum pong RTT: normal or degraded, with hysteresis

The master ring plus these cursors covers both reconnection and slow readers. A client that falls off the ring's tail is past any useful recovery and gets disconnected, then resumes through `HISTORY`, which covers up to 60 s.

## The launcher

```python
import os, sys, platform
here = os.path.dirname(os.path.realpath(__file__))  # realpath: harness/ reaches us through a symlink
arch = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[platform.machine()]
binary = os.path.join(here, "bin", f"distributary-linux-{arch}")
os.chmod(binary, 0o755)                                 # zip extraction can drop the exec bit
os.execv(binary, ["solution_server.py"] + sys.argv[1:]) # keep the name monitor_server.py matches on
```

- **`realpath`:** `abspath` would resolve to `harness/bin/` when the launcher runs through the symlink.
- **`execv`:** the harness's SIGTERM then reaches the Go process directly, and no orphan keeps the port.
- **`argv[0]`:** keeping `solution_server.py` lets `monitor_server.py` (and possibly the hidden evaluator) find our process.

## Migrating from the current repo

| Now | After |
| --- | --- |
| 26 starter files at the root | Test programs and `network_profiles/` move to `harness/`; guides and templates move to `docs/starter/` |
| `solution_server.py` (naive baseline) | Replaced by the launcher. The naive behaviour survives as `--mode=naive` for the before/after comparison. |
| `client/` | Stays in place and gets rewritten |
| `Untitled.md` | `docs/design.md` |
| `plan.md` | `docs/plan.md` |
| Empty `.gitignore` | Ignore `harness/*.csv`, `harness/*.json`, `experiments/results/raw/`, `__pycache__/`, `venv/`, local Go build output (but not `bin/`) |
| Two branches: `main` (only `LICENSE`) and `master` (all work) | Merge `LICENSE` into `master`, make one of them the default, delete the other |

## Everyday commands

| Command | Does |
| --- | --- |
| `make build` | Builds a local binary for the current machine |
| `make bins` | Cross-compiles both committed binaries (`CGO_ENABLED=0`). Run before any push the evaluator might see. |
| `make test` | `go vet` and `go test -race ./...` |
| `make check` | `python harness/submission_check.py`, from the root |
| `make t1` to `make t5` | The `PUBLIC_TESTS.md` commands with `--runs 3`, results copied into `experiments/results/` |

CI runs `vet`, the tests, both builds and the submission check on every push. Performance tests stay manual, on the Ubuntu VM.
