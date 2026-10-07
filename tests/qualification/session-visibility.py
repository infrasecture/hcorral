#!/usr/bin/env python3
"""Verify native picker/resume of synthetic history; never start a model turn."""

import json
import os
import selectors
import subprocess
import sys
import tempfile
import time


thread_id, expected_text, workspace, *command = sys.argv[1:]
if not command:
    raise SystemExit("usage: session-visibility.py ID TEXT WORKSPACE COMMAND [ARG...]")

with tempfile.TemporaryFile() as errors:
    process = subprocess.Popen(command, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=errors)
    selector = selectors.DefaultSelector()
    selector.register(process.stdout, selectors.EVENT_READ)
    pending = bytearray()
    request_id = 0

    def send(message):
        process.stdin.write(json.dumps(message).encode() + b"\n")
        process.stdin.flush()

    def call(method, params):
        global request_id
        request_id += 1
        send({"id": request_id, "method": method, "params": params})
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            while b"\n" in pending:
                line, _, rest = pending.partition(b"\n")
                pending[:] = rest
                message = json.loads(line)
                if message.get("id") != request_id:
                    continue
                if "error" in message:
                    raise RuntimeError(f"{method}: {message['error']}")
                return message["result"]
            if selector.select(max(0, deadline - time.monotonic())):
                data = os.read(process.stdout.fileno(), 65536)
                if not data:
                    raise RuntimeError(f"app server closed during {method}")
                pending.extend(data)
                if len(pending) > 16 << 20:
                    raise RuntimeError("unbounded app-server response")
        raise RuntimeError(f"app-server deadline: {method}")

    try:
        call("initialize", {"clientInfo": {"name": "hcorral_qualification", "version": "0.1"},
                            "capabilities": {"experimentalApi": True}})
        send({"method": "initialized"})
        listing = call("thread/list", {"limit": 100, "modelProviders": ["test-provider"]})
        if thread_id not in {thread["id"] for thread in listing["data"]}:
            raise RuntimeError("conversation is missing from the native picker")
        resumed = call("thread/resume", {"threadId": thread_id, "cwd": workspace,
                                         "modelProvider": "test-provider",
                                         "approvalPolicy": "never", "sandbox": "read-only"})
        if resumed["thread"]["id"] != thread_id:
            raise RuntimeError("resume selected another conversation")
        if expected_text not in json.dumps(resumed["thread"].get("turns")):
            raise RuntimeError("resumed conversation did not contain the persisted user message")
        print(f"PASS: native picker and resume preserved {thread_id}")
    except Exception:
        errors.seek(0)
        sys.stderr.buffer.write(errors.read(65536))
        raise
    finally:
        process.stdin.close()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
        selector.close()
        process.stdout.close()
