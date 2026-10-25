#!/usr/bin/env python3
"""Required evaluator entry point: python solution_server.py --port 8765

This file only starts the prebuilt Go server in bin/. It replaces its own
process with the binary (os.execv) rather than starting a child, because:

  - run_demo.py and submission_check.py stop the server with SIGTERM. With a
    child process, the signal would only reach Python and the Go server would
    keep the port, so the next run could not bind.
  - argv[0] stays "solution_server.py", so monitor_server.py, which finds the
    server by searching process command lines for that name, still finds it.

realpath() matters because harness/solution_server.py is a symlink to this
file, and bin/ lives next to the real file, not next to the symlink.

The original Python starter server is kept at harness/starter_server.py.
"""

import os
import platform
import sys

ARCH = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}


def main():
    if platform.system() != "Linux":
        sys.exit("solution_server.py: the server binaries are built for Linux only")

    machine = platform.machine().lower()
    arch = ARCH.get(machine)
    if arch is None:
        sys.exit("solution_server.py: unsupported CPU architecture %r" % machine)

    here = os.path.dirname(os.path.realpath(__file__))
    binary = os.path.join(here, "bin", "distributary-linux-" + arch)
    if not os.path.isfile(binary):
        sys.exit("solution_server.py: %s not found; build it with `make bins`" % binary)
    if not os.access(binary, os.X_OK):
        os.chmod(binary, 0o755)  # zip extraction can drop the executable bit

    sys.stdout.flush()
    os.execv(binary, ["solution_server.py"] + sys.argv[1:])


if __name__ == "__main__":
    main()