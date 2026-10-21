"""Host calls for Pi's pure helpers that need the host's parsers: parseFrontmatter (the host's YAML parser, the one that reads skills and prompt templates)
and Date.parse (the host's port of V8's). They read no session state, so the host serves them before an extension registers, and a helper called while the
factory runs (before ``run``) dials the host itself: the first call opens the connection and ``run`` later reuses it. A call from a handler uses the
handler's own connection (``ctx.parse_frontmatter``, ``ctx.parse_date``)."""
import base64
import json
import os
import struct
import threading
from dataclasses import dataclass
from typing import Any, Callable, Optional

from .image_dimension_note import ResizedImage

_CALL_PARSE_FRONTMATTER = "frontmatter.parse"
_CALL_PARSE_DATE = "date.parse"
# The host's ports of Pi's generateDiffString, generateUnifiedPatch, copyToClipboard, convertToPng and resizeImage.
_CALL_GENERATE_DIFF_STRING = "diff.string"
_CALL_GENERATE_UNIFIED_PATCH = "diff.patch"
_CALL_COPY_TO_CLIPBOARD = "clipboard.copy"
_CALL_CONVERT_TO_PNG = "image.convertToPng"
_CALL_RESIZE_IMAGE = "image.resize"
# highlightCode and the theme initTheme installs: the host's highlight.js port and its theme registry. Like the two above they read no session state, so the host
# serves them before an extension registers.
_CALL_HIGHLIGHT_CODE = "highlight.code"
_CALL_LOAD_THEME = "theme.load"


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


@dataclass(frozen=True)
class DiffString:
    """The result of generate_diff_string: Pi's ``{ diff, firstChangedLine }``. ``first_changed_line`` is None when the texts do not differ (undefined in Pi)."""

    diff: str
    first_changed_line: Optional[int]


@dataclass(frozen=True)
class ConvertedImage:
    """The result of convert_to_png: Pi's ``{ data, mimeType }``."""

    data: str
    mime_type: str


@dataclass(frozen=True)
class ImageResizeOptions:
    """Pi's ImageResizeOptions (utils/image-resize-core.ts). None takes Pi's default."""

    max_width: Optional[int] = None
    max_height: Optional[int] = None
    max_bytes: Optional[int] = None
    jpeg_quality: Optional[int] = None


def _diff_args(old_content: str, new_content: str, context_lines: Optional[int]) -> dict:
    args: dict = {"oldContent": old_content, "newContent": new_content}
    if context_lines is not None:
        args["contextLines"] = context_lines
    return args


def _patch_args(path: str, old_content: str, new_content: str, context_lines: Optional[int]) -> dict:
    return {**_diff_args(old_content, new_content, context_lines), "path": path}


def _resize_args(input_bytes: bytes, mime_type: str, options: Optional[ImageResizeOptions]) -> dict:
    args: dict = {"data": base64.b64encode(bytes(input_bytes)).decode("ascii"), "mimeType": mime_type}
    if options is not None:
        wire = {"maxWidth": options.max_width, "maxHeight": options.max_height, "maxBytes": options.max_bytes, "jpegQuality": options.jpeg_quality}
        args["options"] = {key: value for key, value in wire.items() if value is not None}
    return args


def _decode_diff_string(result: Any) -> DiffString:
    return DiffString(diff=result.get("diff", ""), first_changed_line=result.get("firstChangedLine"))


def _decode_converted(result: Any) -> Optional[ConvertedImage]:
    image = result.get("image")
    return None if not isinstance(image, dict) else ConvertedImage(data=image.get("data", ""), mime_type=image.get("mimeType", ""))


def _decode_resized(result: Any) -> Optional[ResizedImage]:
    image = result.get("image")
    if not isinstance(image, dict):
        return None

    def number(key: str) -> float:
        value = image.get(key)
        return float("nan") if value is None else float(value)

    return ResizedImage(number("originalWidth"), number("originalHeight"), number("width"), number("height"), bool(image.get("wasResized")), image.get("data", ""), image.get("mimeType", ""))


def generate_diff_string(old_content: str, new_content: str, context_lines: Optional[int] = None) -> DiffString:
    """Pi's generateDiffString (core/tools/edit-diff.ts, exported from index.ts): the line-numbered display diff of two texts, with the context around each change cut to
    ``context_lines`` (default 4) lines, and the first changed line of the new text. The host's byte-exact port answers. It may be called before ``run``."""
    return _decode_diff_string(_helper_call(_CALL_GENERATE_DIFF_STRING, _diff_args(old_content, new_content, context_lines)))


def generate_unified_patch(path: str, old_content: str, new_content: str, context_lines: Optional[int] = None) -> str:
    """Pi's generateUnifiedPatch (core/tools/edit-diff.ts, exported from index.ts): a standard unified patch of two texts for one path, with ``context_lines`` (default 4)
    lines of context around each change. The host's port of jsdiff answers. It may be called before ``run``."""
    return _helper_call(_CALL_GENERATE_UNIFIED_PATCH, _patch_args(path, old_content, new_content, context_lines)).get("patch", "")


def copy_to_clipboard(text: str) -> None:
    """Pi's copyToClipboard (utils/clipboard.ts, exported from index.ts): the host copies ``text`` with the strategy its own copy commands use, and a failed copy raises
    ``HostCallError``. It may be called before ``run``."""
    _helper_call(_CALL_COPY_TO_CLIPBOARD, {"text": text})


def convert_to_png(base64_data: str, mime_type: str) -> Optional[ConvertedImage]:
    """Pi's convertToPng (utils/image-convert.ts, exported from index.ts): base64 image data as base64 PNG data, which a PNG already is. None, as Pi's null, when the data
    does not decode. It may be called before ``run``."""
    return _decode_converted(_helper_call(_CALL_CONVERT_TO_PNG, {"data": base64_data, "mimeType": mime_type}))


def resize_image(input_bytes: bytes, mime_type: str, options: Optional[ImageResizeOptions] = None) -> Optional[ResizedImage]:
    """Pi's resizeImage (utils/image-resize.ts, exported from index.ts): the image fitted within the option limits (2000 pixels on a side, 4.5 MiB encoded unless
    ``options`` say otherwise), as the host's image code does it. None, as Pi's null, when the image cannot be made to fit. An image already within every limit comes back
    as it was. It may be called before ``run``."""
    return _decode_resized(_helper_call(_CALL_RESIZE_IMAGE, _resize_args(input_bytes, mime_type, options)))
def highlight_code_lines(code: str, lang: str, markdown: bool, theme: Optional[dict]) -> list:
    """The lines the host's highlight.js port makes of ``code`` in ``lang``, drawn with ``theme`` (the palette of the theme ``init_theme`` installed) or, when
    None, with the host's active theme."""
    args: dict = {"code": code, "lang": lang}
    if markdown:
        args["markdown"] = True
    if theme is not None:
        args["theme"] = theme
    lines = _helper_call(_CALL_HIGHLIGHT_CODE, args).get("lines")
    if not isinstance(lines, list):
        raise ValueError("decode highlightCode: the host answered no lines")
    return [line for line in lines if isinstance(line, str)]


def load_theme(name: Optional[str]) -> dict:
    """The palette of the theme ``initTheme(name)`` installs."""
    theme = _helper_call(_CALL_LOAD_THEME, {"name": name}).get("theme")
    if not isinstance(theme, dict):
        raise ValueError("initTheme: the host answered a theme that is not a palette")
    return theme


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


def _call_each_running(method: str, args: Any) -> Any:
    """The result of a host call over the first running extension's connection that answers; the last failure when none does, ``RuntimeError`` when none runs. It
    never dials: it is for a call that must reach the host after the connection it started on has gone."""
    failure: Exception = RuntimeError("no extension is running")
    for running in list(_State.running):
        try:
            return running._call(method, args).get("result") or {}
        except (RuntimeError, OSError, EOFError) as error:
            failure = error
    raise failure


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
