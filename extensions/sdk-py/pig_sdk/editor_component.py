"""``ctx.ui.setEditorComponent`` for Python extensions.

Pi's factory returns a CustomEditor subclass: the host's keys reach its
``handleInput``, its ``render`` output is the editor on screen, and its
``super`` calls reach the default editor. Subclass :class:`EditorComponent`,
whose methods call the host's default editor, override the ones that differ and
call ``super()`` for the default behaviour.

Every method runs on one worker thread per installed editor, in the order the
host produced the events. After each event the SDK renders the component and
sends the frame when it changed.
"""

from __future__ import annotations

import dataclasses
import queue
import threading
from typing import TYPE_CHECKING, Any, Callable

if TYPE_CHECKING:
    from . import Extension
    from .kit import Autocomplete

# The app actions Pi's CustomEditor.handleInput dispatches to a handler of its own (custom-editor.ts).
_ACTION_INTERRUPT = "app.interrupt"
_ACTION_EXIT = "app.exit"
_ACTION_PASTE_IMAGE = "app.clipboard.pasteImage"
# The hooks entry of a component that sets its own on_extension_shortcut.
_HOOK_SHORTCUT = "extensionShortcut"


@dataclasses.dataclass(frozen=True)
class EditorStatus:
    """The working, compaction, summarization or retry status the host shows while the editor embeds it
    (:attr:`EditorComponent.embed_working_status`): Pi's StatusIndicator as the host snapshots it. The host advances the
    animation and sends a snapshot with each frame (D94), so ``frame`` is the current frame of ``frames``."""

    kind: str
    message: str
    frames: tuple[str, ...]
    frame: int
    spinner_color: str
    message_color: str
    indicator_verbatim: bool


class EditorBase:
    """The host's default editor, the ``super`` of an :class:`EditorComponent`. Every method is a call to the host's editor, valid once the factory has returned."""

    def __init__(self, extension: "Extension", key: str) -> None:
        self._extension = extension
        self._key = key
        #: CustomEditor's onEscape: the handler of the interrupt key while no autocomplete shows, in place of the host's.
        #: None keeps the host's.
        self.on_escape: Callable[[], None] | None = None
        #: CustomEditor's onCtrlD: the handler of the exit key on an empty editor, in place of the host's.
        self.on_ctrl_d: Callable[[], None] | None = None
        #: CustomEditor's onPasteImage: the handler of the paste-image key, in place of the host's.
        self.on_paste_image: Callable[[], None] | None = None
        #: CustomEditor's onExtensionShortcut: it runs first for every key, and a key it reports handled (returns true)
        #: goes no further. It replaces the host's, which runs the extensions' shortcuts.
        self.on_extension_shortcut: Callable[[str], bool] | None = None
        #: CustomEditor's actionHandlers: the handlers of the app actions the component handles itself, by Pi's action
        #: id ("app.clear"), replacing the host's handler for the action. The dict is live.
        self.action_handlers: dict[str, Callable[[], None]] = {}

    def _op(self, op: str, args: dict[str, Any] | None = None) -> dict[str, Any]:
        reply = self._extension._call("ui.editor.base", {"key": self._key, "op": op, "args": args or {}})
        return reply.get("result") or {}

    def on_action(self, action: str, handler: Callable[[], None]) -> None:
        """CustomEditor's onAction: register ``handler`` for an app action."""
        self.action_handlers[action] = handler

    def _hooked(self) -> list[str]:
        """The app actions whose handler the component set, for the host to answer instead of running. Pi's handleInput
        consults onEscape or the interrupt entry, onCtrlD or the exit entry, onPasteImage alone, and every other entry."""
        ids: list[str] = []
        if self.on_escape is not None or self.action_handlers.get(_ACTION_INTERRUPT) is not None:
            ids.append(_ACTION_INTERRUPT)
        if self.on_ctrl_d is not None or self.action_handlers.get(_ACTION_EXIT) is not None:
            ids.append(_ACTION_EXIT)
        if self.on_paste_image is not None:
            ids.append(_ACTION_PASTE_IMAGE)
        for action in sorted(self.action_handlers):
            if self.action_handlers[action] is not None and action not in (_ACTION_INTERRUPT, _ACTION_EXIT, _ACTION_PASTE_IMAGE):
                ids.append(action)
        if self.on_extension_shortcut is not None:
            ids.append(_HOOK_SHORTCUT)
        return ids

    def _handler(self, action: str) -> Callable[[], None] | None:
        if action == _ACTION_INTERRUPT and self.on_escape is not None:
            return self.on_escape
        if action == _ACTION_EXIT and self.on_ctrl_d is not None:
            return self.on_ctrl_d
        if action == _ACTION_PASTE_IMAGE:
            return self.on_paste_image
        return self.action_handlers.get(action)

    def handle_input(self, data: str) -> None:
        """The default editor's handleInput, CustomEditor's: the component's on_extension_shortcut first, then the host's
        extension shortcuts, the paste-image, interrupt and exit actions and the other app actions, each through the
        handler the component set for it when it set one, then the editing."""
        shortcut = self.on_extension_shortcut
        if shortcut is not None and shortcut(data):
            return
        args: dict[str, Any] = {"data": data}
        hooks = self._hooked()
        if hooks:
            args["hooks"] = hooks
        hook = self._op("handleInput", args).get("hook")
        if hook:
            handler = self._handler(str(hook))
            if handler is not None:
                handler()

    def handle_mouse(self, event: dict[str, Any]) -> None:
        self._op("handleMouse", {"event": event})

    def render(self, width: int) -> list[str]:
        return [str(line) for line in self._op("render", {"width": width}).get("lines") or []]

    def set_text(self, text: str) -> None:
        self._op("setText", {"text": text})

    def insert_text_at_cursor(self, text: str) -> None:
        self._op("insertTextAtCursor", {"text": text})

    def add_to_history(self, text: str) -> None:
        self._op("addToHistory", {"text": text})

    def get_text(self) -> str:
        return str(self._op("getText").get("text", ""))

    def get_expanded_text(self) -> str:
        """The text with paste markers expanded."""
        return str(self._op("getExpandedText").get("text", ""))

    def get_lines(self) -> list[str]:
        return [str(line) for line in self._op("getLines").get("lines") or []]

    def get_cursor(self) -> dict[str, int]:
        """The cursor as ``{"line": ..., "col": ...}``: a logical line and a UTF-16 column."""
        cursor = self._op("getCursor")
        return {"line": int(cursor.get("line", 0)), "col": int(cursor.get("col", 0))}

    def is_showing_autocomplete(self) -> bool:
        return bool(self._op("isShowingAutocomplete").get("value", False))

    def set_padding_x(self, padding: int) -> None:
        self._op("setPaddingX", {"n": padding})

    def set_autocomplete_max_visible(self, max_visible: int) -> None:
        self._op("setAutocompleteMaxVisible", {"n": max_visible})

    def get_padding_x(self) -> int:
        return int(self._op("getPaddingX").get("n", 0))

    def get_autocomplete_max_visible(self) -> int:
        return int(self._op("getAutocompleteMaxVisible").get("n", 0))

    def border_color(self, text: str) -> str:
        """The default editor's borderColor called on ``text``: the editor's border color for its thinking level or bash mode."""
        return str(self._op("borderColor", {"text": text}).get("text", ""))

    @property
    def disable_submit(self) -> bool:
        """The default editor's disableSubmit."""
        return bool(self._op("getDisableSubmit").get("value", False))

    @disable_submit.setter
    def disable_submit(self, disabled: bool) -> None:
        self._op("setDisableSubmit", {"value": bool(disabled)})

    @property
    def focused(self) -> bool:
        """The default editor's focused, which the TUI sets as the editor takes and loses the focus."""
        return bool(self._op("getFocused").get("value", False))

    @focused.setter
    def focused(self, focused: bool) -> None:
        self._op("setFocused", {"value": bool(focused)})

    def set_autocomplete_provider(self, provider: "Autocomplete | None") -> None:
        """The default editor's setAutocompleteProvider with Pi's CombinedAutocompleteProvider(commands, basePath) (a
        :class:`pig_sdk.kit.Autocomplete`); None leaves the editor without a provider."""
        self._op("setAutocompleteProvider", {"provider": None if provider is None else provider._wire()})

    def on_submit(self, text: str) -> None:
        """Call the host's onSubmit callback with ``text``, as an editor does when the user submits it."""
        self._op("onSubmit", {"text": text})

    def on_change(self, text: str) -> None:
        """Call the host's onChange callback with ``text``, as an editor does when its text changed."""
        self._op("onChange", {"text": text})

    def set_working_status_indicator(self, status: "EditorStatus | None") -> None:
        """CustomEditor's setWorkingStatusIndicator, which the host calls when an editor that embeds the status gets a new
        status, a new frame of it, or none (None). The host draws the status in the rows :meth:`render` returns; override
        this to read it."""

    def invalidate(self) -> None:
        """The editor's invalidate, which the host calls when the theme changes. Pi's Editor has no cached state to drop;
        override this to drop the component's."""


class EditorComponent:
    """The editor an extension installs with ``set_editor_component``. Every method defaults to the host's default editor through :attr:`base`."""

    #: Whether the component draws the working, compaction, summarization and retry status in its top border, as Pi's
    #: CustomEditor does with ``embedWorkingStatus: true``. The host then renders the status into the rows that
    #: :meth:`EditorBase.render` returns instead of showing it above the editor. Read once, after the factory returns.
    embed_working_status: bool = False

    def __init__(self, base: EditorBase) -> None:
        self.base = base

    def handle_input(self, data: str) -> None:
        self.base.handle_input(data)

    def handle_mouse(self, event: dict[str, Any]) -> None:
        self.base.handle_mouse(event)

    def render(self, width: int) -> list[str]:
        return self.base.render(width)

    def set_text(self, text: str) -> None:
        self.base.set_text(text)

    def insert_text_at_cursor(self, text: str) -> None:
        self.base.insert_text_at_cursor(text)

    def add_to_history(self, text: str) -> None:
        self.base.add_to_history(text)

    def set_working_status_indicator(self, status: "EditorStatus | None") -> None:
        self.base.set_working_status_indicator(status)

    def invalidate(self) -> None:
        self.base.invalidate()


EditorFactory = Callable[[EditorBase], EditorComponent]

_STOP = object()


class _EditorSession:
    """One installed editor component and the worker thread that runs it."""

    def __init__(self, extension: "Extension", key: str, factory: "EditorFactory | None" = None) -> None:
        self.extension = extension
        self.key = key
        # The factory this session was installed from; get_editor_component returns it (Pi's runtime keeps the extension's own editorFactory).
        self.factory = factory
        self.component: EditorComponent | None = None
        self.events: "queue.Queue[Any]" = queue.Queue()
        self.last_frame: tuple[int, list[str]] | None = None
        self.seq = 0
        # The working status the component was last told, and whether it was told one.
        self.last_status: EditorStatus | None = None
        self.status_sent = False
        self.thread = threading.Thread(target=self._run, name=f"pig-{key}", daemon=True)

    def post(self, event: Any) -> None:
        self.events.put(event)

    def close(self) -> None:
        self.events.put(_STOP)

    def _fail(self, what: str, exc: BaseException) -> None:
        self.extension._notify("ui.notify", {"message": f"editor {what} failed: {exc}", "level": "error"})

    def _guard(self, what: str, call: Callable[[], Any]) -> Any:
        try:
            return call()
        except Exception as exc:  # noqa: BLE001 - an extension's error is reported, never fatal to the worker
            self._fail(what, exc)
            return None

    def _configure(self, raw: Any) -> None:
        """Apply the host's configuration: a changed working status goes to the component's set_working_status_indicator."""
        status = None
        if isinstance(raw, dict):
            status = EditorStatus(
                kind=str(raw.get("kind", "")),
                message=str(raw.get("message", "")),
                frames=tuple(str(frame) for frame in raw.get("frames") or ()),
                frame=int(raw.get("frame", 0)),
                spinner_color=str(raw.get("spinnerColor", "")),
                message_color=str(raw.get("messageColor", "")),
                indicator_verbatim=bool(raw.get("indicatorVerbatim", False)),
            )
        if (self.status_sent and status == self.last_status) or (not self.status_sent and status is None):
            return
        self.status_sent, self.last_status = True, status
        self._guard("setWorkingStatusIndicator", lambda: self.component.set_working_status_indicator(status))

    def _render_now(self) -> None:
        with self.extension._state_lock:
            width = self.extension._width or 80
        lines = self._guard("render", lambda: [str(line) for line in self.component.render(width)])
        if lines is None:
            return
        if self.last_frame == (width, lines):
            return
        self.last_frame = (width, lines)
        self.seq += 1
        self.extension._notify("ui.editor.render", {"key": self.key, "lines": lines, "width": width, "seq": self.seq})

    def _run(self) -> None:
        component = self.component
        if component is None:
            raise RuntimeError("editor component loop started without a component")
        while True:
            event = self.events.get()
            if event is _STOP:
                return
            kind, payload = event
            if kind == "input":
                self._guard("handleInput", lambda: component.handle_input(payload))
                self._render_now()
                self.extension._notify("ui.editor.inputDone", {"key": self.key})
                continue
            if kind == "mouse":
                self._guard("handleMouse", lambda: component.handle_mouse(payload))
            elif kind == "setText":
                self._guard("setText", lambda: component.set_text(payload))
            elif kind == "insertText":
                self._guard("insertTextAtCursor", lambda: component.insert_text_at_cursor(payload))
            elif kind == "addToHistory":
                self._guard("addToHistory", lambda: component.add_to_history(payload))
            elif kind == "configure":
                self._configure(payload)
            elif kind == "invalidate":
                self._guard("invalidate", component.invalidate)
            self._render_now()


_EDITOR_EVENTS = {
    "ui.editor.input": ("input", "data"),
    "ui.editor.mouse": ("mouse", "event"),
    "ui.editor.setText": ("setText", "text"),
    "ui.editor.insertText": ("insertText", "text"),
    "ui.editor.addToHistory": ("addToHistory", "text"),
    "ui.editor.configure": ("configure", "workingStatus"),
    "ui.editor.invalidate": ("invalidate", None),
}


def install_editor(extension: "Extension", seq: int, factory: EditorFactory) -> None:
    """Install the component the factory builds. The host learns of the editor before the factory runs, so the factory may already call the :class:`EditorBase` it receives: such a call waits until the host's editor is ready."""
    key = f"editor-{seq}"
    session = _EditorSession(extension, key, factory)
    with extension._editor_lock:
        previous = extension._editor
        extension._editor = session
    if previous is not None:
        previous.close()
    extension._notify("ui.editor.install", {"key": key, "delegated": True})
    try:
        component = factory(EditorBase(extension, key))
    except BaseException:
        with extension._editor_lock:
            if extension._editor is session:
                extension._editor = None
        extension._notify("ui.editor.clear", {"key": key})
        raise
    session.component = component
    session.thread.start()
    if getattr(component, "embed_working_status", False) is True:
        extension._notify("ui.editor.options", {"key": key, "embedWorkingStatus": True})


def clear_editor(extension: "Extension") -> None:
    with extension._editor_lock:
        previous, extension._editor = extension._editor, None
    if previous is not None:
        previous.close()


def editor_notify(extension: "Extension", method: str, args: dict[str, Any]) -> bool:
    """Route one of the host's editor notifies to the installed component; report whether ``method`` was one."""
    if method != "ui.editor.closed" and method not in _EDITOR_EVENTS:
        return False
    with extension._editor_lock:
        session = extension._editor
        if session is None or session.key != str(args.get("key") or ""):
            return True
        if method == "ui.editor.closed":
            extension._editor = None
    if method == "ui.editor.closed":
        session.close()
        return True
    kind, field = _EDITOR_EVENTS[method]
    payload = args.get(field) if field else None
    if field in ("data", "text"):
        payload = str(payload or "")
    session.post((kind, payload))
    return True
