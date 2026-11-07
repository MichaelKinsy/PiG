"""rpc-settle-wait.py: the agent_settled handler and the hold command wait on a plain TCP read.

They dial RPC_SETTLE_ADDR, announce themselves with a "waiting" line and return when the peer writes a line. The input handler dials RPC_SETTLE_INPUT_ADDR instead. Without the address they return at once. The wait is no SDK call, so nothing but the read itself tells the extension runtime from a handler that computes."""
import os
import socket

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


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("rpc-settle-wait")
    ext.on_event("agent_settled", lambda ctx, data: _wait("RPC_SETTLE_ADDR"))
    ext.on_event("input", lambda ctx, data: _wait("RPC_SETTLE_INPUT_ADDR"))
    ext.command("hold", "wait on a socket read", lambda ctx, args: _wait("RPC_SETTLE_ADDR"))
    return ext
