# Team 04 - LiveRecall

## Team members

- Ooi Zheng Hao / A0308283A / Frontend Client
- Achmad Firdaus Assabil / XXXXX / XXXXX
- William Jeffery Liow / XXXXX / XXXXX
- Leifsen Law / A0307362J / Session, Source and Routing

## Ubuntu setup

LiveRecall is developed and tested on Ubuntu 24.04 LTS. Use Ubuntu for the network impairment tests, and run the backend and simulated clients inside the same Ubuntu environment when applying network profiles to loopback (`lo`).

| Component | Requirement |
| --- | --- |
| Operating system | Ubuntu 24.04 LTS, installed directly or in a VM |
| Python | Python 3.12, as used by the project CI |
| Go | Go 1.27 or newer, as required by `go.mod` |
| Architecture | Linux `amd64` (x86-64) or `arm64`, supported by the server launcher |
| Browser | A current browser with WebSocket and JavaScript module support |

Go is needed to build the backend and run Go tests. The browser client uses plain JavaScript and does not need an npm build step. Audio transcription packages are optional and are not needed for the deterministic application.

### Ubuntu packages

```bash
sudo apt update && sudo apt upgrade -y
sudo apt install -y \
  git curl ca-certificates build-essential \
  python3 python3-venv python3-pip \
  iproute2 kmod
```

`build-essential` supplies `make` and the C compiler needed by Go's race detector. `iproute2` supplies the `tc` command used for network impairment tests.

### Go

Check the existing installation first:

```bash
go version
```

If Go 1.27 or newer is already available, continue to [Install](#install). Otherwise, the following installs Go 1.27.3 under your user account, selecting the archive for your Ubuntu architecture. This version is available from the [official Go downloads](https://go.dev/dl/); see the [Go installation instructions](https://go.dev/doc/install) for system-wide installation options.

```bash
LIVERECALL_GO_VERSION=1.27.3
LIVERECALL_GO_ARCH="$(dpkg --print-architecture)"
LIVERECALL_GO_ARCHIVE="go${LIVERECALL_GO_VERSION}.linux-${LIVERECALL_GO_ARCH}.tar.gz"

curl --fail --location \
  --output "/tmp/${LIVERECALL_GO_ARCHIVE}" \
  "https://go.dev/dl/${LIVERECALL_GO_ARCHIVE}"

mkdir -p "$HOME/.local/lib/go-${LIVERECALL_GO_VERSION}"
tar -C "$HOME/.local/lib/go-${LIVERECALL_GO_VERSION}" \
  -xzf "/tmp/${LIVERECALL_GO_ARCHIVE}"

export PATH="$HOME/.local/lib/go-${LIVERECALL_GO_VERSION}/go/bin:$PATH"
go version
```

To make this installation available in new Bash terminals, add its path once:

```bash
printf '\nexport PATH="$HOME/.local/lib/go-1.27.3/go/bin:$PATH"\n' >> "$HOME/.bashrc"
```

## Install

Clone or extract the repository, then open a terminal in its root directory, which contains `solution_server.py`, `go.mod`, and `requirements.txt`. Run all remaining commands from this root directory.

```bash
python3 -m venv venv
source venv/bin/activate
python3 -m pip install -r requirements.txt
make build
```

The requirements file installs the Python WebSocket dependency; the Go backend resolves its own dependencies during the build. `make build` creates `bin/distributary-linux-amd64` or `bin/distributary-linux-arm64` for the current machine, and `solution_server.py` launches that compiled binary. Run `make build` again after changing Go code, or `make bins` to build binaries for both supported architectures.

Activate the virtual environment with `source venv/bin/activate` in each new terminal used for Python commands.

Confirm the toolchain:

```bash
python3 --version
go version
make --version
tc -V
python3 -m pip check
```

## Required server

```bash
python3 solution_server.py --host 127.0.0.1 --port 8765 --mode live
```

Live mode is the default. The server accepts caption sources and browser clients over WebSocket, and serves the browser files over HTTP on the same port. Stop it with Ctrl+C.

To connect from other devices, bind to `0.0.0.0` and open `http://<server-ip>:8765/` from that device.

## Browser client

After starting the server above, open **http://127.0.0.1:8765/**. Open additional tabs to test independent clients.

The WebSocket address follows the page's host and port (default 8765). If the frontend is hosted separately, for example on port 8000, open `http://<frontend-host>:8000/?serverPort=8765` when both services use the same hostname, or supply a full `?server=ws://<backend-host>:8765` URL.

## Public deterministic tests

Before running tests, **stop any manually started backend and source**.

From the repository root, with the virtual environment active, run the build and protocol checks:

```bash
make test
make check
```

`make test` runs Go vet and the Go tests with the race detector. `make check` builds the server and checks the required launcher and WebSocket handshake on port 9876; it does not measure caption correctness or performance. Both need permission to open local TCP sockets.

Before the performance runs, confirm that loopback is available and load the network emulation module:

```bash
ip link show lo
sudo modprobe sch_netem
```

The Ubuntu kernel must provide `sch_netem`; if it is unavailable in a restricted container or VM, use an Ubuntu environment whose kernel supports it.

Run T1-T5 with three independent runs each:

```bash
python3 experiments/run/perf.py t1 t2 t3 t4 --runs 3
python3 experiments/run/perf.py t5 --runs 3
```

T5 requests sudo permission to apply the loopback network profile, and the runner resets the profile afterwards. If a run is interrupted and leaves a profile applied, reset it before the next test:

```bash
sudo bash harness/network_profiles/reset.sh lo
```

The runner records summaries in `experiments/results/perf_log.csv` and raw runs in `experiments/results/runs/`. Further test details will be documented in [Public tests](docs/developer_docs/PUBLIC_TESTS.md).

## Mandatory deterministic application demo

Start the backend as in [Required server](#required-server) and open two or three browser tabs. Then, in a second terminal from the repository root:

```bash
source venv/bin/activate
python3 harness/caption_source.py \
  --uri ws://127.0.0.1:8765 \
  --transcript demo_transcript.txt \
  --duration 120 \
  --revision-delay 0.5 \
  --segment-gap 1
```

1. Watch provisional captions update in place and become finalized.
2. Click **What did I miss?** and select 15, 30, or 60 seconds. Captions are newest first; use its Refresh button to update the snapshot.
3. Open another tab after captions have started to demonstrate late-join history.
4. Use **Simulate temporary disconnection** in the testing taskbar. After the chosen duration, the same client reconnects and recovers recent missed finals.
5. Use **Refresh client session** and confirm to clear saved client state and reconnect with a new ID and zeroed resume position.

Stop both terminal processes with Ctrl+C when finished.

> For a new caption run, **restart both the backend and the source**, then use **Refresh client session** in existing tabs. Restarting only the source reuses segment numbers that the running backend may already have finalized.

## Design notes

- **Protocol:** `internal/protocol` defines the message shapes and handles JSON encoding, decoding, and field validation.
- **Revision state:** each segment retains its newest provisional revision; finalized segments cannot be overwritten by later updates.
- **Bounded history:** the backend retains up to 1024 finals over a 90-second source-time window and up to 16 active segments. The browser keeps a 60-second window in per-tab session storage.
- **Recovery:** clients report their newest finalized position in `CLIENT_HELLO`. The server sends missed finals within the requested history window before live updates.
- **Client isolation:** each client has its own reader, writer, and cursor over shared caption state. A four-message flow-control window uses WebSocket ping/pong acknowledgements.
- **Overload:** slower clients receive fewer provisional revisions while finals retain priority. Clients behind retained history are disconnected and recover through the recent history window on reconnect.

## Documentation

- [Developer documentation](docs/developer_docs/): current implementation notes in the [Developer Guide](docs/developer_docs/DeveloperGuide.md) and [protocol specification](docs/developer_docs/protocol_specification.md).
- [Initial development notes](docs/initial_development_docs_for_info/): early plans and reference material, some of which is outdated.
- [Provided templates](docs/provided_docs/): original assignment material retained for reference.
