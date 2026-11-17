"""rpc-settle-wait.py: the agent_settled handler and the hold command wait on a plain TCP read.

They dial RPC_SETTLE_ADDR, announce themselves with a "waiting" line and return when the peer writes a line. The input handler dials RPC_SETTLE_INPUT_ADDR instead, the before_agent_start handler RPC_SETTLE_START_ADDR. Without the address they return at once. With RPC_SETTLE_DONE the hold command writes that file once its wait returned. The wait is no SDK call, so nothing but the read itself tells the extension runtime from a handler that computes."""
import os
import socket
import time

import pig_sdk


def _wait(env):
    addr = os.environ.get(env)
    if not addr:
        return
    host, port = addr.rsplit(":", 1)
    with socket.create_connection((host, int(port))) as peer:
        peer.sendall(b"waiting\n")
        with peer.makefile("rb") as lines:
            lines.readline()


def _hold(ctx, args):
    _wait("RPC_SETTLE_ADDR")
    done = os.environ.get("RPC_SETTLE_DONE")
    if done:
        with open(done, "w", encoding="utf-8") as marker:
            marker.write("done\n")


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("rpc-settle-wait")
    ext.on_event("agent_settled", lambda ctx, data: _wait("RPC_SETTLE_ADDR"))
    ext.on_event("input", lambda ctx, data: _wait("RPC_SETTLE_INPUT_ADDR"))
    ext.on_event("before_agent_start", lambda ctx, data: _wait("RPC_SETTLE_START_ADDR"))

    def name_handler(event):
        def handle(ctx, data):
            busy = int(os.environ.get("RPC_NAME_BUSY_MS") or 0)
            if not os.environ.get("RPC_NAME_ADDR") and not busy:
                return
            end = time.monotonic() + busy / 1000
            while time.monotonic() < end:
                pass  # computing: nothing waits
            _wait("RPC_NAME_ADDR")
            ctx.notify("name-handler-done:" + event, "info")

        return handle

    # Handlers of the two events Pi emits without awaiting them: with RPC_NAME_ADDR they wait on the peer, with RPC_NAME_BUSY_MS they compute for that long without waiting, and then they notify.
    for event in ("session_info_changed", "thinking_level_select"):
        ext.on_event(event, name_handler(event))
    ext.command("hold", "wait on a socket read", _hold)
    return ext
