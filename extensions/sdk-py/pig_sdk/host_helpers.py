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
