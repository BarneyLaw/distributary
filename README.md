# Team 04 - LiveRecall

## Team members

- Ooi Zheng Hao / A0308283A / Frontend Client
- Achmad Firdaus Assabil / XXXXX / XXXXX
- William Jeffery Liow / XXXXX / XXXXX
- Leifsen Law / XXXXX / XXXXX

## Ubuntu setup

Use Ubuntu Linux for the network impairment tests. The development environment uses Ubuntu 24.04, Python 3.12, and Go 1.27; Go 1.27 or newer is required to build the server.

Install Go separately, then install the Ubuntu packages:

```bash
sudo apt update
sudo apt install -y python3 python3-venv python3-pip make iproute2
```

See the [Ubuntu setup guide](docs/developer_docs/UBUNTU_SETUP.md) for installation, verification, and first-run instructions.

## Install

Run from the repository root:

```bash
python3 -m venv venv
source venv/bin/activate
pip install -r requirements.txt
make build
```

Activate the virtual environment in each terminal used for Python commands. Run `make build` again after changing Go code; `solution_server.py` launches the compiled binary in `bin/`.

## Required server

```bash
python3 solution_server.py --host 127.0.0.1 --port 8765
```

Live mode is the default. The server accepts caption sources and browser clients over WebSocket, and serves the browser files over HTTP on the same port. To connect from other devices, bind to `0.0.0.0` and use the server's IP address in the browser.

## Browser client

After starting the server above, simply open **http://127.0.0.1:8765/**. 

*Note: the WebSocket address follows the page's host and port, defult: 8756.*

## Public deterministic tests

Before running tests, **Stop any manually started backend and source**

From the repository root, with the virtual environment active:

```bash
make test
make check
python3 experiments/run/perf.py t1 t2 t3 t4 --runs 3
python3 experiments/run/perf.py t5 --runs 3
```

T5 requires sudo for the loopback network profile; the runner applies and resets it. Further test details will be documented in [Public tests](docs/developer_docs/PUBLIC_TESTS.md).

## Mandatory deterministic application demo

Start the backend as above, open two or three browser tabs, then run this in another terminal:

```bash
python3 harness/caption_source.py \
  --uri ws://127.0.0.1:8765 \
  --transcript demo_transcript.txt \
  --duration 120 \
  --revision-delay 0.5 \
  --segment-gap 1
```

1. Watch provisional captions update in place and become finalized.
2. Click on **What did I miss?** and select 15, 30, or 60 seconds. Captions are newest first; use its Refresh button to update the snapshot.
3. Open another tab after captions have started to demonstrate late-join history.
4. Use **Simulate temporary disconnection** in the testing taskbar. After the chosen duration, the same client reconnects and recovers recent missed finals.
5. Use **Refresh client session** and confirm to clear saved client state and reconnect with a new ID and zeroed resume position.


> For a new test run, **restart the server**, then refresh the client session. 

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
