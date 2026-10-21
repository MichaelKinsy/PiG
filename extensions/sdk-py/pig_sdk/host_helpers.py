"""Host calls for Pi's pure helpers that need the host's parsers: parseFrontmatter (the host's YAML parser, the one that reads skills and prompt templates)
and Date.parse (the host's port of V8's). They read no session state, so the host serves them before an extension registers, and a helper called while the
factory runs (before ``run``) dials the host itself: the first call opens the connection and ``run`` later reuses it. A call from a handler uses the
handler's own connection (``ctx.parse_frontmatter``, ``ctx.parse_date``)."""
import json
import os
import struct
import threading
from dataclasses import dataclass
from typing import Any, Callable, Optional

_CALL_PARSE_FRONTMATTER = "frontmatter.parse"
_CALL_PARSE_DATE = "date.parse"


@dataclass(frozen=True)
class ParsedFrontmatter:
    """Pi's ``{ frontmatter, body }``. ``frontmatter`` is ``parse(yaml) ?? {}``: a dict for the usual mapping header, whose keys follow the order a JavaScript
    object enumerates them (array-index keys first, ascending, then insertion order), and the list, string, number or boolean of any other YAML document."""

    frontmatter: Any
    body: str


class _State:
    # Re-entrant: a listener the host dispatches while a factory-time call waits makes bus calls of its own on the same thread (``loading_call``).
    lock = threading.RLock()
    # The extension whose factory-time bus calls dialled each socket, by path: the owner of the listeners the host dispatches on that connection before ``run``.
    owners: dict = {}
    # The call results a ``loading_call`` read for an outer call that is still waiting, by call id.
    results: dict = {}
    call_seq: int = 0
    # The connections helpers opened before ``run``, by socket path; ``run`` takes its own.
    dialled: dict = {}
    # The extensions that are running (a packed cell runs several), each with the socket it serves in ``_served_socket``: the first that is still running
    # answers a helper that has no Context and no socket of its own.
    running: list = []
    hook: Any = None


def set_host_helpers(parse_frontmatter: Optional[Callable[[str], ParsedFrontmatter]] = None, parse_date: Optional[Callable[[str], Optional[float]]] = None) -> None:
    """Install in-process parsers (a host that runs the extension in its own process); both None removes them."""
    _State.hook = None if parse_frontmatter is None and parse_date is None else (parse_frontmatter, parse_date)


def parse_frontmatter(content: str) -> ParsedFrontmatter:
    """Pi's parseFrontmatter (utils/frontmatter.ts, exported from index.ts), answered by the host's parser. A header the YAML parser refuses is the error Pi
    throws (``HostCallError``). It may be called before ``run``: the first call connects to the host, and ``run`` reuses the connection."""
    hook = _State.hook
    if hook is not None and hook[0] is not None:
        return hook[0](content)
    return _decode_frontmatter(_helper_call(_CALL_PARSE_FRONTMATTER, {"content": content}))


def strip_frontmatter(content: str) -> str:
    """Pi's stripFrontmatter: the body of parse_frontmatter."""
    return parse_frontmatter(content).body


def parse_date(text: str) -> Optional[float]:
    """``Date.parse(text)`` (``new Date(text).getTime()`` for a string), V8's parser answered by the host: the time value in milliseconds, None when it is NaN."""
    hook = _State.hook
    if hook is not None and hook[1] is not None:
        return hook[1](text)
    return _decode_date(_helper_call(_CALL_PARSE_DATE, {"text": text}))


def _decode_frontmatter(result: Any) -> ParsedFrontmatter:
    return ParsedFrontmatter(frontmatter=result.get("frontmatter"), body=result.get("body", ""))


def _decode_date(result: Any) -> Optional[float]:
    ms = result.get("ms")
    return None if ms is None else float(ms)


def _helper_call(method: str, args: Any) -> Any:
    """The result of a host call made outside a handler: over the running extension's connection, or over the one this call dials, which ``run`` reuses."""
    running = _running_for(_socket_path())
    if running is not None:
        return running._call(method, args).get("result") or {}
    return loading_call(method, args)


def loading_call(method: str, args: Any, owner: Any = None) -> Any:
    """A host call made while a factory runs, over the connection this extension's first such call dials and ``run`` later takes. It never rides a running
    extension's connection: the host attributes what a call carries (a ``pi.events`` listener) to the extension of the connection it arrives on.

    While it waits for the result it answers what the host sends the connection: pings, and the ``events.dispatch`` requests of ``owner``'s listeners (the
    extension that made a bus call on this connection), as a Node runtime serves a dispatch during its factory. The wait is re-entrant: a listener that makes a
    bus call of its own waits for its result on the same thread, and the result of an outer call that arrives meanwhile is kept for it."""
    with _State.lock:
        path = _socket_path()
        if not path:
            raise RuntimeError("not connected to the host: PIG_EXT_SOCKET is not set, so this extension was not launched by pig")
        sock = _State.dialled.get(path)
        if sock is None:
            from . import _connect_unix

            sock = _State.dialled[path] = _connect_unix(path)
        if owner is not None:
            _State.owners[path] = owner
        _State.call_seq += 1
        call_id = f"h{_State.call_seq}"
        _send_frame(sock, {"type": "call", "id": call_id, "call": {"method": method, "args": args}})
        while call_id not in _State.results:
            env = _read_frame(sock)
            kind = env.get("type")
            if kind == "call_result":
                _State.results[env.get("id")] = env.get("call_result") or {}
            elif kind == "ping":
                _send_frame(sock, {"type": "pong", "pong": {"nonce": (env.get("ping") or {}).get("nonce", "")}})
            elif kind == "request":
                _answer_loading_request(sock, _State.owners.get(path), env)
            elif kind == "notify" and (env.get("notify") or {}).get("method") == "events.release" and _State.owners.get(path) is not None:
                _State.owners[path]._bus.release(str(((env.get("notify") or {}).get("args") or {}).get("handlerId") or ""))
        result = _State.results.pop(call_id)
        error = result.get("error")
        if error:
            from . import HostCallError

            raise HostCallError(error.get("message", "host call failed"), error.get("code"))
        return result.get("result") or {}


def _answer_loading_request(sock: Any, owner: Any, env: dict) -> None:
    """Answer a request the host made of a connection whose extension has not registered: a listener dispatch runs the listener (its error or panic is its dispatch's failure), anything else is refused so the host does not wait for it."""
    request = env.get("request") or {}
    error = None
    if request.get("method") == "events.dispatch" and owner is not None:
        from . import Context

        try:
            owner._bus.dispatch(Context(owner, _loading=True), request.get("args") or {})
        except Exception as exc:  # noqa: BLE001 - the dispatch's failure, reported to the host as an ordinary listener's is
            error = {"message": str(exc)}
    else:
        error = {"message": f"{request.get('method', '')}: the extension has not registered with the host yet"}
    _send_frame(sock, {"type": "response", "id": env.get("id"), "response": {"result": None, "error": error}})


def _running_for(path: str) -> Any:
    """The running extension a helper uses. A runner that names a socket (PIG_EXT_SOCKET, set to the member's own while its factory runs) means the extension
    that socket belongs to, so a helper called by one member's factory never rides another member's connection, which the host serves only after it has
    accepted this member's: it is the running extension on that socket, and none while this member has not connected (the helper dials it). With no socket
    named, the first extension that is still running answers. An extension that serves no socket (``_served_socket`` empty) answers whichever is named."""
    for running in list(_State.running):
        served = getattr(running, "_served_socket", "")
        if not path or not served or served == path:
            return running
    return None


def _socket_path() -> str:
    """The socket a helper dials: PIG_EXT_SOCKET, which a packed cell's runner sets to the member's socket while the member's factory runs."""
    return os.environ.get("PIG_EXT_SOCKET", "")


def _send_frame(sock: Any, env: dict) -> None:
    data = json.dumps(env, separators=(",", ":")).encode()
    sock.sendall(struct.pack(">I", len(data)) + data)


def _read_frame(sock: Any) -> dict:
    header = _read_exact(sock, 4)
    return json.loads(_read_exact(sock, struct.unpack(">I", header)[0]))


def _read_exact(sock: Any, count: int) -> bytes:
    buffer = bytearray()
    while len(buffer) < count:
        chunk = sock.recv(count - len(buffer))
        if not chunk:
            raise EOFError("the host closed the connection")
        buffer.extend(chunk)
    return bytes(buffer)


def take_helper_socket(path: str) -> Any:
    """The connection a helper opened to ``path``, for ``run`` to serve on; None when there is none."""
    with _State.lock:
        _State.owners.pop(path, None)
        return _State.dialled.pop(path, None)
