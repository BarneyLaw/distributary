# Naive Baseline

The `naive` branch ports the professor's starter server to Go, unchanged in behaviour, so it can serve as the "before" in the report's required before/after comparison. It also sets up the plumbing the real server will reuse: the Python launcher, prebuilt binaries, the harness folder, the `Makefile` and CI.

## What it does

`internal/naive` mirrors `harness/starter_server.py`:

- Every `CAPTION_UPDATE` from the source is forwarded, byte for byte, to every connected client.
- The source handler waits until **all** of those sends finish before reading the next update (`sync.WaitGroup`, standing in for `asyncio.gather`). One stalled client therefore delays everybody once its socket buffers fill.
- Every `CLIENT_HELLO` gets an empty `HISTORY`. There is no history, revision state, deduplication, per-client isolation or backpressure.
- The connection settings match the starter's: 1 MiB message limit, 5 s to send a hello, a ping every 20 s, and 20 s to answer it. A bad hello is closed with code 1008.

It differs from the starter in two small ways, both documented in the package comment:

- Captions that fail `protocol.DecodeSourceMessage` are dropped instead of forwarded.
- It does not negotiate permessage-deflate compression. The starter's Python library does by default, so under a bandwidth limit this port sends somewhat more bytes than the starter.

`cmd/distributary` selects the implementation with `--mode` (only `naive` exists so far, so it is the default). When the real server lands, it becomes the default and `--mode=naive` stays available for comparisons.

## Does it really match the starter?

Same machine, one run each. The two are effectively identical, which is what we want from a baseline.

| Test | Metric | Python starter | Go naive |
| --- | --- | --- | --- |
| T1 (20 clients, 20 s) | P50 / P99 caption age | 2.25 / 6.47 ms | 1.94 / 4.19 ms |
| T1 | Completeness, coverage, fresh | 100%, 100%, 100% | 100%, 100%, 100% |
| T3 (100 clients, 10% slow at 500 ms, 60 s) | Normal clients P95 | 10.60 ms | 9.66 ms |
| T3 | Slow clients P50 / P95 | 14.72 s / 28.09 s | 14.75 s / 28.09 s |
| T3 | Slow clients fresh | 1.69% | 1.69% |
| T3 | Final completeness | 95.13% | 95.13% |

T3 shows the problem the real server has to solve. Slow clients end up almost 30 s behind. Completeness sits just above 95%, the threshold below which hidden performance is capped at 5/30.

These numbers come from a cloud container. For the report, rerun both with `--runs 3` on our Ubuntu VM: `make t3` and `make py-baseline`.

## Running it

```sh
make test            # go vet + unit tests (protocol and naive) with -race
make build           # bin/distributary-linux-<arch> for this machine
make check           # professor's submission_check.py through the launcher
make t1              # public T1, 3 runs, results in experiments/results/
make t3              # public T3
make py-baseline     # the original Python starter on the same harness (T3 by default)
make py-baseline ARGS="--clients 20 --duration 20 --runs 3"   # any other test
make verify-harness  # SHA-256 check that harness/ is still the professor's code
```

`python solution_server.py --port 8765` still works from the root, and `run_demo.py` works from `harness/`.

## What changed in the repo

| Change | Why |
| --- | --- |
| `solution_server.py` is now a launcher that `exec`s `bin/distributary-linux-<arch>` | The evaluator only runs this command. `exec` (not a child process) means SIGTERM from the harness reaches Go directly and the port is freed. `argv[0]` stays `solution_server.py` so `monitor_server.py` can find the process. `realpath` makes it work through the harness symlink. |
| The original server moved to `harness/starter_server.py` | Kept unmodified for `make py-baseline` |
| `harness/solution_server.py` symlink to `../solution_server.py` | `run_demo.py` requires the server beside it |
| `harness/SHA256SUMS` | Proves the professor's files are unmodified (`make verify-harness`, also run in CI) |
| Executable bit restored on `harness/network_profiles/*.sh` | `PUBLIC_TESTS.md` runs them as `sudo ./network_profiles/...` |
| `go.mod` requires `gorilla/websocket v1.5.3`; empty `vendor/` removed | The only dependency. `go.sum` pins its exact contents, and Go downloads it on first build. Not vendored: the evaluator runs the committed binaries and never builds, and an out-of-date `vendor/` breaks every build with "inconsistent vendoring". |
| Removed empty `internal/doc.go` | An empty `.go` file breaks `go build ./...` |
| `Makefile`, `.github/workflows/ci.yaml`, `.gitignore` filled in | Build, test, check and experiment shortcuts; CI runs gofmt, vet, race tests, both builds, the harness checksum and `submission_check.py` |

## Before merging

- **Build the committed binaries with our Go 1.27 toolchain:** run `make bins` and commit `bin/`. They are not included in this branch. The ones used for the numbers above were built with Go 1.24 in a cloud container, because the Go 1.27 toolchain download was blocked there.
- `go.sum` was generated with Go 1.24. Run `go mod tidy` once with Go 1.27 and commit any diff (there should be none).
