# Ubuntu Setup

This guide sets up LiveRecall on Ubuntu 24.04 LTS for local development, the deterministic caption demo, and network tests. Run the backend and simulated clients inside the same Ubuntu environment when applying network profiles to loopback (`lo`).

## Requirements

| Component | Requirement |
| --- | --- |
| Operating system | Ubuntu 24.04 LTS, installed directly or in a VM |
| Python | Python 3.12, as used by the project CI |
| Go | Go 1.27 or newer, as required by `go.mod` |
| Architecture | Linux `amd64` (x86-64) or `arm64`, supported by the server launcher |
| Browser | A current browser with WebSocket and JavaScript module support |

Go is needed to build the backend and run Go tests. The browser client uses plain JavaScript and does not need an npm build step. Audio transcription packages are optional and are not needed for the deterministic application.

## Install Ubuntu packages

```bash
sudo apt update
sudo apt install -y \
  git curl ca-certificates build-essential \
  python3 python3-venv python3-pip \
  iproute2 kmod
```

`build-essential` supplies `make` and the C compiler needed by Go's race detector. `iproute2` supplies the `tc` command used for network impairment tests.

## Install Go

Check the existing installation first:

```bash
go version
```

If Go 1.27 or newer is already available, continue to the next section. Otherwise, the following installs Go 1.27.3 under your user account, selecting the archive for your Ubuntu architecture. This version is available from the [official Go downloads](https://go.dev/dl/); see the [Go installation instructions](https://go.dev/doc/install) for system-wide installation options.

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

## Prepare the project

Clone or extract the repository, then open a terminal in its root directory, which contains `solution_server.py`, `go.mod`, and `requirements.txt`. Run the remaining project commands from this directory.

Create and activate a Python virtual environment:

```bash
python3 -m venv venv
source venv/bin/activate
python3 -m pip install -r requirements.txt
```

The requirements file installs the Python WebSocket dependency. Install it inside the virtual environment; the Go backend resolves its own dependencies during the build.

Build the backend:

```bash
make build
```

This creates `bin/distributary-linux-amd64` or `bin/distributary-linux-arm64` for the current machine. The Python launcher runs this compiled binary, so run `make build` again after changing Go code. Use `make bins` when preparing binaries for both supported architectures.

In each new terminal used for Python commands, activate the same environment with `source venv/bin/activate`.

## Check the installation

```bash
python3 --version
go version
make --version
tc -V
python3 -m pip check
make test
make check
```

`make test` runs Go vet and the Go tests with the race detector. `make check` builds the server and checks the required launcher and WebSocket handshake on port 9876; it does not measure caption correctness or performance. These checks need permission to open local TCP sockets.

## Run the application

In the first terminal, with the virtual environment active:

```bash
python3 solution_server.py --host 127.0.0.1 --port 8765 --mode live
```

Open **http://127.0.0.1:8765/** in a browser. The backend serves the frontend files and WebSocket connections on the same port.

In a second terminal, from the repository root:

```bash
source venv/bin/activate
python3 harness/caption_source.py \
  --uri ws://127.0.0.1:8765 \
  --transcript demo_transcript.txt \
  --duration 120 \
  --revision-delay 0.5 \
  --segment-gap 1
```

Open additional tabs to test independent clients. Use **What did I miss?** for recent finalized captions and the testing taskbar for client resets and temporary disconnections.

Stop both terminal processes with Ctrl+C when finished. For a new caption run, restart the backend and source, then use **Refresh client session** in existing tabs. Restarting only the source reuses segment numbers that the running backend may already have finalized.

For access from another device, bind the backend to `0.0.0.0` and open `http://<server-ip>:8765/` from that device. For separate frontend hosting on port 8000, open `http://<frontend-host>:8000/?serverPort=8765` when both services use the same hostname, or supply a full `?server=ws://<backend-host>:8765` URL.

## Prepare network tests

Stop the manually started backend and source before running the performance runner. Confirm that loopback is available and load the network emulation module:

```bash
ip link show lo
sudo modprobe sch_netem
```

The Ubuntu kernel must provide `sch_netem`; if it is unavailable in a restricted container or VM, use an Ubuntu environment whose kernel supports it.

Run the tests from the repository root with the virtual environment active:

```bash
python3 experiments/run/perf.py t1 t2 t3 t4 --runs 3
python3 experiments/run/perf.py t5 --runs 3
```

T5 requests sudo permission to apply the loopback network profile. The runner resets the profile afterwards. If a run is interrupted and leaves a profile applied, reset it before the next test:

```bash
sudo bash harness/network_profiles/reset.sh lo
```

The runner records summaries in `experiments/results/perf_log.csv` and raw runs in `experiments/results/runs/`.