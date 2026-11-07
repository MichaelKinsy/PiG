"""rpc-settle-wait.py: the agent_settled handler and the hold command wait on a plain TCP read.

They dial RPC_SETTLE_ADDR, announce themselves with a "waiting" line and return when the peer writes a line. Without RPC_SETTLE_ADDR they return at once. The wait is no SDK call, so nothing but the read itself tells the extension runtime from a handler that computes."""
import os
import socket

import pig_sdk


def _wait():
    if not os.environ.get("RPC_SETTLE_ADDR"):
        return
    host, port = os.environ["RPC_SETTLE_ADDR"].rsplit(":", 1)
    with socket.create_connection((host, int(port))) as peer:
        peer.sendall(b"waiting\n")
        with peer.makefile("rb") as lines:
            lines.readline()


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("rpc-settle-wait")
    ext.on_event("agent_settled", lambda ctx, data: _wait())
    ext.command("hold", "wait on a socket read", lambda ctx, args: _wait())
    return ext
