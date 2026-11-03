#!/usr/bin/env python3
"""Browser end-to-end test: real server, real caption source, real browser client.

What it does (about 45 s):

  1. Starts our server on port 8766 (python solution_server.py --mode live).
  2. Starts a TCP proxy on 8765 -> 8766. The browser client connects to its
     page's host and port, so all browser traffic goes through the proxy, and
     the test can cut it to simulate a network outage.
  3. Opens browser pages A and B at http://127.0.0.1:8765/ (the server serves
     client/ itself), and starts caption_source.py --duration 30 (straight to
     8766, unaffected by the proxy).
  4. At 8 s cuts every browser connection and refuses new ones for 5 s.
     A and B must reconnect on their own and recover through HISTORY.
  5. At 17 s opens page C, a late joiner.
  6. After the source finishes, checks every page.

Checks, from the WebSocket frames each page received:
  - every final from segment 1 to the last arrives exactly once, in order
    (live or through HISTORY): no gap, no duplicate
  - no revision goes backwards, and no provisional arrives after its final
  - A and B received a non-empty HISTORY after reconnecting
and from the page itself:
  - the status shows "connected"
  - "What did I miss?" at 60 s lists every final, newest first

Requirements: pip install playwright && python -m playwright install chromium
Run from the repo root, after `make build`:  python3 experiments/run/browser_e2e.py
Add --headed to watch it.
"""

from __future__ import annotations

import argparse
import json
import socket
import subprocess
import sys
import threading
import time
from pathlib import Path

from playwright.sync_api import sync_playwright

ROOT = Path(__file__).resolve().parents[2]
BROWSER_PORT = 8765  # page and WebSocket traffic enter through this proxy
SERVER_PORT = 8766


class CuttableProxy:
    """Plain TCP proxy whose connections can all be cut at once."""

    def __init__(self, listen_port: int, target_port: int):
        self.target = ("127.0.0.1", target_port)
        self.lock = threading.Lock()
        self.socks: set[socket.socket] = set()
        self.refusing = False
        self.listener = socket.create_server(("127.0.0.1", listen_port), reuse_port=False)
        threading.Thread(target=self._accept, daemon=True).start()

    def _accept(self):
        while True:
            try:
                client, _ = self.listener.accept()
            except OSError:
                return
            with self.lock:
                refusing = self.refusing
            if refusing:
                client.close()
                continue
            try:
                upstream = socket.create_connection(self.target)
            except OSError:
                client.close()
                continue
            with self.lock:
                self.socks |= {client, upstream}
            for a, b in ((client, upstream), (upstream, client)):
                threading.Thread(target=self._pump, args=(a, b), daemon=True).start()

    def _pump(self, src: socket.socket, dst: socket.socket):
        try:
            while data := src.recv(65536):
                dst.sendall(data)
        except OSError:
            pass
        finally:
            for s in (src, dst):
                try:
                    s.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass

    def cut(self):
        with self.lock:
            self.refusing = True
            socks, self.socks = self.socks, set()
        for s in socks:
            try:
                s.shutdown(socket.SHUT_RDWR)
                s.close()
            except OSError:
                pass

    def restore(self):
        with self.lock:
            self.refusing = False

    def close(self):
        self.cut()
        self.listener.close()


def wait_port(port: int, timeout: float = 10.0):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            socket.create_connection(("127.0.0.1", port), timeout=0.3).close()
            return
        except OSError:
            time.sleep(0.1)
    raise SystemExit(f"nothing listening on {port}")


def watch(page, frames: list):
    """Record every WebSocket frame the page receives, across reconnects."""
    page.on("websocket", lambda ws: ws.on("framereceived", lambda payload: frames.append(payload)))


def check_frames(name: str, frames: list, last_segment: int, expect_recovery: bool) -> list[str]:
    problems = []
    finals_in_order: list[int] = []
    rev: dict[int, int] = {}
    final_seen: set[int] = set()
    histories_with_items = 0
    for raw in frames:
        msg = json.loads(raw)
        if msg.get("type") == "HISTORY":
            items = msg.get("items", [])
            histories_with_items += bool(items)
            for it in items:
                if not it.get("final"):
                    problems.append(f"{name}: HISTORY contained a provisional {it['segment']}.{it['revision']}")
        elif msg.get("type") == "CAPTION_UPDATE":
            items = [msg]
        else:
            continue
        for it in items:
            seg, r, final = it["segment"], it["revision"], it.get("final", False)
            if seg in final_seen:
                problems.append(f"{name}: received {seg}.{r} after segment {seg}'s final")
                continue
            if r <= rev.get(seg, 0):
                problems.append(f"{name}: revision {seg}.{r} is not newer than {seg}.{rev[seg]}")
            rev[seg] = r
            if final:
                final_seen.add(seg)
                finals_in_order.append(seg)

    expected = list(range(1, last_segment + 1))
    if finals_in_order != expected:
        missing = sorted(set(expected) - set(finals_in_order))
        problems.append(f"{name}: finals not exactly 1..{last_segment} in order; "
                        f"missing={missing[:10]} got {len(finals_in_order)} finals")
    if expect_recovery and histories_with_items == 0:
        problems.append(f"{name}: no non-empty HISTORY, so the reconnect recovered nothing")
    print(f"  {name}: {len(finals_in_order)} finals, {len(frames)} frames, "
          f"{histories_with_items} non-empty HISTORY")
    return problems


def check_page(name: str, page, sentences: list[str], last_segment: int) -> list[str]:
    problems = []
    status = page.text_content("#status")
    if status != "connected":
        problems.append(f"{name}: status is {status!r}, want 'connected'")

    page.click("#recall-toggle")
    page.click("button[data-seconds='60']")
    texts = page.locator("#recall .recall-text").all_text_contents()
    want = [sentences[(seg - 1) % len(sentences)] for seg in range(last_segment, 0, -1)]
    if texts != want:
        problems.append(f"{name}: 'What did I miss?' 60 s shows {len(texts)} captions, "
                        f"want all {last_segment}, newest first")
    return problems


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--headed", action="store_true", help="show the browser windows")
    ap.add_argument("--mode", default="live")
    args = ap.parse_args()

    sentences = [ln.strip() for ln in (ROOT / "sample_transcript.txt").read_text().splitlines()
                 if ln.strip() and not ln.startswith("#")]
    procs = []
    proxy = None
    try:
        server = subprocess.Popen(
            [sys.executable, "solution_server.py", "--port", str(SERVER_PORT), "--mode", args.mode],
            cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        procs.append(server)
        wait_port(SERVER_PORT)
        proxy = CuttableProxy(BROWSER_PORT, SERVER_PORT)

        with sync_playwright() as p:
            browser = p.chromium.launch(headless=not args.headed)
            pages, frames = {}, {}
            for name in ("A", "B"):
                page = browser.new_context().new_page()
                frames[name] = []
                watch(page, frames[name])
                try:
                    page.goto(f"http://127.0.0.1:{BROWSER_PORT}/")
                    page.wait_for_function("document.querySelector('#status').textContent === 'connected'",
                                           timeout=10_000)
                except Exception as e:
                    raise SystemExit(f"page {name} never connected ({type(e).__name__}). The server must "
                                     f"serve client/ on plain HTTP, which --mode naive does not.")
                pages[name] = page

            source = subprocess.Popen(
                [sys.executable, "harness/caption_source.py", "--uri", f"ws://127.0.0.1:{SERVER_PORT}",
                 "--duration", "30", "--quiet"],
                cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
            procs.append(source)
            t0 = time.time()

            def at(seconds):  # sleep until t0 + seconds, keeping the pages alive
                while time.time() < t0 + seconds:
                    for pg in pages.values():
                        pg.wait_for_timeout(50)

            at(8)
            print("cutting browser connections for 5 s")
            proxy.cut()
            at(13)
            proxy.restore()
            at(17)
            print("opening late joiner C")
            page = browser.new_context().new_page()
            frames["C"] = []
            watch(page, frames["C"])
            page.goto(f"http://127.0.0.1:{BROWSER_PORT}/")
            pages["C"] = page

            out, _ = source.communicate(timeout=60)
            last_segment = int(out.split("emitted")[1].split()[0])
            at(time.time() - t0 + 2)  # let the last frames land
            print(f"source emitted {last_segment} segments")

            problems = []
            for name in ("A", "B", "C"):
                problems += check_frames(name, frames[name], last_segment, expect_recovery=name != "C")
                problems += check_page(name, pages[name], sentences, last_segment)
            browser.close()
    finally:
        if proxy:
            proxy.close()
        for proc in procs:
            if proc.poll() is None:
                proc.terminate()
                proc.wait(timeout=5)

    if problems:
        print("\nFAIL")
        for p in problems:
            print("  -", p)
        server_log = server.stdout.read() if server.stdout else ""
        print("\nserver log (last 20 lines):\n" + "\n".join(server_log.splitlines()[-20:]))
        sys.exit(1)
    print("\nPASS: every page got every final exactly once, in order, and shows them all")


if __name__ == "__main__":
    main()
