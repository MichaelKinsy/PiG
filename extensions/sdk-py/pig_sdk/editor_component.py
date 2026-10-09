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

import queue
import threading
from typing import TYPE_CHECKING, Any, Callable

if TYPE_CHECKING:
    from . import Extension


class EditorBase:
    """The host's default editor, the ``super`` of an :class:`EditorComponent`. Every method is a call to the host's editor, valid once the factory has returned."""

    def __init__(self, extension: "Extension", key: str) -> None:
        self._extension = extension
        self._key = key

    def _op(self, op: str, args: dict[str, Any] | None = None) -> dict[str, Any]:
        reply = self._extension._call("ui.editor.base", {"key": self._key, "op": op, "args": args or {}})
        return reply.get("result") or {}

    def handle_input(self, data: str) -> None:
        """The default editor's handleInput: extension shortcuts, then the app actions, then editing."""
        self._op("handleInput", {"data": data})

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


class EditorComponent:
    """The editor an extension installs with ``set_editor_component``. Every method defaults to the host's default editor through :attr:`base`."""

    #: Whether the component draws the working, compaction, summarization and retry status in its top border, as Pi's
    #: CustomEditor does with ``embedWorkingStatus: true``. The host then renders the status into the rows that
    #: :meth:`EditorBase.render` returns instead of showing it above the editor. Read once, after the factory returns.
    embed_working_status = False

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


EditorFactory = Callable[[EditorBase], EditorComponent]

_STOP = object()


class _EditorSession:
    """One installed editor component and the worker thread that runs it."""

    def __init__(self, extension: "Extension", key: str) -> None:
        self.extension = extension
        self.key = key
        self.component: EditorComponent | None = None
        self.events: "queue.Queue[Any]" = queue.Queue()
        self.last_frame: tuple[int, list[str]] | None = None
        self.seq = 0
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
        assert component is not None
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
            self._render_now()


_EDITOR_EVENTS = {
    "ui.editor.input": ("input", "data"),
    "ui.editor.mouse": ("mouse", "event"),
    "ui.editor.setText": ("setText", "text"),
    "ui.editor.insertText": ("insertText", "text"),
    "ui.editor.addToHistory": ("addToHistory", "text"),
    "ui.editor.configure": ("render", None),
}


def install_editor(extension: "Extension", seq: int, factory: EditorFactory) -> None:
    """Install the component the factory builds. The host learns of the editor before the factory runs, so the factory may already call the :class:`EditorBase` it receives: such a call waits until the host's editor is ready."""
    key = f"editor-{seq}"
    session = _EditorSession(extension, key)
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
