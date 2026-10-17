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
    lock = threading.Lock()
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
    with _State.lock:
        path = _socket_path()
        if not path:
            raise RuntimeError("not connected to the host: PIG_EXT_SOCKET is not set, so this extension was not launched by pig")
        sock = _State.dialled.get(path)
        if sock is None:
            from . import _connect_unix

            sock = _State.dialled[path] = _connect_unix(path)
        return _roundtrip(sock, method, args)


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


def _roundtrip(sock: Any, method: str, args: Any) -> Any:
    data = json.dumps({"type": "call", "id": "h1", "call": {"method": method, "args": args}}, separators=(",", ":")).encode()
    sock.sendall(struct.pack(">I", len(data)) + data)
    while True:
        header = _read_exact(sock, 4)
        env = json.loads(_read_exact(sock, struct.unpack(">I", header)[0]))
        if env.get("type") != "call_result" or env.get("id") != "h1":
            continue
        result = env.get("call_result") or {}
        error = result.get("error")
        if error:
            from . import HostCallError

            raise HostCallError(error.get("message", "host call failed"), error.get("code"))
        return result.get("result") or {}


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
        return _State.dialled.pop(path, None)
