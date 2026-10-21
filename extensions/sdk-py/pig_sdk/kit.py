"""The extension component kit (D107): Pi's tui components described as a
view the host renders with its tui ports.

Build a tree from the node classes here, wrap it in a :class:`View`, and hand
it to a view surface: a component with ``view(width)`` passed to
``ctx.custom``, ``ctx.set_widget(key, view)``,
``ctx.set_header_view``/``ctx.set_footer_view``, the ``call_view``/
``result_view`` tool renderers, or ``ext.message_view_renderer``/
``ext.entry_view_renderer``. The view is authoritative: the host renders it at
the width it lays the surface out at, so the terminal shows what Pi's
components draw.

Constructors take upstream's positional arguments and defaults; upstream's
option bags are keyword arguments. Every style closure upstream takes is a
theme token name here (``"accent"``, ``"customMessageBg"``, ...); the host
validates tokens, colors and ids, and keeps the previous frame for an invalid
view. A component with a ``render`` of its own is a :class:`Lines` range.
"""

from __future__ import annotations

import base64
import hashlib
import threading
from collections.abc import Sequence
from dataclasses import dataclass
from typing import Any, Literal, Protocol, TypeAlias, Union

__all__ = [
    "Align",
    "AssistantMessage",
    "BashExecution",
    "Box",
    "CANCEL",
    "CHANGE",
    "Container",
    "ContentBlock",
    "Diff",
    "DynamicBorder",
    "Event",
    "HStack",
    "Image",
    "Lines",
    "List",
    "ListItem",
    "Loader",
    "LoaderIndicator",
    "Markdown",
    "Message",
    "Node",
    "ABORT",
    "ESCAPE",
    "MOUSE",
    "SELECT",
    "SELECTION_CHANGE",
    "SUBMIT",
    "SelectItem",
    "SelectLayout",
    "SelectList",
    "SettingItem",
    "SettingsList",
    "Spacer",
    "StackEntry",
    "TOOL_DEFINITION_BUILTIN",
    "TOOL_DEFINITION_EMPTY",
    "Text",
    "TextStyle",
    "ToolExecution",
    "ToolResult",
    "ToolResultContent",
    "TruncatedText",
    "UserMessage",
    "VStack",
    "View",
    "ViewComponent",
    "ViewEventHandler",
    "image_content",
    "text_block",
    "text_content",
    "thinking_block",
    "tool_call_block",
]

# Event types: SelectList onSelect, onCancel (also SettingsList's),
# onSelectionChange, and SettingsList onChange.
SELECT = "select"
CANCEL = "cancel"
SELECTION_CHANGE = "selectionChange"
CHANGE = "change"
# The widget kinds' callbacks: Input's and Editor's onSubmit, Input's onEscape,
# CancellableLoader's onAbort and the mouse event a MouseRegion took.
SUBMIT = "submit"
ESCAPE = "escape"
ABORT = "abort"
MOUSE = "mouse"
_EVENT_TYPES = (SELECT, CANCEL, SELECTION_CHANGE, CHANGE, SUBMIT, ESCAPE, ABORT, MOUSE)

# The host's depth bound: deeper trees are invalid, and encoding stops there,
# so a tree that contains itself fails instead of recursing without end.
_MAX_DEPTH = 64

Align: TypeAlias = Literal["stretch", "start", "center", "end"]


class _ImageData:
    """Image bytes and their ref, the lowercase hex SHA-256 of the bytes."""

    __slots__ = ("ref", "mime_type", "data")

    def __init__(self, data: bytes, mime_type: str) -> None:
        if not isinstance(data, (bytes, bytearray, memoryview)):
            raise TypeError("image data must be bytes")
        self.data = bytes(data)
        self.mime_type = str(mime_type)
        self.ref = hashlib.sha256(self.data).hexdigest()

    def __eq__(self, other: object) -> bool:
        return isinstance(other, _ImageData) and (self.ref, self.mime_type) == (other.ref, other.mime_type)

    def __hash__(self) -> int:
        return hash((self.ref, self.mime_type))


def _put(out: dict[str, Any], key: str, value: Any) -> None:
    if value is not None:
        out[key] = value


class _Encoder:
    """Encodes nodes, collecting the images they reference, one per ref."""

    def __init__(self, frontend: bool) -> None:
        self.frontend = frontend
        self.images: list[_ImageData] = []

    def add_image(self, image: _ImageData) -> None:
        if all(known.ref != image.ref for known in self.images):
            self.images.append(image)

    def node(self, node: Node, depth: int) -> dict[str, Any]:
        if depth >= _MAX_DEPTH:
            raise ValueError(f"kit: view deeper than {_MAX_DEPTH} nodes")
        if not isinstance(node, _Node):
            raise TypeError(f"kit: {node!r} is not a kit node")
        out: dict[str, Any] = {"kind": node.kind}
        out.update(node._fields(self, depth))
        return out

    def children(self, children: list[Node], depth: int) -> dict[str, Any]:
        return {"children": [self.node(child, depth + 1) for child in children]} if children else {}


class _Node:
    kind = ""

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        raise NotImplementedError

    def __eq__(self, other: object) -> bool:
        return type(self) is type(other) and self.__dict__ == other.__dict__

    def __repr__(self) -> str:
        fields = ", ".join(f"{key}={value!r}" for key, value in self.__dict__.items() if value is not None)
        return f"{type(self).__name__}({fields})"


class Container(_Node):
    """Upstream ``Container``: its children one after another."""

    kind = "container"

    def __init__(self, children: list[Node] | None = None) -> None:
        self.children: list[Node] = list(children or [])

    def add_child(self, child: Node) -> Container:
        """Upstream ``addChild``; returns the container for chaining."""
        self.children.append(child)
        return self

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        return enc.children(self.children, depth)


class Box(_Node):
    """Upstream ``Box(paddingX = 1, paddingY = 1, bgFn?)``: its children inside
    padding, on an optional background token."""

    kind = "box"

    def __init__(self, padding_x: int = 1, padding_y: int = 1, *, bg: str | None = None, children: list[Node] | None = None) -> None:
        self.padding_x = int(padding_x)
        self.padding_y = int(padding_y)
        self.bg = bg
        self.children: list[Node] = list(children or [])

    def add_child(self, child: Node) -> Box:
        """Upstream ``addChild``; returns the box for chaining."""
        self.children.append(child)
        return self

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out = enc.children(self.children, depth)
        out.update(paddingX=self.padding_x, paddingY=self.padding_y)
        _put(out, "bg", self.bg)
        return out


class Text(_Node):
    """Upstream ``Text(text = "", paddingX = 1, paddingY = 1, customBgFn?)``."""

    kind = "text"

    def __init__(self, text: str = "", padding_x: int = 1, padding_y: int = 1, *, bg: str | None = None) -> None:
        self.text = str(text)
        self.padding_x = int(padding_x)
        self.padding_y = int(padding_y)
        self.bg = bg

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {"text": self.text, "paddingX": self.padding_x, "paddingY": self.padding_y}
        _put(out, "bg", self.bg)
        return out


class TruncatedText(_Node):
    """Upstream ``TruncatedText(text, paddingX = 0, paddingY = 0)``."""

    kind = "truncated-text"

    def __init__(self, text: str, padding_x: int = 0, padding_y: int = 0) -> None:
        self.text = str(text)
        self.padding_x = int(padding_x)
        self.padding_y = int(padding_y)

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        return {"text": self.text, "paddingX": self.padding_x, "paddingY": self.padding_y}


@dataclass
class TextStyle:
    """Upstream Markdown ``DefaultTextStyle``, with theme tokens for its color
    functions."""

    color: str | None = None
    bg_color: str | None = None
    bold: bool = False
    italic: bool = False
    strikethrough: bool = False
    underline: bool = False

    def _encode(self) -> dict[str, Any]:
        out: dict[str, Any] = {}
        _put(out, "color", self.color)
        _put(out, "bgColor", self.bg_color)
        for key in ("bold", "italic", "strikethrough", "underline"):
            if getattr(self, key):
                out[key] = True
        return out


class Markdown(_Node):
    """Upstream ``Markdown(text, paddingX, paddingY, theme, defaultTextStyle?,
    options?)``, themed with coding-agent's ``getMarkdownTheme()``."""

    kind = "markdown"

    def __init__(
        self,
        text: str,
        padding_x: int = 0,
        padding_y: int = 0,
        *,
        default_text_style: TextStyle | None = None,
        render_latex: bool | None = None,
    ) -> None:
        self.text = str(text)
        self.padding_x = int(padding_x)
        self.padding_y = int(padding_y)
        self.default_text_style = default_text_style
        self.render_latex = render_latex

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {"text": self.text, "paddingX": self.padding_x, "paddingY": self.padding_y}
        if self.default_text_style is not None:
            out["defaultTextStyle"] = self.default_text_style._encode()
        _put(out, "renderLatex", self.render_latex)
        return out


class Spacer(_Node):
    """Upstream ``Spacer(lines = 1)``."""

    kind = "spacer"

    def __init__(self, lines: int = 1) -> None:
        self.lines = int(lines)

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        return {"lines": self.lines}


class DynamicBorder(_Node):
    """Coding-agent ``DynamicBorder(color)``: a full-width rule in a foreground
    token."""

    kind = "dynamic-border"

    def __init__(self, color: str = "border") -> None:
        self.color = str(color)

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        return {"color": self.color}


@dataclass
class SelectItem:
    """Upstream ``SelectItem``."""

    value: str
    label: str
    description: str | None = None

    def _encode(self) -> dict[str, Any]:
        out: dict[str, Any] = {"value": self.value, "label": self.label}
        _put(out, "description", self.description)
        return out


@dataclass
class SelectLayout:
    """Upstream ``SelectListLayoutOptions`` without ``truncatePrimary``, a
    closure."""

    min_primary_column_width: int | None = None
    max_primary_column_width: int | None = None

    def _encode(self) -> dict[str, Any]:
        out: dict[str, Any] = {}
        _put(out, "minPrimaryColumnWidth", self.min_primary_column_width)
        _put(out, "maxPrimaryColumnWidth", self.max_primary_column_width)
        return out


class SelectList(_Node):
    """Upstream ``SelectList(items, maxVisible, theme, layout?)``, themed with
    coding-agent's ``getSelectListTheme()``.

    ``id`` names the list in :attr:`Event.node` and :attr:`View.focus`; it is
    required and unique within the view. The host owns the selection and
    filter and reports the callbacks as :class:`Event`. ``selected_index``
    and ``filter`` are upstream's ``setSelectedIndex``/``setFilter``: applied
    when they differ from the value this view sent last, so repeating one
    never undoes the user's moves.
    """

    kind = "select-list"

    def __init__(
        self,
        id: str,  # noqa: A002 - upstream's field name
        items: list[SelectItem],
        max_visible: int = 5,
        *,
        layout: SelectLayout | None = None,
        selected_index: int | None = None,
        filter: str | None = None,  # noqa: A002 - upstream's setter name
    ) -> None:
        self.id = str(id)
        self.items = list(items)
        self.max_visible = int(max_visible)
        self.layout = layout
        self.selected_index = selected_index
        self.filter = filter

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {"id": self.id, "items": [item._encode() for item in self.items], "maxVisible": self.max_visible}
        if self.layout is not None:
            out["layout"] = self.layout._encode()
        _put(out, "selectedIndex", self.selected_index)
        _put(out, "filter", self.filter)
        return out


@dataclass
class SettingItem:
    """Upstream ``SettingItem``; ``submenu`` is a node the open setting shows in
    the list's place."""

    id: str
    label: str
    current_value: str
    description: str | None = None
    values: list[str] | None = None
    submenu: Node | None = None

    def _encode(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {"id": self.id, "label": self.label}
        _put(out, "description", self.description)
        out["currentValue"] = self.current_value
        if self.values is not None:
            out["values"] = list(self.values)
        if self.submenu is not None:
            out["submenu"] = enc.node(self.submenu, depth + 1)
        return out


class SettingsList(_Node):
    """Upstream ``SettingsList(items, maxVisible, theme, onChange, onCancel,
    options?)``, themed with coding-agent's ``getSettingsListTheme()``. The
    host owns its state and reports ``onChange``/``onCancel`` as
    :class:`Event`. ``selected_index`` (among the shown settings) and
    ``filter`` (the search input's text) apply as :class:`SelectList`'s do."""

    kind = "settings-list"

    def __init__(
        self,
        id: str,  # noqa: A002 - upstream's field name
        items: list[SettingItem],
        max_visible: int,
        *,
        enable_search: bool = False,
        selected_index: int | None = None,
        filter: str | None = None,  # noqa: A002 - the search input's value
    ) -> None:
        self.id = str(id)
        self.items = list(items)
        self.max_visible = int(max_visible)
        self.enable_search = bool(enable_search)
        self.selected_index = selected_index
        self.filter = filter

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {"id": self.id, "items": [item._encode(enc, depth) for item in self.items], "maxVisible": self.max_visible}
        if self.enable_search:
            out["enableSearch"] = True
        _put(out, "selectedIndex", self.selected_index)
        _put(out, "filter", self.filter)
        return out


class Image(_Node):
    """Upstream ``Image(base64Data, mimeType, theme, options?)`` given the
    decoded bytes. A connection sends the bytes once; later frames name
    :attr:`ref`."""

    kind = "image"

    def __init__(
        self,
        data: bytes,
        mime_type: str,
        *,
        max_width_cells: int | None = None,
        max_height_cells: int | None = None,
        filename: str | None = None,
        fallback_color: str | None = None,
    ) -> None:
        self._image = _ImageData(data, mime_type)
        self.max_width_cells = max_width_cells
        self.max_height_cells = max_height_cells
        self.filename = filename
        self.fallback_color = fallback_color

    @property
    def ref(self) -> str:
        """The lowercase hex SHA-256 of the image's bytes."""
        return self._image.ref

    @property
    def data(self) -> bytes:
        """The image's decoded bytes."""
        return self._image.data

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {"ref": self._image.ref, "mimeType": self._image.mime_type}
        _put(out, "maxWidthCells", self.max_width_cells)
        _put(out, "maxHeightCells", self.max_height_cells)
        _put(out, "filename", self.filename)
        _put(out, "fallbackColor", self.fallback_color)
        enc.add_image(self._image)
        return out


@dataclass
class LoaderIndicator:
    """Upstream ``LoaderIndicatorOptions``. ``frames=[]`` hides the indicator."""

    frames: list[str] | None = None
    interval_ms: int | None = None

    def _encode(self) -> dict[str, Any]:
        out: dict[str, Any] = {}
        if self.frames is not None:
            out["frames"] = list(self.frames)
        _put(out, "intervalMs", self.interval_ms)
        return out


class Loader(_Node):
    """Upstream ``Loader(ui, spinnerColorFn, messageColorFn, message =
    "Loading...", indicator?)``. The host animates it while the surface is
    mounted, from ``frame`` (upstream ``currentFrame``)."""

    kind = "loader"

    def __init__(
        self,
        message: str = "Loading...",
        *,
        spinner_color: str | None = None,
        message_color: str | None = None,
        indicator: LoaderIndicator | None = None,
        frame: int | None = None,
    ) -> None:
        self.message = str(message)
        self.spinner_color = spinner_color
        self.message_color = message_color
        self.indicator = indicator
        self.frame = frame

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {"message": self.message}
        _put(out, "spinnerColor", self.spinner_color)
        _put(out, "messageColor", self.message_color)
        if self.indicator is not None:
            out["indicator"] = self.indicator._encode()
        _put(out, "frame", self.frame)
        return out


@dataclass
class StackEntry:
    """Upstream ``StackEntry``: a stack child with its options, without
    ``visible`` (a closure). ``basis=None`` is ``"auto"``."""

    component: Node
    basis: int | None = None
    grow: int | None = None
    shrink: int | None = None
    min_size: int | None = None
    max_size: int | None = None

    def _encode_options(self) -> dict[str, Any]:
        out: dict[str, Any] = {}
        _put(out, "basis", self.basis)
        _put(out, "grow", self.grow)
        _put(out, "shrink", self.shrink)
        _put(out, "minSize", self.min_size)
        _put(out, "maxSize", self.max_size)
        return out


class _Stack(_Node):
    def __init__(self, children: list[Node | StackEntry] | None = None, *, gap: int | None = None, align: Align | None = None) -> None:
        self.children: list[Node | StackEntry] = list(children or [])
        self.gap = gap
        self.align = align

    def add_child(self, child: Node | StackEntry) -> _Stack:
        """Upstream ``addChild``; a :class:`StackEntry` carries the options."""
        self.children.append(child)
        return self

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {}
        if self.children:
            encoded = []
            for child in self.children:
                if isinstance(child, StackEntry):
                    node = enc.node(child.component, depth + 1)
                    # Default entry options travel as no options, as upstream's addChild records none.
                    options = child._encode_options()
                    if options:
                        node["stack"] = options
                else:
                    node = enc.node(child, depth + 1)
                encoded.append(node)
            out["children"] = encoded
        _put(out, "gap", self.gap)
        _put(out, "align", self.align)
        return out


class HStack(_Stack):
    """Upstream ``HStack(children?, options?)``: children side by side."""

    kind = "hstack"


class VStack(_Stack):
    """Upstream ``VStack(children?, options?)``: children one under another."""

    kind = "vstack"


@dataclass
class ListItem:
    """One row of a :class:`List` annotation."""

    label: str
    detail: str | None = None
    columns: list[str] | None = None

    def _encode(self) -> dict[str, Any]:
        out: dict[str, Any] = {"label": self.label}
        _put(out, "detail", self.detail)
        if self.columns is not None:
            out["columns"] = [str(column) for column in self.columns]
        return out


@dataclass
class List:
    """Frontend-only: the rows of a :class:`Lines` node are a list, one item
    per row, which a frontend may draw as a native list. ``selected`` is the
    selected item's index, ``-1`` for none."""

    items: list[ListItem]
    selected: int = -1

    def _encode(self) -> dict[str, Any]:
        return {"items": [item._encode() for item in self.items], "selectedIndex": int(self.selected)}


class Lines(_Node):
    """Rows a component of the extension's own drew, shown verbatim.

    :meth:`image`, :meth:`progress` and ``list`` tell a frontend what the rows
    depict; the SDK sends them, and the image bytes, only while a frontend
    draws. ``list`` (for example a track table that keeps its own terminal
    look) has one item per row; the host rejects a list whose item count
    differs from the rows.
    """

    kind = "lines"

    def __init__(self, content: list[str], *, list: List | None = None) -> None:  # noqa: A002 - the wire's name
        self.content = [str(line) for line in content]
        self.list = list
        self._image: _ImageData | None = None
        self._progress: tuple[float, float] | None = None

    def image(self, data: bytes, mime_type: str) -> Lines:
        """Frontend-only: the rows depict this image (for example a half-block
        cover), which a frontend may draw in their place."""
        self._image = _ImageData(data, mime_type)
        return self

    def progress(self, value: float, max: float) -> Lines:  # noqa: A002 - the wire's name
        """Frontend-only: the rows show ``value`` in ``0..max`` (for example a
        play bar), which a frontend may draw as a native bar."""
        self._progress = (float(value), float(max))
        return self

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {"content": [*self.content]}
        if enc.frontend:
            if self._image is not None:
                out["image"] = {"ref": self._image.ref}
                enc.add_image(self._image)
            if self._progress is not None:
                out["progress"] = {"value": self._progress[0], "max": self._progress[1]}
            if self.list is not None:
                out["list"] = self.list._encode()
        return out


# The conversation kinds are Pi's conversation components, which Pi exports
# to extensions: a user message, an assistant message with its thinking, a
# tool call's card, a `!` command and a colored diff. The host draws each with
# the port PiG's main transcript uses, and a frontend draws them as it draws
# the transcript. A node with a non-empty ``id`` keeps its host component
# across frames while its constructor arguments stay the same, as a Pi author
# keeps a component and calls its update methods; then a field that differs
# from the value sent last applies as that method. See
# docs/plan/extension-component-kit.md §2.1.


class UserMessage(_Node):
    """Upstream ``UserMessageComponent(text, getMarkdownTheme(), outputPad =
    1)``: the user's Markdown on the user message background."""

    kind = "user-message"

    def __init__(self, text: str, output_pad: int = 1) -> None:
        self.text = str(text)
        self.output_pad = int(output_pad)

    def set_output_pad(self, padding: int) -> None:
        """Upstream ``setOutputPad``."""
        self.output_pad = int(padding)

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        return {"text": self.text, "outputPad": self.output_pad}


@dataclass
class ContentBlock:
    """A content block of an assistant message: :func:`text_block`,
    :func:`thinking_block` or :func:`tool_call_block`."""

    type: str
    text: str = ""
    thinking: str = ""

    def _encode(self) -> dict[str, Any]:
        out: dict[str, Any] = {"type": self.type}
        if self.text:
            out["text"] = self.text
        if self.thinking:
            out["thinking"] = self.thinking
        return out


def text_block(text: str) -> ContentBlock:
    """A text content block."""
    return ContentBlock("text", text=str(text))


def thinking_block(thinking: str) -> ContentBlock:
    """A thinking content block."""
    return ContentBlock("thinking", thinking=str(thinking))


def tool_call_block() -> ContentBlock:
    """A tool call content block. The component draws none; it separates
    thinking runs and leaves abort and error lines to the tool cards."""
    return ContentBlock("toolCall")


@dataclass
class Message:
    """The part of upstream ``AssistantMessage`` the component draws.
    ``stop_reason`` is ``"stop"`` (also ``""``), ``"length"``, ``"toolUse"``,
    ``"error"`` or ``"aborted"``."""

    content: list[ContentBlock]
    stop_reason: str = ""
    error_message: str = ""

    def _encode(self) -> dict[str, Any]:
        out: dict[str, Any] = {"content": [block._encode() for block in self.content]}
        if self.stop_reason:
            out["stopReason"] = self.stop_reason
        if self.error_message:
            out["errorMessage"] = self.error_message
        return out


class AssistantMessage(_Node):
    """Upstream ``AssistantMessageComponent(message, hideThinkingBlock,
    getMarkdownTheme(), hiddenThinkingLabel, outputPad)``: text blocks as
    Markdown, thinking runs in the thinking color (or the hidden label), and a
    length, abort or error line. A click on a thinking run toggles it on the
    host. Upstream's defaults: thinking shown, the label ``"Thinking..."`` and
    ``outputPad`` 1. No message draws nothing."""

    kind = "assistant-message"

    def __init__(self, message: Message | None = None, *, id: str = "") -> None:  # noqa: A002 - upstream's field name
        self.id = str(id)
        self.message = message
        self.is_streaming = False
        self.hide_thinking_block = False
        self.hidden_thinking_label = "Thinking..."
        self.output_pad = 1

    def update_content(self, message: Message, is_streaming: bool) -> None:
        """Upstream ``updateContent(message, isStreaming)``."""
        self.message = message
        self.is_streaming = bool(is_streaming)

    def set_hide_thinking_block(self, hide: bool) -> None:
        """Upstream ``setHideThinkingBlock``; on the host it also clears the
        runs a click toggled."""
        self.hide_thinking_block = bool(hide)

    def set_hidden_thinking_label(self, label: str) -> None:
        """Upstream ``setHiddenThinkingLabel``."""
        self.hidden_thinking_label = str(label)

    def set_output_pad(self, padding: int) -> None:
        """Upstream ``setOutputPad`` (0 or 1)."""
        self.output_pad = int(padding)

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {}
        if self.id:
            out["id"] = self.id
        if self.message is not None:
            out["message"] = self.message._encode()
        out["outputPad"] = self.output_pad
        if self.hide_thinking_block:
            out["hideThinkingBlock"] = True
        if self.hidden_thinking_label != "Thinking...":
            out["hiddenThinkingLabel"] = self.hidden_thinking_label
        if self.is_streaming:
            out["isStreaming"] = True
        return out


# Tool definitions name the renderers of a :class:`ToolExecution`, since a
# tool definition is closures: a built-in tool's renderers (the default), or
# a definition without renderers.
TOOL_DEFINITION_BUILTIN = "builtin"
TOOL_DEFINITION_EMPTY = "empty"


@dataclass
class ToolResultContent:
    """A content block of a tool result: :func:`text_content` or
    :func:`image_content`."""

    text: str = ""
    image: _ImageData | None = None


def text_content(text: str) -> ToolResultContent:
    """A text block of a tool result."""
    return ToolResultContent(text=str(text))


def image_content(data: bytes, mime_type: str) -> ToolResultContent:
    """An image block of a tool result: the decoded bytes. The SDK sends them
    as other kit images, once per connection."""
    return ToolResultContent(image=_ImageData(data, mime_type))


@dataclass
class ToolResult:
    """The result upstream ``updateResult`` receives. ``details`` is any JSON
    value the built-in renderers read (an edit's diff, a read's truncation),
    or None. ``duration_ms`` is a final result's recorded execution time in
    milliseconds, or None; a shell's card shows it as "Took …" once the
    result is final."""

    content: list[ToolResultContent]
    is_error: bool = False
    details: Any = None
    duration_ms: int | None = None


class ToolExecution(_Node):
    """Upstream ``ToolExecutionComponent(toolName, toolCallId, args,
    {showImages, imageWidthCells, outputPad}, toolDefinition, ui, cwd)`` and
    its state: the tool card the main transcript draws. A click on a result
    toggles its expansion on the host. Upstream's defaults: the built-in
    definition, images shown 60 cells wide, outputPad 1, and no result yet."""

    kind = "tool-execution"

    def __init__(
        self,
        tool_name: str,
        tool_call_id: str = "",
        args: Any = None,
        cwd: str = "",
        *,
        tool_definition: str = TOOL_DEFINITION_BUILTIN,
        output_pad: int = 1,
        id: str = "",  # noqa: A002 - upstream's field name
    ) -> None:
        self.id = str(id)
        self.tool_name = str(tool_name)
        self.tool_call_id = str(tool_call_id)
        self.args = args
        self.tool_definition = str(tool_definition)
        self.cwd = str(cwd)
        self.show_images = True
        self.image_width_cells = 60
        self.output_pad = int(output_pad)
        self.execution_started = False
        self.args_complete = False
        self.expanded = False
        self.result: ToolResult | None = None
        self.is_partial = True

    def update_args(self, args: Any) -> None:
        """Upstream ``updateArgs``."""
        self.args = args

    def mark_execution_started(self) -> None:
        """Upstream ``markExecutionStarted``."""
        self.execution_started = True

    def set_args_complete(self) -> None:
        """Upstream ``setArgsComplete``."""
        self.args_complete = True

    def update_result(self, result: ToolResult, is_partial: bool) -> None:
        """Upstream ``updateResult(result, isPartial)``."""
        self.result = result
        self.is_partial = bool(is_partial)

    def set_expanded(self, expanded: bool) -> None:
        """Upstream ``setExpanded``."""
        self.expanded = bool(expanded)

    def set_show_images(self, show: bool) -> None:
        """Upstream ``setShowImages``."""
        self.show_images = bool(show)

    def set_image_width_cells(self, width: int) -> None:
        """Upstream ``setImageWidthCells``: at least 1."""
        self.image_width_cells = max(1, int(width))

    def set_output_pad(self, padding: int) -> None:
        """Upstream ``setOutputPad``: the card's horizontal padding."""
        self.output_pad = int(padding)

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        # The constructor arguments always, the options when they differ from
        # upstream's defaults, and isPartial with a result.
        out: dict[str, Any] = {}
        if self.id:
            out["id"] = self.id
        if self.output_pad != 1:
            out["outputPad"] = self.output_pad
        out["toolName"] = self.tool_name
        if self.tool_call_id:
            out["toolCallId"] = self.tool_call_id
        out["args"] = {} if self.args is None else self.args
        if self.tool_definition != TOOL_DEFINITION_BUILTIN:
            out["toolDefinition"] = self.tool_definition
        if self.cwd:
            out["cwd"] = self.cwd
        if not self.show_images:
            out["showImages"] = False
        if self.image_width_cells != 60:
            out["imageWidthCells"] = self.image_width_cells
        if self.execution_started:
            out["executionStarted"] = True
        if self.args_complete:
            out["argsComplete"] = True
        if self.expanded:
            out["expanded"] = True
        if self.result is not None:
            content: list[dict[str, Any]] = []
            for block in self.result.content:
                if block.image is not None:
                    enc.add_image(block.image)
                    content.append({"type": "image", "ref": block.image.ref, "mimeType": block.image.mime_type})
                else:
                    content.append(ContentBlock("text", text=block.text)._encode())
            result: dict[str, Any] = {"content": content}
            if self.result.is_error:
                result["isError"] = True
            _put(result, "details", self.result.details)
            if self.result.duration_ms is not None:
                result["durationMs"] = int(self.result.duration_ms)
            out["result"] = result
            out["isPartial"] = self.is_partial
        return out


class BashExecution(_Node):
    """Upstream ``BashExecutionComponent(command, ui, excludeFromContext,
    outputPad)``: a ``!`` command between two borders, with its spinner
    while it runs (the host animates it) and its status after."""

    kind = "bash-execution"

    def __init__(self, command: str, exclude_from_context: bool = False, *, output_pad: int = 1, id: str = "") -> None:  # noqa: A002 - upstream's field name
        self.id = str(id)
        self.command = str(command)
        self.exclude_from_context = bool(exclude_from_context)
        self.output_pad = int(output_pad)
        self.output = ""
        self.expanded = False
        self.complete: dict[str, Any] | None = None

    def append_output(self, chunk: str) -> None:
        """Upstream ``appendOutput``."""
        self.output += str(chunk)

    def set_complete(self, exit_code: int | None, cancelled: bool = False, truncated: bool = False, full_output_path: str = "") -> None:
        """Upstream ``setComplete(exitCode, cancelled, truncated ? {truncated}
        : undefined, fullOutputPath)``: the exit code (None when unknown),
        whether the command was cancelled, whether its output was truncated,
        and where the full output is ("" for nowhere)."""
        complete: dict[str, Any] = {}
        if exit_code is not None:
            complete["exitCode"] = int(exit_code)
        if cancelled:
            complete["cancelled"] = True
        if truncated:
            complete["truncated"] = True
        if full_output_path:
            complete["fullOutputPath"] = str(full_output_path)
        self.complete = complete

    def set_expanded(self, expanded: bool) -> None:
        """Upstream ``setExpanded``."""
        self.expanded = bool(expanded)

    def set_output_pad(self, padding: int) -> None:
        """Upstream ``setOutputPad``: the horizontal padding of the command,
        output and status rows."""
        self.output_pad = int(padding)

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {}
        if self.id:
            out["id"] = self.id
        if self.output_pad != 1:
            out["outputPad"] = self.output_pad
        if self.expanded:
            out["expanded"] = True
        out["command"] = self.command
        if self.exclude_from_context:
            out["excludeFromContext"] = True
        if self.output:
            out["output"] = self.output
        if self.complete is not None:
            out["complete"] = dict(self.complete)
        return out


class Diff(_Node):
    """Upstream ``renderDiff(diff, {filePath})`` drawn in a ``Text(…,
    paddingX, paddingY)``, as Pi's edit renderer draws it: context, removed
    and added lines in the diff colors, with a changed word inverted."""

    kind = "diff"

    def __init__(self, diff: str, *, file_path: str = "", padding_x: int = 0, padding_y: int = 0) -> None:
        self.diff = str(diff)
        self.file_path = str(file_path)
        self.padding_x = int(padding_x)
        self.padding_y = int(padding_y)

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {"paddingX": self.padding_x, "paddingY": self.padding_y, "diff": self.diff}
        if self.file_path:
            out["filePath"] = self.file_path
        return out


Node: TypeAlias = Union[
    Container,
    Box,
    Text,
    TruncatedText,
    Markdown,
    Spacer,
    DynamicBorder,
    SelectList,
    SettingsList,
    Image,
    Loader,
    HStack,
    VStack,
    Lines,
    UserMessage,
    AssistantMessage,
    ToolExecution,
    BashExecution,
    Diff,
]


class View:
    """A component tree with the surface's focus and theme overrides.

    ``focus`` is the id of the select-list, settings-list, input, editor,
    cancellable-loader or bordered-loader that receives the keys it binds (every
    key for an input and an editor; ``ui.custom`` only). ``theme`` overrides tokens for this
    surface only: foreground ``accent``, ``muted``, ``dim``, ``text``,
    ``border``, ``borderAccent``, ``borderMuted``, ``success``, ``warning``,
    ``error``; background ``selectedBg``, ``customMessageBg``; values are
    ``#rrggbb``.
    """

    def __init__(self, root: Node, *, focus: str | None = None, theme: dict[str, str] | None = None) -> None:
        self.root = root
        self.focus = focus
        self.theme: dict[str, str] = dict(theme or {})

    def __eq__(self, other: object) -> bool:
        return isinstance(other, View) and (self.root, self.focus, self.theme) == (other.root, other.focus, other.theme)

    def __repr__(self) -> str:
        return f"View(root={self.root!r}, focus={self.focus!r}, theme={self.theme!r})"

    def _encode(self, frontend: bool) -> _Encoded:
        """The wire ``ViewPayload`` without ``images``, and the images it
        references. A tree deeper than the host's bound raises, as in the Go
        SDK."""
        enc = _Encoder(frontend)
        body: dict[str, Any] = {"root": enc.node(self.root, 0)}
        if self.focus:
            body["focus"] = self.focus
        if self.theme:
            body["theme"] = dict(self.theme)
        return _Encoded(body, enc.images)


class _Encoded:
    """A view's wire body and the images it references, one per ref."""

    __slots__ = ("body", "images")

    def __init__(self, body: dict[str, Any], images: list[_ImageData]) -> None:
        self.body = body
        self.images = images

    def references(self, refs: set[str]) -> bool:
        return any(image.ref in refs for image in self.images)

    def refs(self) -> list[str]:
        return [image.ref for image in self.images]


@dataclass
class Event:
    """A callback of an interactive node of a ``ui.custom`` view (wire
    ``ui.view.event``), delivered to ``handle_view_event``.

    ``type`` is :data:`SELECT`, :data:`CANCEL`, :data:`SELECTION_CHANGE` or
    :data:`CHANGE`. ``index`` is the item's index among the shown (filtered)
    items for select and selection changes, 0 otherwise; ``item`` is the
    select-list item of those; ``id`` and ``value`` are a changed setting
    and its new value.
    """

    node: str
    type: str
    index: int = 0
    item: SelectItem | None = None
    id: str = ""
    value: str = ""
    #: ``{"line", "col"}`` of an input or an editor after change, submit and escape.
    cursor: dict[str, int] | None = None
    #: The mouse event (``type``, ``button``, ``x``, ``y``, ``screenX``, ...) of a mouse event.
    mouse: dict[str, Any] | None = None

    @staticmethod
    def _from_wire(args: dict[str, Any]) -> tuple[str, Event] | None:
        """The overlay key and event of a ``ui.view.event``. An unknown type is
        not an event this SDK delivers."""
        kind = args.get("type")
        if kind not in _EVENT_TYPES:
            return None
        raw_item = args.get("item")
        item = None
        if isinstance(raw_item, dict):
            description = raw_item.get("description")
            item = SelectItem(str(raw_item.get("value") or ""), str(raw_item.get("label") or ""), None if description is None else str(description))
        event = Event(
            node=str(args.get("node") or ""),
            type=str(kind),
            index=int(args.get("index") or 0),
            item=item,
            id=str(args.get("id") or ""),
            value=str(args.get("value") or ""),
            cursor=dict(args["cursor"]) if isinstance(args.get("cursor"), dict) else None,
            mouse=dict(args["mouse"]) if isinstance(args.get("mouse"), dict) else None,
        )
        return str(args.get("key") or ""), event


class ViewComponent(Protocol):
    """A focused ``ui.custom`` component whose frames are views.

    Pass it to ``ctx.custom``. The host renders the view and owns the state of
    its lists: keys the focused list binds go to it, every other key reaches
    ``handle_input``. A component that is also a :class:`ViewEventHandler`
    receives the lists' callbacks; one without ``handle_view_event`` ignores
    events.
    """

    def view(self, width: int) -> View: ...

    def handle_input(self, data: str) -> Any: ...


class ViewEventHandler(Protocol):
    """The optional part of a :class:`ViewComponent`: ``handle_view_event``
    receives the lists' callbacks after every input sent before them, on the
    same worker as ``handle_input``, so the two never run at once. Its result
    means what ``handle_input``'s does: done closes the overlay with its
    value, a raise closes it with the error, and pending renders the next
    frame.
    """

    def handle_view_event(self, event: Event) -> Any: ...


class _ViewLedger:
    """A connection's view state: the frontend flag, the image refs the host
    holds, and the latest encoded view of each widget, header and footer,
    which an eviction sends again."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._frontend = False
        self._sent: set[str] = set()
        # Each held across a send, so an eviction's resend and the author's
        # next frame keep their order.
        self.widgets_lock = threading.Lock()
        self.widgets: dict[str, tuple[_Encoded, dict[str, Any] | None]] = {}
        self.surfaces_lock = threading.Lock()
        self.surfaces: dict[str, _Encoded] = {}

    def apply_state(self, state: dict[str, Any]) -> None:
        """Apply a whole state snapshot (ready or ``state_update``):
        ``frontend`` is true only while a frontend draws."""
        with self._lock:
            self._frontend = state.get("frontend") is True

    def encode(self, view: View) -> _Encoded:
        if not isinstance(view, View):
            raise TypeError("a view surface takes a kit.View")
        with self._lock:
            frontend = self._frontend
        return view._encode(frontend)

    def has_unsent(self, encoded: _Encoded) -> bool:
        with self._lock:
            return any(image.ref not in self._sent for image in encoded.images)

    def wire(self, encoded: _Encoded) -> dict[str, Any]:
        """The wire view: the body with the data of every image not sent on
        this connection yet, which counts as sent from here on."""
        unsent = []
        with self._lock:
            for image in encoded.images:
                if image.ref not in self._sent:
                    self._sent.add(image.ref)
                    unsent.append({"ref": image.ref, "mimeType": image.mime_type, "data": base64.b64encode(image.data).decode("ascii")})
        if unsent:
            return dict(encoded.body, images=unsent)
        return encoded.body

    def forget(self, refs: set[str]) -> None:
        """Forget refs the host dropped (``ui.view.evicted``)."""
        with self._lock:
            self._sent -= refs

    def result(self, value: Any, view: bool) -> dict[str, Any]:
        """A renderer's wire ``RenderResult``: ``{view}`` for a view renderer,
        with the lines absent, else ``{lines}``."""
        if view:
            return {"view": self.wire(self.encode(value))}
        return {"lines": value}


# ── widget kinds (docs/plan/extension-component-kit.md §2.2) ──────────────────


class BranchSummary(_Node):
    """Coding-agent's ``BranchSummaryMessageComponent(message,
    getMarkdownTheme(), outputPad)``: the marker of a summarized branch, with
    the summary's markdown behind a click or ``set_expanded``. It draws with
    the active theme, as Pi's conversation components do. A node with an
    ``id`` keeps its host component across frames, so a click's toggle lives
    as long as the node."""

    kind = "branch-summary"

    def __init__(self, summary: str, *, output_pad: int = 1, id: str = "") -> None:  # noqa: A002 - upstream's field name
        self.id = str(id)
        self.summary = str(summary)
        self.output_pad = int(output_pad)
        self.expanded = False

    def set_expanded(self, expanded: bool) -> None:
        """Upstream ``setExpanded``."""
        self.expanded = bool(expanded)

    def set_output_pad(self, output_pad: int) -> None:
        """Upstream ``setOutputPad``."""
        self.output_pad = int(output_pad)

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {}
        if self.id:
            out["id"] = self.id
        if self.output_pad != 1:
            out["outputPad"] = self.output_pad
        if self.expanded:
            out["expanded"] = True
        if self.summary:
            out["summary"] = self.summary
        return out


__all__ += ["BranchSummary"]


class CompactionSummary(_Node):
    """Coding-agent's ``CompactionSummaryMessageComponent(message,
    getMarkdownTheme(), outputPad)``: the marker of a compaction ("Compacted
    from 12,345 tokens"), with the summary's markdown behind a click or
    ``set_expanded``. It draws with the active theme, and a node with an
    ``id`` keeps its host component across frames, as
    :class:`BranchSummary` does."""

    kind = "compaction-summary"

    def __init__(self, summary: str, tokens_before: int, *, output_pad: int = 1, id: str = "") -> None:  # noqa: A002 - upstream's field name
        self.id = str(id)
        self.summary = str(summary)
        self.tokens_before = int(tokens_before)
        self.output_pad = int(output_pad)
        self.expanded = False

    def set_expanded(self, expanded: bool) -> None:
        """Upstream ``setExpanded``."""
        self.expanded = bool(expanded)

    def set_output_pad(self, output_pad: int) -> None:
        """Upstream ``setOutputPad``."""
        self.output_pad = int(output_pad)

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {}
        if self.id:
            out["id"] = self.id
        if self.output_pad != 1:
            out["outputPad"] = self.output_pad
        if self.expanded:
            out["expanded"] = True
        if self.summary:
            out["summary"] = self.summary
        out["tokensBefore"] = self.tokens_before
        return out


__all__ += ["CompactionSummary"]


class SkillInvocation(_Node):
    """Coding-agent's ``SkillInvocationMessageComponent(skillBlock,
    getMarkdownTheme(), outputPad)``: a skill's invocation, one line collapsed
    and the skill's content behind a click or ``set_expanded``. It draws with
    the active theme, and a node with an ``id`` keeps its host component
    across frames, as :class:`BranchSummary` does."""

    kind = "skill-invocation"

    def __init__(self, name: str, content: str, *, output_pad: int = 1, id: str = "") -> None:  # noqa: A002 - upstream's field name
        self.id = str(id)
        self.name = str(name)
        self.content = str(content)
        self.output_pad = int(output_pad)
        self.expanded = False

    def set_expanded(self, expanded: bool) -> None:
        """Upstream ``setExpanded``."""
        self.expanded = bool(expanded)

    def set_output_pad(self, output_pad: int) -> None:
        """Upstream ``setOutputPad``."""
        self.output_pad = int(output_pad)

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {}
        if self.id:
            out["id"] = self.id
        if self.output_pad != 1:
            out["outputPad"] = self.output_pad
        if self.expanded:
            out["expanded"] = True
        skill: dict[str, Any] = {}
        if self.name:
            skill["name"] = self.name
        if self.content:
            skill["content"] = self.content
        out["skill"] = skill
        return out


__all__ += ["SkillInvocation"]


class CustomMessage(_Node):
    """Coding-agent's ``CustomMessageComponent(message, undefined,
    getMarkdownTheme(), outputPad)``: the default rendering of an extension's
    message, "[customType]" over the content's text in a customMessageBg box.
    A message's custom renderer is a closure upstream and has no kind here;
    the message-renderer surfaces draw it. It draws with the active theme, and
    a node with an ``id`` keeps its host component across frames."""

    kind = "custom-message"

    def __init__(self, custom_type: str, text: str, *, output_pad: int = 1, id: str = "") -> None:  # noqa: A002 - upstream's field name
        self.id = str(id)
        self.custom_type = str(custom_type)
        self.text = str(text)
        self.output_pad = int(output_pad)
        self.expanded = False

    def set_expanded(self, expanded: bool) -> None:
        """Upstream ``setExpanded``; the default rendering does not draw it."""
        self.expanded = bool(expanded)

    def set_output_pad(self, output_pad: int) -> None:
        """Upstream ``setOutputPad``."""
        self.output_pad = int(output_pad)

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {}
        if self.id:
            out["id"] = self.id
        if self.output_pad != 1:
            out["outputPad"] = self.output_pad
        if self.expanded:
            out["expanded"] = True
        if self.custom_type:
            out["customType"] = self.custom_type
        if self.text:
            out["text"] = self.text
        return out


__all__ += ["CustomMessage"]


class CancellableLoader(_Node):
    """Pi-tui's ``CancellableLoader(ui, spinnerColorFn, messageColorFn,
    message, indicator)``: a :class:`Loader` that the focused
    ``tui.select.cancel`` key aborts. ``id`` is required and unique within the
    view; the host owns the abort state and the animation, and the abort
    reaches the extension as an :data:`ABORT` event of the node, which the
    view's ``focus`` must name to receive the key. Any other key goes to the
    extension, as the loader ignores it."""

    kind = "cancellable-loader"

    def __init__(
        self,
        id: str,  # noqa: A002 - upstream's field name
        message: str = "Loading...",
        *,
        spinner_color: str | None = None,
        message_color: str | None = None,
        indicator: LoaderIndicator | None = None,
        frame: int | None = None,
    ) -> None:
        self.id = str(id)
        self.loader = Loader(message, spinner_color=spinner_color, message_color=message_color, indicator=indicator, frame=frame)

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        return {"id": self.id, **self.loader._fields(enc, depth)}


class BorderedLoader(_Node):
    """Coding-agent's ``BorderedLoader(tui, theme, message, {cancellable})``: a
    loader between two borders with, when cancellable (the default), a cancel
    key hint that the focused ``tui.select.cancel`` key aborts (an
    :data:`ABORT` event). It draws with the view's theme, the one Pi hands the
    factory that builds it. ``id`` is required and unique within the view; the
    host owns the abort state and the animation."""

    kind = "bordered-loader"

    def __init__(self, id: str, message: str = "Loading...", *, cancellable: bool = True, frame: int | None = None) -> None:  # noqa: A002 - upstream's field name
        self.id = str(id)
        self.message = str(message)
        self.cancellable = bool(cancellable)
        self.frame = frame

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {"id": self.id, "message": self.message}
        if not self.cancellable:
            out["cancellable"] = False
        _put(out, "frame", self.frame)
        return out


__all__ += ["BorderedLoader", "CancellableLoader"]


class Input(_Node):
    """Pi-tui's ``Input({prompt, placeholder, placeholderStyle})``: a one-line
    text field. ``id`` is required and unique within the view. The host owns the
    value and the cursor and runs Input's own key and mouse handling once the
    view's ``focus`` names it, as the focused component of Pi's TUI takes every
    key. The extension receives ``onSubmit`` as a :data:`SUBMIT` event,
    ``onEscape`` as an :data:`ESCAPE` event, and a :data:`CHANGE` event with the
    value and ``cursor`` after each key or press that changed either.
    ``value`` is ``setValue``'s argument, applied when it differs from the one
    the previous frame sent; ``prompt`` ``None`` takes upstream's ``"> "``;
    ``placeholder_color`` is a theme foreground token for ``placeholderStyle``."""

    kind = "input"

    def __init__(
        self,
        id: str,  # noqa: A002 - upstream's field name
        value: str = "",
        *,
        prompt: str | None = None,
        placeholder: str = "",
        placeholder_color: str | None = None,
    ) -> None:
        self.id = str(id)
        self.value = str(value)
        self.prompt = prompt
        self.placeholder = str(placeholder)
        self.placeholder_color = placeholder_color

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {"id": self.id}
        if self.value:
            out["value"] = self.value
        _put(out, "prompt", self.prompt)
        if self.placeholder:
            out["placeholder"] = self.placeholder
        _put(out, "placeholderColor", self.placeholder_color)
        return out


__all__ += ["Input"]


class SlashCommand:
    """Pi-tui's ``SlashCommand`` without ``getArgumentCompletions``, which is a
    closure and cannot cross."""

    def __init__(self, name: str, description: str = "", argument_hint: str = "") -> None:
        self.name = str(name)
        self.description = str(description)
        self.argument_hint = str(argument_hint)

    def _wire(self) -> dict[str, Any]:
        out: dict[str, Any] = {"name": self.name}
        if self.description:
            out["description"] = self.description
        if self.argument_hint:
            out["argumentHint"] = self.argument_hint
        return out


class Autocomplete:
    """Pi-tui's ``CombinedAutocompleteProvider(commands, basePath)``: the slash
    commands and the directory its path completion starts from (the host's
    working directory when empty). The host runs its ``getSuggestions``,
    ``applyCompletion`` and ``shouldTriggerFileCompletion`` as the editor
    asks."""

    def __init__(self, commands: Sequence[SlashCommand] = (), base_path: str = "") -> None:
        self.commands = list(commands)
        self.base_path = str(base_path)

    def _wire(self) -> dict[str, Any]:
        out: dict[str, Any] = {}
        if self.commands:
            out["commands"] = [command._wire() for command in self.commands]
        if self.base_path:
            out["basePath"] = self.base_path
        return out


class Editor(_Node):
    """Pi-tui's ``Editor(tui, theme, {paddingX, autocompleteMaxVisible})`` with
    the setters an author calls on it: a multi-line text editor between two
    borders. ``id`` is required and unique within the view. The host owns the
    text and the cursor and runs Editor's own key and mouse handling once the
    view's ``focus`` names it. The extension receives ``onSubmit`` as a
    :data:`SUBMIT` event of the node (the text in ``value``) and a
    :data:`CHANGE` event with the text and ``cursor`` after each key or press
    that changed either. The editor shows ``max(5, floor(terminal rows * 0.3))``
    lines, the terminal's rows being the host's.

    ``value`` is ``setText``'s argument, applied when it differs from the one
    the previous frame sent. ``autocomplete_max_visible`` ``0`` takes upstream's
    5; ``border_color`` is a theme foreground token (default ``borderMuted``);
    ``history`` is the ``addToHistory`` calls, oldest first: a frame that
    extends the history the previous frame sent adds the new entries, another
    history is another editor."""

    kind = "editor"

    def __init__(
        self,
        id: str,  # noqa: A002 - upstream's field name
        value: str = "",
        *,
        padding_x: int = 0,
        autocomplete_max_visible: int = 0,
        border_color: str | None = None,
        disable_submit: bool = False,
        history: Sequence[str] = (),
        autocomplete: Autocomplete | None = None,
    ) -> None:
        self.id = str(id)
        self.value = str(value)
        self.padding_x = int(padding_x)
        self.autocomplete_max_visible = int(autocomplete_max_visible)
        self.border_color = border_color
        self.disable_submit = bool(disable_submit)
        self.history = [str(entry) for entry in history]
        self.autocomplete = autocomplete

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {"id": self.id}
        if self.value:
            out["value"] = self.value
        if self.padding_x:
            out["paddingX"] = self.padding_x
        if self.autocomplete_max_visible:
            out["autocompleteMaxVisible"] = self.autocomplete_max_visible
        _put(out, "borderColor", self.border_color)
        if self.disable_submit:
            out["disableSubmit"] = True
        if self.history:
            out["history"] = list(self.history)
        if self.autocomplete is not None:
            out["autocomplete"] = self.autocomplete._wire()
        return out


__all__ += ["Autocomplete", "Editor", "SlashCommand"]


class ScrollView(_Node):
    """Pi-tui's ``ScrollView(child, {follow, primary, overscroll, scrollbar,
    scrollbarTrackStyle, scrollbarThumbStyle, scrollbarHideDelayMs})``: one
    child that scrolls. A view is laid out without a viewport height, as Pi's
    layout engine does for a scroll node given none: the host draws the child's
    rows, padded by a column when ``scrollbar`` is ``"always"``, and a frontend
    scrolls from the state the component reports. The host keeps its component
    (and its scroll state) across frames.

    ``follow`` is ``"none"`` (default) or ``"end"``; ``overscroll`` ``"chain"``
    (default) or ``"contain"``; ``scrollbar`` ``"hidden"`` (default),
    ``"auto"`` or ``"always"``; ``scrollbar_track_color`` and
    ``scrollbar_thumb_color`` are theme foreground tokens;
    ``scrollbar_hide_delay_ms`` ``None`` takes upstream's 1000; ``scroll_top``
    is ``scrollTo``'s argument, applied when it differs from the one the
    previous frame sent."""

    kind = "scroll-view"

    def __init__(
        self,
        id: str,  # noqa: A002 - upstream's field name
        child: Node,
        *,
        follow: str = "",
        primary: bool = False,
        overscroll: str = "",
        scrollbar: str = "",
        scrollbar_track_color: str | None = None,
        scrollbar_thumb_color: str | None = None,
        scrollbar_hide_delay_ms: int | None = None,
        scroll_top: int | None = None,
    ) -> None:
        self.id = str(id)
        self.child = child
        self.follow = follow
        self.primary = bool(primary)
        self.overscroll = overscroll
        self.scrollbar = scrollbar
        self.scrollbar_track_color = scrollbar_track_color
        self.scrollbar_thumb_color = scrollbar_thumb_color
        self.scrollbar_hide_delay_ms = scrollbar_hide_delay_ms
        self.scroll_top = scroll_top

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        out: dict[str, Any] = {"id": self.id, "children": [enc.node(self.child, depth + 1)]}
        if self.follow:
            out["follow"] = self.follow
        if self.primary:
            out["primary"] = True
        if self.overscroll:
            out["overscroll"] = self.overscroll
        if self.scrollbar:
            out["scrollbar"] = self.scrollbar
        _put(out, "scrollbarTrackColor", self.scrollbar_track_color)
        _put(out, "scrollbarThumbColor", self.scrollbar_thumb_color)
        _put(out, "scrollbarHideDelayMs", self.scrollbar_hide_delay_ms)
        _put(out, "scrollTop", self.scroll_top)
        return out


class MouseRegion(_Node):
    """Pi-tui's ``MouseRegion(child, onMouse)``: mouse handling added to a child
    without changing how it draws. The child gets each event first and the
    region's handler only the events it leaves. The handler is a closure
    upstream and crosses as a description: for each event whose type is in
    ``types`` (``"press"``, ``"release"``, ``"click"``, ``"drag"``, ``"move"``,
    ``"wheel"``; every type when none is named) it returns the result below,
    and for the others no result. The host answers the terminal at once and
    reports each event the handler ran for as a :data:`MOUSE` event of the node,
    with coordinates local to the region. ``handled`` ``None`` takes true;
    ``render`` ``None`` takes upstream's per-type default. ``id`` is required
    and unique within the view."""

    kind = "mouse-region"

    def __init__(
        self,
        id: str,  # noqa: A002 - upstream's field name
        child: Node,
        *,
        types: Sequence[str] = (),
        handled: bool | None = None,
        capture: bool = False,
        focus: bool = False,
        render: bool | None = None,
    ) -> None:
        self.id = str(id)
        self.child = child
        self.types = list(types)
        self.handled = handled
        self.capture = bool(capture)
        self.focus = bool(focus)
        self.render = render

    def _fields(self, enc: _Encoder, depth: int) -> dict[str, Any]:
        region: dict[str, Any] = {}
        if self.types:
            region["types"] = list(self.types)
        _put(region, "handled", self.handled)
        if self.capture:
            region["capture"] = True
        if self.focus:
            region["focus"] = True
        _put(region, "render", self.render)
        return {"id": self.id, "children": [enc.node(self.child, depth + 1)], "region": region}


__all__ += ["MouseRegion", "ScrollView"]
