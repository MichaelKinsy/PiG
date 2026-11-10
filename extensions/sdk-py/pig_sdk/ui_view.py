"""pig additive (D614): plugin UI mounts.

An extension mounts a :class:`pig_sdk.kit.View` in a slot the attached client draws, updates it, and answers the user's
intents. The host replicates what the client draws (``Context.view_abilities``); with no client attached it is ``None``
and a mount fails with ``ViewError`` code ``"unsupported"``. The wire is the one the Go SDK speaks: ``ui.view.mount``,
``update``, ``hide``, ``show``, ``finish``, ``remove`` and ``state`` calls, and the ``ui.view.intent`` request.
"""

from __future__ import annotations

import threading
from dataclasses import dataclass, field
from typing import Any, Callable

SLOTS = ("notify", "status", "widget-above", "widget-below", "header", "footer", "dialog", "overlay", "panel", "tool-card", "message", "entry")


class ViewError(RuntimeError):
    """A failed mount operation. ``code`` is one of invalid, unsupported, limit, stale, terminal, not_found."""

    def __init__(self, code: str, message: str):
        super().__init__(f"{code}: {message}")
        self.code = code
        self.message = message


@dataclass(frozen=True)
class ViewRef:
    """One revision of one mount incarnation. Pass the ref the last operation returned to the next update."""

    key: str
    epoch: int = 0
    rev: int = 0

    def _wire(self) -> dict[str, Any]:
        return {"key": self.key, "epoch": self.epoch, "rev": self.rev}


@dataclass(frozen=True)
class ViewStatus:
    """The reply of every mount operation. ``state`` is live, hidden, finished, removed or broken; ``reason`` is set
    for broken: session_replaced, owner_gone or shutdown."""

    mount: ViewRef
    state: str
    reason: str = ""


@dataclass(frozen=True)
class ViewTarget:
    """Ties a tool-card, message or entry mount to what it decorates; set exactly one field."""

    tool_call_id: str = ""
    message_type: str = ""
    entry_type: str = ""

    def _wire(self) -> dict[str, Any]:
        out: dict[str, Any] = {}
        if self.tool_call_id:
            out["toolCallId"] = self.tool_call_id
        if self.message_type:
            out["messageType"] = self.message_type
        if self.entry_type:
            out["entryType"] = self.entry_type
        return out


@dataclass(frozen=True)
class ViewShape:
    """The closed JSON subset an intent argument or answer is checked against: type boolean, string, integer, number,
    enum (``enum``, at most 32), object (``properties`` at most 32, ``required``) or array (``items``, ``max_items``
    at most 64), nested to depth 4."""

    type: str
    enum: tuple[str, ...] = ()
    properties: dict[str, "ViewShape"] = field(default_factory=dict)
    required: tuple[str, ...] = ()
    items: "ViewShape | None" = None
    max_items: int = 0

    def _wire(self) -> dict[str, Any]:
        out: dict[str, Any] = {"type": self.type}
        if self.enum:
            out["enum"] = list(self.enum)
        if self.properties:
            out["properties"] = {k: v._wire() for k, v in self.properties.items()}
        if self.required:
            out["required"] = list(self.required)
        if self.items is not None:
            out["items"] = self.items._wire()
        if self.max_items:
            out["maxItems"] = self.max_items
        return out


@dataclass(frozen=True)
class ViewIntent:
    """One intent the view's nodes may raise. ``name`` is ``"<extension>.<verb>"``; ``timeout_ms`` defaults to 30000 and is 100..300000."""

    name: str
    arg: ViewShape
    node_key: str = ""
    answer: ViewShape | None = None
    cancellable: bool = False
    timeout_ms: int = 0

    def _wire(self) -> dict[str, Any]:
        out: dict[str, Any] = {"name": self.name, "arg": self.arg._wire()}
        if self.node_key:
            out["nodeKey"] = self.node_key
        if self.answer is not None:
            out["answer"] = self.answer._wire()
        if self.cancellable:
            out["cancellable"] = True
        if self.timeout_ms:
            out["timeoutMs"] = self.timeout_ms
        return out


@dataclass(frozen=True)
class ViewAsset:
    """A bounded image a view refers to by id; ``delivery`` is inline or managed."""

    id: str
    mime: str
    size: int
    sha256: str
    delivery: str

    def _wire(self) -> dict[str, Any]:
        return {"id": self.id, "mime": self.mime, "size": self.size, "sha256": self.sha256, "delivery": self.delivery}


@dataclass(frozen=True)
class ViewLimits:
    nodes: int = 0
    depth: int = 0
    snapshot_bytes: int = 0
    text_bytes: int = 0
    mounts: int = 0
    intent_arg_bytes: int = 0


@dataclass(frozen=True)
class ViewInput:
    keyboard: bool = False
    pointer: bool = False
    text: bool = False


@dataclass(frozen=True)
class ViewAbilities:
    """What the attached client draws."""

    slots: tuple[str, ...] = ()
    nodes: tuple[str, ...] = ()
    markdown: bool = False
    image_formats: tuple[str, ...] = ()
    limits: ViewLimits = field(default_factory=ViewLimits)
    input: ViewInput = field(default_factory=ViewInput)


@dataclass
class ViewIntentInvocation:
    """One intent the user raised. ``context`` is the handler's context: ``context.is_cancelled()`` turns true when the host
    cancels a cancellable intent (timeout, or the user withdrew it); a cancelled handler's answer is dropped."""

    mount: ViewRef
    rev: int
    node_key: str
    intent: str
    arg: Any
    ticket: str
    context: Any


@dataclass(frozen=True)
class ViewIntentResult:
    """A handler's answer: outcome ``"done"`` (``answer``), ``"refused"`` or ``"failed"`` (``error``)."""

    outcome: str = "done"
    answer: Any = None
    error: str = ""


ViewIntentHandler = Callable[[ViewIntentInvocation], ViewIntentResult]


def decode_abilities(raw: Any) -> ViewAbilities | None:
    if not isinstance(raw, dict):
        return None
    limits = raw.get("limits") or {}
    inp = raw.get("input") or {}
    return ViewAbilities(
        slots=tuple(raw.get("slots") or ()),
        nodes=tuple(raw.get("nodes") or ()),
        markdown=raw.get("markdown") is True,
        image_formats=tuple(raw.get("imageFormats") or ()),
        limits=ViewLimits(
            nodes=int(limits.get("nodes") or 0),
            depth=int(limits.get("depth") or 0),
            snapshot_bytes=int(limits.get("snapshotBytes") or 0),
            text_bytes=int(limits.get("textBytes") or 0),
            mounts=int(limits.get("mounts") or 0),
            intent_arg_bytes=int(limits.get("intentArgBytes") or 0),
        ),
        input=ViewInput(keyboard=inp.get("keyboard") is True, pointer=inp.get("pointer") is True, text=inp.get("text") is True),
    )


def decode_status(result: Any) -> ViewStatus:
    if not isinstance(result, dict) or not isinstance(result.get("mount"), dict):
        raise RuntimeError("ui.view: malformed reply")
    m = result["mount"]
    return ViewStatus(ViewRef(str(m.get("key", "")), int(m.get("epoch", 0)), int(m.get("rev", 0))), str(result.get("state", "")), str(result.get("reason", "")))


class ViewState:
    """The replicated abilities and the intent handler of one extension."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._abilities: ViewAbilities | None = None
        self.handler: ViewIntentHandler | None = None

    def apply_state(self, state: dict[str, Any]) -> None:
        """Apply a whole state snapshot: an absent ``uiAbilities`` clears them."""
        with self._lock:
            self._abilities = decode_abilities(state.get("uiAbilities"))

    def abilities(self) -> ViewAbilities | None:
        with self._lock:
            return self._abilities


def encode_content(wire_view: dict[str, Any], intents: Any, assets: Any, fallback_wire: dict[str, Any] | None) -> dict[str, Any]:
    out: dict[str, Any] = {"view": wire_view}
    if intents:
        out["intents"] = [i._wire() for i in intents]
    if assets:
        out["assets"] = [a._wire() for a in assets]
    if fallback_wire is not None:
        out["fallback"] = fallback_wire["root"]
    return out


def handle_intent(state: ViewState, ctx: Any, args: dict[str, Any]) -> dict[str, Any]:
    """Answer a ui.view.intent request."""
    handler = state.handler
    if handler is None:
        return {"outcome": "refused", "error": "no intent handler"}
    m = args.get("mount") or {}
    res = handler(
        ViewIntentInvocation(
            mount=ViewRef(str(m.get("key", "")), int(m.get("epoch", 0))),
            rev=int(args.get("rev", 0)),
            node_key=str(args.get("nodeKey", "")),
            intent=str(args.get("intent", "")),
            arg=args.get("arg"),
            ticket=str(args.get("ticket", "")),
            context=ctx,
        )
    )
    out: dict[str, Any] = {"outcome": res.outcome or "done"}
    if res.answer is not None:
        out["answer"] = res.answer
    if res.error:
        out["error"] = res.error
    return out


__all__ = [name for name in dir() if not name.startswith("_") and name not in ("annotations", "threading", "dataclass", "field", "Any", "Callable")]
