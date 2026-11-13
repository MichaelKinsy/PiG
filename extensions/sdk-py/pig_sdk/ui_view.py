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
    #: The JSON value a finished mount was given by ``finish_view(mount, result=...)``; ``None`` otherwise.
    result: Any = None


@dataclass(frozen=True)
class ViewClaim:
    """One fact the attached client states about itself, named ``<area>.<fact>``. ``value`` is a bool, an int, a str or
    a tuple of str."""

    name: str
    value: Any


@dataclass(frozen=True)
class ViewSlotClaims:
    """The claims that replace the same-named ones for mounts of one slot."""

    slot: str
    claims: tuple[ViewClaim, ...] = ()


@dataclass(frozen=True)
class ViewDrawingAbilities:
    """The drawing profiles (``scene``, ``pixels``) the client draws, and how many shapes and pixels."""

    profiles: tuple[str, ...] = ()
    shapes: int = 0
    pixels: int = 0


@dataclass(frozen=True)
class ViewNotice:
    """One diagnostic of a mount: an operation the host rejected (``invalid``, ``unsupported``, ``limit``, ``stale``,
    ``terminal``), an intent it ended (``refused``, ``failed``, ``timeout``, ``stale``), a part a client could not draw
    (``undrawn``) or a focus request a client did not grant (``unfocused``). ``count`` is how often it happened; ``measured``
    and ``limit`` are ``None`` unless a limit was hit."""

    code: str
    severity: str
    message: str = ""
    path: str = ""
    claim: str = ""
    intent: str = ""
    measured: int | None = None
    limit: int | None = None
    epoch: int = 0
    rev: int = 0
    count: int = 0


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
    """One intent the view's nodes may raise. ``name`` is ``"<extension>.<verb>"``; ``timeout_ms`` defaults to 30000 and is 100..300000.

    ``when_hidden`` lets a client raise the intent while the mount is hidden: it is how the user opens a view the extension
    hid, and the handler shows the mount. At most one intent of a mount has it; it names no node and its ``arg`` accepts ``{}``.

    ``dismiss`` names the intent a client raises, with ``{}``, when the user dismisses a dialog or overlay mount: the handler
    closes the mount. The same three rules hold for it. Without one, Escape leaves the mount and it stays open until the
    extension closes it."""

    name: str
    arg: ViewShape
    node_key: str = ""
    answer: ViewShape | None = None
    cancellable: bool = False
    timeout_ms: int = 0
    when_hidden: bool = False
    dismiss: bool = False

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
        if self.when_hidden:
            out["whenHidden"] = True
        if self.dismiss:
            out["dismiss"] = True
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
    #: Further facts the client states; with several clients attached, the claims all of them share.
    claims: tuple[ViewClaim, ...] = ()
    slot_claims: tuple[ViewSlotClaims, ...] = ()
    #: ``None`` when the client draws no drawing.
    drawing: ViewDrawingAbilities | None = None


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
        claims=_claims(raw.get("claims")),
        slot_claims=tuple(ViewSlotClaims(str(sc.get("slot", "")), _claims(sc.get("claims"))) for sc in raw.get("slotClaims") or ()),
        drawing=None
        if not isinstance(raw.get("drawing"), dict)
        else ViewDrawingAbilities(
            profiles=tuple(raw["drawing"].get("profiles") or ()),
            shapes=int(raw["drawing"].get("shapes") or 0),
            pixels=int(raw["drawing"].get("pixels") or 0),
        ),
    )


def decode_status(result: Any) -> ViewStatus:
    if not isinstance(result, dict) or not isinstance(result.get("mount"), dict):
        raise RuntimeError("ui.view: malformed reply")
    m = result["mount"]
    return ViewStatus(
        ViewRef(str(m.get("key", "")), int(m.get("epoch", 0)), int(m.get("rev", 0))),
        str(result.get("state", "")),
        str(result.get("reason", "")),
        result.get("result"),
    )


def decode_notices(result: Any) -> list[ViewNotice]:
    if not isinstance(result, dict) or not isinstance(result.get("notices"), list):
        raise RuntimeError("ui.view.diagnostics: malformed reply")
    return [
        ViewNotice(
            code=str(n.get("code", "")),
            severity=str(n.get("severity", "")),
            message=str(n.get("message", "")),
            path=str(n.get("path", "")),
            claim=str(n.get("claim", "")),
            intent=str(n.get("intent", "")),
            measured=n.get("measured"),
            limit=n.get("limit"),
            epoch=int(n.get("epoch", 0)),
            rev=int(n.get("rev", 0)),
            count=int(n.get("count", 0)),
        )
        for n in result["notices"]
    ]


def _claims(raw: Any) -> tuple[ViewClaim, ...]:
    out = []
    for c in raw or ():
        value = c.get("value")
        out.append(ViewClaim(str(c.get("name", "")), tuple(value) if isinstance(value, list) else value))
    return tuple(out)


class ViewState:
    """The replicated abilities and the intent handler of one extension."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._abilities: ViewAbilities | None = None
        # The uiAbilities member the abilities were decoded from (None: no client), and whether the host sent a state: a later different member is a change.
        self._raw: Any = None
        self._known = False
        self.handler: ViewIntentHandler | None = None

    def apply_state(self, state: dict[str, Any]) -> tuple[bool, ViewAbilities | None]:
        """Apply a whole state snapshot: an absent ``uiAbilities`` clears them. Returns whether they differ from the ones the
        snapshot before carried, and the new abilities. The first snapshot is where the extension starts, not a change."""
        raw = state.get("uiAbilities")
        abilities = decode_abilities(raw)
        if abilities is None:
            raw = None
        with self._lock:
            changed = self._known and raw != self._raw
            self._abilities, self._raw, self._known = abilities, raw, True
        return changed, abilities

    def abilities(self) -> ViewAbilities | None:
        with self._lock:
            return self._abilities


def encode_content(
    wire_view: dict[str, Any],
    intents: Any,
    assets: Any,
    fallback_wire: dict[str, Any] | None,
    alternatives: Any = (),
    drawing: Any = None,
    a11y: Any = (),
    focus: Any = (),
    bindings: Any = (),
    title: str = "",
) -> dict[str, Any]:
    """Build the content of a mount or an update. ``alternatives`` are already in wire form (``when``, ``view``, ``drawing``);
    ``drawing``, ``a11y``, ``focus`` and ``bindings`` are the wire records themselves."""
    out: dict[str, Any] = {"view": wire_view}
    if title:
        out["title"] = title
    if alternatives:
        out["alternatives"] = list(alternatives)
    if drawing is not None:
        out["drawing"] = drawing
    if a11y:
        out["a11y"] = list(a11y)
    if focus:
        out["focus"] = list(focus)
    if bindings:
        out["bindings"] = list(bindings)
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
