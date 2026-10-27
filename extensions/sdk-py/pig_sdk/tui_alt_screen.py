"""Pi's ``TuiAltScreen`` (pi-tui ``tui-alt-screen.ts``) for a Python extension: the alternate-screen TUI with a scrollable, application-owned viewport.

The host runs the layout, scroll, selection, overlay, focus and render logic (its Go port of the class). This object is the extension's proxy for
it: the extension supplies the :class:`Terminal` and the child :class:`Component` objects, and the host calls back into the extension for everything
the extension owns (terminal output, component render / input / mouse / focus, input listeners, the options' callbacks). Node serves Pi's own class.

Async contract (Pi's promises, kept as blocking calls): :meth:`TuiAltScreen.copy_active_selection_to_clipboard`, :meth:`TuiAltScreen.query_terminal_colors`,
``Terminal.drain_input`` and the ``copy_selection`` option block the calling thread and return a value or raise; a terminal or option callback that is an
``async def`` is awaited on the thread the host's callback runs on. Every other member is synchronous in Pi and a host round trip here. Terminal output
reaches the terminal in write order, each write acknowledged before the next.

Python names: Pi's ``viewportTop`` is :attr:`TuiAltScreen.viewport_top`, ``scrollBy`` is :meth:`TuiAltScreen.scroll_by`, ``[VIEWPORT_TUI]`` is
:attr:`TuiAltScreen.viewport_tui`, ``onDebug`` is the property :attr:`TuiAltScreen.on_debug`. The reads that are properties in Pi (``mode``, ``children``,
``terminal``, ``viewport_top``, ``is_following_output``, ``full_redraws``, ``has_overlay_entries``) are properties here; ``children`` is a snapshot.

A child component is any object with ``render(width) -> list[str]`` and, optionally, ``invalidate()``, ``handle_input(data)``,
``handle_mouse(event: MouseEvent) -> TuiMouseEventResult | None``, a ``focused`` attribute (Pi's Focusable) and ``wants_key_release``. The host is never
asked to deliver input or mouse events to a component that lacks the handler. :meth:`TuiAltScreen.get_focused_component` returns the very object given to
:meth:`TuiAltScreen.set_focus`.

The screen ends with the connection: the host disposes it, and no callback runs afterwards.
"""

from __future__ import annotations

import asyncio
import inspect
import threading
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any, Literal, NotRequired, Protocol, TypedDict

if TYPE_CHECKING:
    from . import Context, Extension, MouseEvent


class RgbColor(TypedDict):
    """pi-tui's ``RgbColor``."""

    r: int
    g: int
    b: int


class TerminalColors(TypedDict, total=False):
    """pi-tui's ``TerminalColors``: what the terminal reported for its theme. Colors it did not report are absent; ``palette`` is set only when all 16 arrived."""

    foreground: RgbColor
    background: RgbColor
    palette: list[RgbColor]


class ProgramStatus(TypedDict):
    """pi-tui's ``ProgramStatus`` (OSC 7501): ``state`` is ``idle``, ``working``, ``blocked``, ``done``, ``error`` or ``clear``; ``kind`` is ``permission``, ``question`` or ``auth``."""

    state: str
    app: NotRequired[str]
    kind: NotRequired[str]
    message: NotRequired[str]


class TuiMouseEventResult(TypedDict, total=False):
    """pi-tui's ``TuiMouseEventResult``: what a component's mouse handler answers."""

    handled: bool
    capture: bool
    focus: bool
    render: bool


class TuiInputListenerResult(TypedDict, total=False):
    """pi-tui's ``TuiInputListenerResult``: ``consume`` stops the input here; ``data`` replaces it for what follows."""

    consume: bool
    data: str


class TuiOverlayOptions(TypedDict, total=False):
    """pi-tui's ``OverlayOptions`` as the host takes them. ``visible`` (a callback) has no wire form and is rejected."""

    width: int | float | str
    minWidth: int | float
    maxHeight: int | float | str
    anchor: Literal["center", "top-left", "top-right", "bottom-left", "bottom-right", "top-center", "bottom-center", "left-center", "right-center"]
    offsetX: int | float
    offsetY: int | float
    row: int | float | str
    col: int | float | str
    margin: int | float | dict[str, int | float]
    nonCapturing: bool


class OverlayBounds(TypedDict):
    """pi-tui's ``OverlayBounds``: the last rendered terminal-relative overlay rectangle."""

    row: int
    col: int
    width: int
    height: int


class Terminal(Protocol):
    """pi-tui's ``Terminal``: the extension's terminal. ``columns``, ``rows`` and ``kitty_protocol_active`` are attributes or properties.

    ``restore_on_exit(before, after)`` is optional; the host calls it only when the terminal has it."""

    columns: int
    rows: int
    kitty_protocol_active: bool

    def start(self, on_input: Callable[[str], None], on_resize: Callable[[], None]) -> None: ...

    def stop(self) -> None: ...

    def drain_input(self, max_ms: int | None = None, idle_ms: int | None = None) -> None: ...

    def write(self, data: str) -> None: ...

    def move_by(self, lines: int) -> None: ...

    def hide_cursor(self) -> None: ...

    def show_cursor(self) -> None: ...

    def clear_line(self) -> None: ...

    def clear_from_cursor(self) -> None: ...

    def clear_screen(self) -> None: ...

    def set_title(self, title: str) -> None: ...

    def set_progress(self, active: bool) -> None: ...

    def set_program_status(self, status: ProgramStatus) -> None: ...


class Component(Protocol):
    """pi-tui's ``Component``: ``render`` is required. ``invalidate``, ``handle_input``, ``handle_mouse``, a ``focused`` attribute and ``wants_key_release`` are optional (see the module doc)."""

    def render(self, width: int) -> list[str]: ...


@dataclass(kw_only=True)
class TuiAltScreenOptions:
    """pi-tui's ``TuiAltScreenOptions``. ``None`` is Pi's undefined."""

    #: Logical lines moved for each mouse-wheel event (default 1), or ``"auto"``. Alt+wheel moves five times as far.
    wheel_scroll_lines: int | float | Literal["auto"] | None = None
    #: Capture mouse events for viewport scrolling and application-owned text selection.
    mouse: bool | None = None
    #: Style a non-current transcript search match.
    search_match_style: Callable[[str], str] | None = None
    #: Style the current transcript search match.
    search_current_match_style: Callable[[str], str] | None = None
    #: Style a transcript search navigation button.
    search_navigation_button_style: Callable[[str, bool], str] | None = None
    #: Render a clickable jump-to-end label.
    scroll_to_end_indicator: Callable[[], str] | None = None
    #: Open an OSC 8 hyperlink activated with a primary-button click.
    open_url: Callable[[str], None] | None = None
    #: Handle an unmodified secondary-button press for clipboard paste.
    on_right_click_paste: Callable[[], None] | None = None
    #: Copy selected text to the clipboard on mouse release (default True).
    copy_on_select: bool | None = None
    #: Copy selected text to the system clipboard: ``True`` on success, an error message to display, or ``False`` for a generic error. Omitted: an OSC 52 write.
    copy_selection: Callable[[str], bool | str] | None = None


_OVERLAY_OPTION_KEYS = frozenset(("width", "minWidth", "maxHeight", "anchor", "offsetX", "offsetY", "row", "col", "margin", "nonCapturing"))
_NO_TARGET: Any = object()


def _settle(value: Any) -> Any:
    """The value of a callback result; an awaitable is awaited on this (a request's own) thread, as Pi awaits its promises."""
    if inspect.isawaitable(value):

        async def wait() -> Any:
            return await value

        return asyncio.run(wait())
    return value


def _caps(component: Any) -> dict[str, bool]:
    return {
        "input": callable(getattr(component, "handle_input", None)),
        "mouse": callable(getattr(component, "handle_mouse", None)),
        "focusable": hasattr(component, "focused"),
        "wantsKeyRelease": bool(getattr(component, "wants_key_release", False)),
    }


def _mouse_wire(event: "MouseEvent | Mapping[str, Any]") -> dict[str, Any]:
    if isinstance(event, Mapping):
        return dict(event)
    wire: dict[str, Any] = {
        "type": event.type, "button": event.button, "x": event.x, "y": event.y, "screenX": event.screen_x, "screenY": event.screen_y,
        "width": event.width, "height": event.height, "shift": event.shift, "alt": event.alt, "ctrl": event.ctrl,
    }
    if event.type == "wheel" or event.wheel_delta:
        wire["wheelDelta"] = event.wheel_delta
    if event.click_count:
        wire["clickCount"] = event.click_count
    return wire


def _overlay_wire(options: Mapping[str, Any]) -> dict[str, Any]:
    for key in options:
        if key == "visible":
            raise ValueError("overlay option 'visible' is a callback and has no wire form; show or hide the overlay with its handle instead")
        if key not in _OVERLAY_OPTION_KEYS:
            raise ValueError(f"unknown overlay option {key!r}")
    return dict(options)


class _TuiAltScreens:
    """The extension's live alternate-screen proxies, by id."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._by_id: dict[str, TuiAltScreen] = {}
        self._closed = False
        self._next = 0

    def next_id(self) -> str:
        with self._lock:
            self._next += 1
            return f"t{self._next}"

    def add(self, screen: "TuiAltScreen") -> bool:
        with self._lock:
            if self._closed:
                return False
            self._by_id[screen._id] = screen
            return True

    def remove(self, screen_id: str) -> None:
        with self._lock:
            self._by_id.pop(screen_id, None)

    def close_all(self) -> None:
        """Forget every screen without telling the host: it disposes them as the connection closes."""
        with self._lock:
            self._closed = True
            self._by_id.clear()

    def dispatch(self, args: dict[str, Any]) -> Any:
        """Answer one host ``tui.alt.cb`` request; the request's own thread calls it, so a callback may call the host."""
        with self._lock:
            screen = self._by_id.get(str(args.get("id") or ""))
        if screen is None:
            raise RuntimeError(f"unknown TuiAltScreen {args.get('id')}")
        return screen._callback(args)


class TuiAltOverlayHandle:
    """pi-tui's ``OverlayHandle`` of an overlay :meth:`TuiAltScreen.show_overlay` mounted.

    ``hide`` removes the overlay permanently; ``set_hidden`` hides or shows it; ``focus`` and ``unfocus`` move keyboard focus. ``unfocus`` takes Pi's
    ``{target}`` as ``target``: absent is Pi's default release, ``None`` focuses nothing, a component the screen holds focuses it. Each control is a
    host call and returns after the host applied it; ``is_hidden``, ``is_focused`` and ``get_bounds`` read the host's current state."""

    def __init__(self, screen: "TuiAltScreen", key: str) -> None:
        self._screen = screen
        self._key = key

    def _control(self, action: str, **fields: Any) -> dict[str, Any]:
        return self._screen._call("overlay", {"key": self._key, "action": action, **fields})

    def hide(self) -> None:
        self._control("hide")

    def set_hidden(self, hidden: bool) -> None:
        self._control("setHidden", hidden=bool(hidden))

    def is_hidden(self) -> bool:
        return bool(self._control("state").get("hidden"))

    def focus(self) -> None:
        self._control("focus")

    def unfocus(self, target: Any = _NO_TARGET) -> None:
        if target is _NO_TARGET:
            self._control("unfocus")
        elif target is None:
            self._control("unfocus", target=None)
        else:
            self._control("unfocus", target={"key": self._screen._held_key(target)})

    def is_focused(self) -> bool:
        return bool(self._control("state").get("focused"))

    def get_bounds(self) -> OverlayBounds | None:
        bounds = self._control("state").get("bounds")
        return dict(bounds) if isinstance(bounds, dict) else None  # type: ignore[return-value]


class TuiAltScreen:
    """Pi's ``TuiAltScreen``. ``TuiAltScreen(ctx, terminal, show_hardware_cursor=None, log_directory=None, options=None)`` creates the screen in the host.

    ``ctx`` is the context of the code that creates it; the screen's later host calls are the extension's own and do not end with that request."""

    def __init__(self, ctx: "Context", terminal: Terminal, show_hardware_cursor: bool | None = None, log_directory: str | None = None, options: TuiAltScreenOptions | None = None) -> None:
        extension = ctx.extension
        self._extension: Extension = extension
        self._terminal = terminal
        self._lock = threading.Lock()
        self._keys: dict[int, str] = {}
        self._objects: dict[str, Any] = {}
        self._children: list[Any] = []
        self._listeners: dict[str, Callable[[str], Any]] = {}
        self._listener_seq = 0
        self._scheme_listeners: list[Callable[[str], Any]] = []
        self._on_debug: Callable[[], Any] | None = None
        self._late_colors: dict[str, Callable[[TerminalColors], Any]] = {}
        self._query_seq = 0
        options = options or TuiAltScreenOptions()
        self._options: dict[str, Callable[..., Any]] = {
            name: fn
            for name, fn in (
                ("copySelection", options.copy_selection), ("openUrl", options.open_url), ("searchMatchStyle", options.search_match_style),
                ("searchCurrentMatchStyle", options.search_current_match_style), ("searchNavigationButtonStyle", options.search_navigation_button_style),
                ("scrollToEndIndicator", options.scroll_to_end_indicator), ("onRightClickPaste", options.on_right_click_paste),
            )
            if fn is not None
        }
        wire_options: dict[str, Any] = {}
        if options.wheel_scroll_lines is not None:
            wire_options["wheelScrollLines"] = options.wheel_scroll_lines
        if options.mouse is not None:
            wire_options["mouse"] = options.mouse
        if options.copy_on_select is not None:
            wire_options["copyOnSelect"] = options.copy_on_select
        if self._options:
            wire_options["callbacks"] = list(self._options)
        args: dict[str, Any] = {"id": extension._tui_alt.next_id(), "op": "create"}
        self._id: str = args["id"]
        if show_hardware_cursor is not None:
            args["showHardwareCursor"] = bool(show_hardware_cursor)
        if log_directory is not None:
            args["logDirectory"] = log_directory
        if wire_options:
            args["options"] = wire_options
        args["terminal"] = self._terminal_block()
        # The host may call back from the moment it knows the screen, which can be before the call returns.
        if not extension._tui_alt.add(self):
            raise RuntimeError("Extension connection is closed")
        try:
            ctx._call("tui.alt", args)
        except BaseException:
            extension._tui_alt.remove(self._id)
            raise

    # Host calls ---------------------------------------------------------------

    def _terminal_block(self) -> dict[str, Any]:
        terminal = self._terminal
        return {"columns": int(terminal.columns), "rows": int(terminal.rows), "kitty": bool(terminal.kitty_protocol_active)}

    def _call(self, op: str, fields: Mapping[str, Any] | None = None) -> dict[str, Any]:
        args: dict[str, Any] = {"id": self._id, "op": op}
        if fields:
            args.update(fields)
        args["terminal"] = self._terminal_block()
        result = self._extension._call("tui.alt", args).get("result")
        return result if isinstance(result, dict) else {}

    def _state(self) -> dict[str, Any]:
        return self._call("state")

    def _key_of(self, component: Any) -> str:
        """The key the host knows ``component`` by: allocated at first sight, the same for the same object."""
        with self._lock:
            key = self._keys.get(id(component))
            if key is None:
                key = f"c{len(self._keys) + 1}"
                self._keys[id(component)] = key
                self._objects[key] = component  # holds the object, so its id is never reused
            return key

    def _held_key(self, component: Any) -> str:
        with self._lock:
            key = self._keys.get(id(component))
        if key is None:
            raise ValueError("unfocus(target) requires None or a component this screen holds")
        return key

    def _component_fields(self, component: Any) -> dict[str, Any]:
        return {"key": self._key_of(component), "caps": _caps(component)}

    # Pi's reads ---------------------------------------------------------------

    @property
    def mode(self) -> Literal["fullscreen"]:
        return "fullscreen"

    @property
    def viewport_tui(self) -> Literal[True]:
        """Pi's ``[VIEWPORT_TUI]``: marks a viewport TUI (``isViewportTUI``)."""
        return True

    @property
    def terminal(self) -> Terminal:
        return self._terminal

    @terminal.setter
    def terminal(self, terminal: Terminal) -> None:
        self._terminal = terminal

    @property
    def children(self) -> list[Any]:
        """The children added with :meth:`add_child`, in order. A snapshot: change the screen with ``add_child``, ``remove_child`` and ``clear``."""
        with self._lock:
            return list(self._children)

    @property
    def viewport_top(self) -> int:
        return int(self._state().get("viewportTop") or 0)

    @property
    def is_following_output(self) -> bool:
        return bool(self._state().get("isFollowingOutput"))

    @property
    def full_redraws(self) -> int:
        return int(self._state().get("fullRedraws") or 0)

    @property
    def has_overlay_entries(self) -> bool:
        return bool(self._state().get("hasOverlayEntries"))

    def has_overlay(self) -> bool:
        return bool(self._state().get("hasOverlay"))

    def has_active_selection(self) -> bool:
        """Whether the fullscreen viewport has a non-empty active text selection."""
        return bool(self._state().get("hasActiveSelection"))

    def get_copy_on_select(self) -> bool:
        return bool(self._state().get("copyOnSelect"))

    def get_show_hardware_cursor(self) -> bool:
        return bool(self._state().get("showHardwareCursor"))

    def get_clear_on_shrink(self) -> bool:
        return bool(self._state().get("clearOnShrink"))

    def get_focused_component(self) -> Any:
        """The very object given to :meth:`set_focus` (or an overlay or child the host focused), or ``None``."""
        key = self._state().get("focusedKey")
        if key is None:
            return None
        with self._lock:
            return self._objects.get(key)

    @property
    def on_debug(self) -> Callable[[], Any] | None:
        """Pi's ``onDebug``: called on the debug key (Shift+Ctrl+D) before input reaches the focused component. ``None`` clears it."""
        return self._on_debug

    @on_debug.setter
    def on_debug(self, callback: Callable[[], Any] | None) -> None:
        self._on_debug = callback
        self._call("setOnDebug", {"enabled": callback is not None})

    # Children, focus, overlays -------------------------------------------------

    def add_child(self, component: Any) -> None:
        self._call("addChild", self._component_fields(component))
        with self._lock:
            self._children.append(component)

    def remove_child(self, component: Any) -> None:
        with self._lock:
            index = next((i for i, child in enumerate(self._children) if child is component), -1)
            key = self._keys.get(id(component))
        if index == -1 or key is None:
            return
        self._call("removeChild", {"key": key})
        with self._lock:
            index = next((i for i, child in enumerate(self._children) if child is component), -1)
            if index != -1:
                del self._children[index]

    def clear(self) -> None:
        self._call("clear")
        with self._lock:
            self._children = []

    def set_layout_root(self, component: Any | None) -> None:
        """Pi's ``setLayoutRoot``: the component rendered instead of the children; ``None`` returns to the children."""
        self._call("setLayoutRoot", {"key": None} if component is None else self._component_fields(component))

    def set_focus(self, component: Any | None) -> None:
        self._call("setFocus", {"key": None} if component is None else self._component_fields(component))

    def show_overlay(self, component: Any, options: TuiOverlayOptions | Mapping[str, Any] | None = None) -> TuiAltOverlayHandle:
        fields = self._component_fields(component)
        if options is not None:
            fields["options"] = _overlay_wire(options)
        self._call("showOverlay", fields)
        return TuiAltOverlayHandle(self, fields["key"])

    def hide_overlay(self) -> None:
        """Hide the topmost overlay and restore the previous focus."""
        self._call("hideOverlay")

    # Lifecycle and rendering ---------------------------------------------------

    def start(self) -> None:
        """Start the terminal (``Terminal.start`` runs), enter the alternate screen, hide the cursor and request the first render."""
        self._call("start")

    def stop(self, preserve_screen: bool = False) -> None:
        """Show the cursor, stop the terminal (``Terminal.stop`` runs) and leave the alternate screen. ``preserve_screen`` leaves the renderer's output in place for another TUI taking over the terminal."""
        self._call("stop", {"preserveScreen": True} if preserve_screen else None)

    def render_now(self, force: bool = False) -> None:
        self._call("renderNow", {"force": True} if force else None)

    def request_render(self, force: bool = False) -> None:
        self._call("requestRender", {"force": True} if force else None)

    def invalidate(self) -> None:
        self._call("invalidate")

    def render(self, width: int) -> list[str]:
        """The lines of the root (the layout root, else the children) rendered at ``width``."""
        return list(self._call("render", {"width": width}).get("lines") or [])

    def get_screen_lines(self) -> list[str]:
        """The lines of the last rendered frame, one per terminal row, as written to the terminal."""
        return list(self._call("getScreenLines").get("lines") or [])

    def scroll_by(self, lines: int) -> None:
        self._call("scrollBy", {"lines": lines})

    def scroll_to_top(self) -> None:
        self._call("scrollToTop")

    def scroll_to_bottom(self) -> None:
        self._call("scrollToBottom")

    def flash(self, message: str, duration_ms: int | None = None) -> None:
        """Show a transient message in the alternate-screen flash stack."""
        self._call("flash", {"message": message} if duration_ms is None else {"message": message, "durationMs": duration_ms})

    def set_wheel_scroll_lines(self, lines: int | float | Literal["auto"]) -> None:
        self._call("setWheelScrollLines", {"lines": lines})

    def set_copy_on_select(self, enabled: bool) -> None:
        self._call("setCopyOnSelect", {"enabled": bool(enabled)})

    def set_show_hardware_cursor(self, enabled: bool) -> None:
        self._call("setShowHardwareCursor", {"enabled": bool(enabled)})

    def set_clear_on_shrink(self, enabled: bool) -> None:
        self._call("setClearOnShrink", {"enabled": bool(enabled)})

    def copy_active_selection_to_clipboard(self) -> bool:
        """Copy the active text selection, if any, through the configured selection clipboard path. Blocks until it settles."""
        return bool(self._call("copyActiveSelectionToClipboard").get("value"))

    def reset_text_selection(self) -> None:
        """Drop the text selection and multi-click history."""
        self._call("resetTextSelection")

    def handle_mouse(self, event: "MouseEvent | Mapping[str, Any]") -> TuiMouseEventResult | None:
        """Dispatch a mouse event to the screen's components; ``None`` when none handled it."""
        result = self._call("handleMouse", {"event": _mouse_wire(event)}).get("result")
        return dict(result) if isinstance(result, dict) else None  # type: ignore[return-value]

    # Input listeners and terminal colors ---------------------------------------

    def add_input_listener(self, listener: Callable[[str], TuiInputListenerResult | Mapping[str, Any] | None]) -> Callable[[], None]:
        """Add a listener that sees each terminal input before the focused component. It returns ``None``, or ``{"consume": True}`` to stop the input, or ``{"data": s}`` to replace it. Returns the function that removes it. Adding a listener already added changes nothing."""
        with self._lock:
            if not any(known == listener for known in self._listeners.values()):
                self._listener_seq += 1
                listener_id = f"l{self._listener_seq}"
                self._listeners[listener_id] = listener
            else:
                listener_id = ""
        if listener_id:
            try:
                self._call("addInputListener", {"listenerId": listener_id})
            except BaseException:
                with self._lock:
                    self._listeners.pop(listener_id, None)
                raise
        return lambda: self.remove_input_listener(listener)

    def remove_input_listener(self, listener: Callable[[str], Any]) -> None:
        with self._lock:
            listener_id = next((lid for lid, known in self._listeners.items() if known == listener), None)
        if listener_id is None:
            return
        self._call("removeInputListener", {"listenerId": listener_id})
        with self._lock:
            self._listeners.pop(listener_id, None)

    def on_terminal_color_scheme_change(self, listener: Callable[[str], Any]) -> Callable[[], None]:
        """Subscribe to the terminal's ``"dark"`` / ``"light"`` scheme reports (see :meth:`set_terminal_color_scheme_notifications`). Returns the function that unsubscribes."""
        with self._lock:
            if not any(known == listener for known in self._scheme_listeners):
                self._scheme_listeners.append(listener)

        def unsubscribe() -> None:
            with self._lock:
                self._scheme_listeners = [known for known in self._scheme_listeners if known != listener]

        return unsubscribe

    def set_terminal_color_scheme_notifications(self, enabled: bool) -> None:
        self._call("setTerminalColorSchemeNotifications", {"enabled": bool(enabled)})

    def query_terminal_colors(self, timeout_ms: int, on_late_reply: Callable[[TerminalColors], Any] | None = None) -> TerminalColors:
        """Query the terminal's theme colors. Blocks until the DA1 reply or ``timeout_ms``; colors the terminal did not report are absent. ``on_late_reply`` receives the colors when the reply arrives after the timeout."""
        fields: dict[str, Any] = {"timeoutMs": timeout_ms, "onLateReply": on_late_reply is not None}
        if on_late_reply is not None:
            # The callback stays registered after the query returns: a reply after the timeout names the query (lateColors).
            with self._lock:
                self._query_seq += 1
                query_id = f"q{self._query_seq}"
                self._late_colors[query_id] = on_late_reply
            fields["queryId"] = query_id
        colors = self._call("queryTerminalColors", fields).get("colors")
        return colors if isinstance(colors, dict) else {}  # type: ignore[return-value]

    # Host callbacks (tui.alt.cb) -----------------------------------------------

    def _on_input(self, data: str) -> None:
        self._call("input", {"data": data})

    def _on_resize(self) -> None:
        self._call("resize")

    def _component(self, args: Mapping[str, Any]) -> Any:
        with self._lock:
            component = self._objects.get(str(args.get("key") or ""))
        if component is None:
            raise RuntimeError(f"unknown component {args.get('key')}")
        return component

    def _callback(self, args: Mapping[str, Any]) -> Any:
        kind = args.get("kind")
        if kind == "terminal":
            return self._terminal_callback(args)
        if kind == "render":
            lines = self._component(args).render(int(args.get("width") or 0))
            if not isinstance(lines, (list, tuple)) or not all(isinstance(line, str) for line in lines):
                raise TypeError("render must return a list of strings")
            return {"lines": list(lines)}
        if kind == "input":
            handler = getattr(self._component(args), "handle_input", None)
            if callable(handler):
                handler(str(args.get("data") or ""))
            return {}
        if kind == "mouse":
            return {"result": self._mouse_callback(args)}
        if kind == "focus":
            component = self._component(args)
            if hasattr(component, "focused"):
                try:
                    component.focused = bool(args.get("focused"))
                except AttributeError:  # a read-only attribute is the component's own choice  # nosec B110
                    pass
            return {}
        if kind == "invalidate":
            invalidate = getattr(self._component(args), "invalidate", None)
            if callable(invalidate):
                invalidate()
            return {}
        if kind == "listener":
            return self._listener_callback(args)
        if kind == "debug":
            callback = self._on_debug
            if callback is not None:
                callback()
            return {}
        if kind == "colorScheme":
            with self._lock:
                listeners = list(self._scheme_listeners)
            for listener in listeners:
                listener(str(args.get("scheme") or ""))
            return {}
        if kind == "lateColors":
            with self._lock:
                callback = self._late_colors.pop(str(args.get("queryId") or ""), None)
            if callback is not None:
                callback(args.get("colors") or {})
            return {}
        if kind == "option":
            return self._option_callback(args)
        raise RuntimeError(f"unknown tui.alt.cb kind {kind!r}")

    def _terminal_callback(self, args: Mapping[str, Any]) -> dict[str, Any]:
        terminal = self._terminal
        method = args.get("method")
        if method == "start":
            terminal.start(self._on_input, self._on_resize)
        elif method == "stop":
            terminal.stop()
        elif method == "write":
            terminal.write(str(args.get("data", "")))
        elif method == "moveBy":
            terminal.move_by(int(args.get("lines") or 0))
        elif method == "hideCursor":
            terminal.hide_cursor()
        elif method == "showCursor":
            terminal.show_cursor()
        elif method == "clearLine":
            terminal.clear_line()
        elif method == "clearFromCursor":
            terminal.clear_from_cursor()
        elif method == "clearScreen":
            terminal.clear_screen()
        elif method == "setTitle":
            terminal.set_title(str(args.get("title", "")))
        elif method == "setProgress":
            terminal.set_progress(bool(args.get("active")))
        elif method == "setProgramStatus":
            terminal.set_program_status(args.get("status"))  # type: ignore[arg-type]
        elif method == "restoreOnExit":
            restore = getattr(terminal, "restore_on_exit", None)
            if callable(restore):
                restore(args.get("before"), args.get("after"))
        elif method == "drainInput":
            _settle(terminal.drain_input(args.get("maxMs"), args.get("idleMs")))
        else:
            raise RuntimeError(f"unknown terminal method {method!r}")
        return {}

    def _mouse_callback(self, args: Mapping[str, Any]) -> dict[str, Any] | None:
        from . import MouseEvent

        component = self._component(args)
        handler = getattr(component, "handle_mouse", None)
        event = MouseEvent._from_wire(args.get("event"))
        if not callable(handler) or event is None:
            return None
        result = handler(event)
        if result is None:
            return None
        if not isinstance(result, Mapping):
            raise TypeError("handle_mouse must return a mapping (handled, capture, focus, render) or None")
        return dict(result)

    def _listener_callback(self, args: Mapping[str, Any]) -> dict[str, Any]:
        with self._lock:
            listener = self._listeners.get(str(args.get("listenerId") or ""))
        if listener is None:
            return {}
        result = listener(str(args.get("data", "")))
        if result is None:
            return {}
        if not isinstance(result, Mapping):
            raise TypeError("an input listener must return a mapping (consume, data) or None")
        reply: dict[str, Any] = {}
        if "consume" in result:
            reply["consume"] = bool(result["consume"])
        if "data" in result:
            reply["data"] = str(result["data"])
        return reply

    def _option_callback(self, args: Mapping[str, Any]) -> dict[str, Any]:
        name = str(args.get("name") or "")
        callback = self._options.get(name)
        if callback is None:
            raise RuntimeError(f"TuiAltScreen option {name!r} is not set")
        call_args = args.get("args") or []
        if name == "copySelection":
            value = _settle(callback(str(call_args[0])))
            return {"value": value if isinstance(value, (bool, str)) else bool(value)}
        if name in ("openUrl", "onRightClickPaste"):
            _settle(callback(*([str(call_args[0])] if name == "openUrl" else [])))
            return {"value": None}
        if name == "searchNavigationButtonStyle":
            return {"value": str(callback(str(call_args[0]), bool(call_args[1])))}
        if name == "scrollToEndIndicator":
            return {"value": str(callback())}
        return {"value": str(callback(str(call_args[0])))}
