"""The members of pi-ai's ``Type`` that compute a schema (``pick``, ``omit``, ``partial``, ``required``, ``key_of``, ``index``, ``mapped``, ``conditional``, ``extends``,
``evaluate``, ``instantiate``, ``cyclic``, ``module``, ``script``, ...), served by TypeBox's type engine, which the host runs: one port of TypeBox's
``build/type/{action,engine,extends,script}`` for every SDK (``internal/typebox/engine``). A call encodes its arguments in the engine's wire form, asks the host (the
``type.engine`` call, which the host answers before the extension registers, so a schema built at import time works) and decodes the schema the host returns: the enumerable
members in the order TypeBox wrote them and the hidden ones (``~kind``, ``~optional``, ...). What TypeBox throws raises ``ValueError`` with its message."""
from typing import Any, Callable, Mapping, Optional, Sequence

_ENVELOPE = "\x00"
_CALL_TYPE_ENGINE = "type.engine"

BIGINT_STRINGIFY_ERROR = "Do not know how to serialize a BigInt"


class BigInt:
    """A JavaScript bigint a schema holds as a member, such as the ``const`` of ``Type.script("1n")`` or ``Type.literal(BigInt(1))``: TypeBox's value holds it, and only ``JSON.stringify``
    of it fails. Writing a schema that holds one as JSON (:func:`type_to_json`, ``TSchema.to_json``, registering it as a tool's parameters) raises ``TypeError`` with Pi's message,
    "Do not know how to serialize a BigInt"; every other use of the value (the guards, passing it to another member) works. It is not an ``int`` (a Python ``int`` is a
    JavaScript number), so ``json.dumps`` does not write it either."""

    __slots__ = ("value",)

    def __init__(self, value: Any) -> None:
        if isinstance(value, bool) or not isinstance(value, int):
            raise TypeError(f"Cannot convert {value!r} to a BigInt")
        self.value = value

    def __eq__(self, other: Any) -> bool:
        return isinstance(other, BigInt) and other.value == self.value

    def __hash__(self) -> int:
        return hash(("BigInt", self.value))

    def __str__(self) -> str:
        return str(self.value)

    def __repr__(self) -> str:
        return f"BigInt({self.value})"


def holds_bigint(value: Any) -> bool:
    """Whether a :class:`BigInt` is anywhere inside ``value`` (a schema, a dict, a list)."""
    if isinstance(value, BigInt):
        return True
    if isinstance(value, dict):
        return any(holds_bigint(member) for member in value.values())
    if isinstance(value, (list, tuple)):
        return any(holds_bigint(member) for member in value)
    return False


class _Undefined:
    """JavaScript's undefined in a reply: JSON.stringify leaves it out of an object and writes null in an array."""


_UNDEFINED = _Undefined()


def _encode(value: Any, refs: list) -> Any:
    """The wire form of a value (see ``internal/typebox/engine/wire.go``), as the JSON-able structure that is dumped."""
    from .type_schema import TSchema, TSchemaArray

    if isinstance(value, TSchemaArray):
        return [_encode(item, refs) for item in value]
    if isinstance(value, TSchema):
        enumerable = {key: _encode(member, refs) for key, member in value.items()}
        hidden = {}
        for name, member in value.hidden.items():
            if name == "~refine":
                hidden[name] = []
                for refinement in member:
                    refs.append(refinement)
                    hidden[name].append({_ENVELOPE: "f", "i": len(refs) - 1})
            else:
                hidden[name] = _encode(member, refs)
        envelope: dict = {_ENVELOPE: "o"}
        if hidden:
            envelope["h"] = hidden
        envelope["e"] = enumerable
        return envelope
    if isinstance(value, dict):
        members = {str(key): _encode(member, refs) for key, member in value.items()}
        if any(key.startswith(_ENVELOPE) for key in members):
            return {_ENVELOPE: "o", "e": members}
        return members
    if isinstance(value, (list, tuple)):
        return [_encode(item, refs) for item in value]
    if isinstance(value, BigInt):
        return {_ENVELOPE: "n", "v": str(value.value)}
    if value is None or isinstance(value, (bool, str)):
        return value
    if isinstance(value, (int, float)):
        return value
    raise TypeError(f"a {type(value).__name__} has no JavaScript counterpart a schema can hold")


def _decode(value: Any, refs: list) -> Any:
    from .type_schema import TSchema

    if isinstance(value, list):
        return [None if item is _UNDEFINED else item for item in (_decode(member, refs) for member in value)]
    if not isinstance(value, dict):
        return value
    if next(iter(value), None) == _ENVELOPE:
        kind = value[_ENVELOPE]
        if kind == "u":
            return _UNDEFINED
        if kind == "n":
            return BigInt(int(value["v"]))
        if kind == "f":
            return refs[value["i"]]
        if kind == "o":
            hidden = {name: _decode(member, refs) for name, member in (value.get("h") or {}).items()}
            enumerable = {key: member for key, member in ((k, _decode(m, refs)) for k, m in (value.get("e") or {}).items()) if member is not _UNDEFINED}
            return TSchema(enumerable, hidden)
        raise ValueError(f"the host answered an unknown envelope {kind!r}")
    return {key: member for key, member in ((k, _decode(m, refs)) for k, m in value.items()) if member is not _UNDEFINED}


def engine_call(member: str, *args: Any) -> Any:
    """Run ``member`` of the type engine on ``args`` and return the value it answers."""
    from . import host_helpers

    refs: list = []
    request = {"member": member, "args": [_encode(arg, refs) for arg in args]}
    reply = host_helpers._helper_call(_CALL_TYPE_ENGINE, request)
    if "error" in reply:
        raise ValueError(reply["error"].get("message", ""))
    return _decode(reply.get("value"), refs)


def _match_count(args: Sequence[Any], low: int, high: int) -> None:
    """TypeBox's ``Arguments.Match``: these members choose their overload by the number of arguments, and any other count throws before anything runs. Python can be called with
    one too few or too many, so the SDK throws what Pi throws."""
    if not low <= len(args) <= high:
        raise ValueError("Invalid Arguments")


def _schema(value: Any) -> Any:
    """A result that is an object, as a schema: a plain object (Module's result, an Extends answer's members) has no hidden members."""
    from .type_schema import TSchema

    if isinstance(value, dict) and not isinstance(value, TSchema):
        return TSchema(value, {})
    return value


def _with(args: list, options: Optional[Mapping[str, Any]]) -> list:
    if options is not None:
        args.append(options)
    return args


def _run(member: str, args: list, options: Optional[Mapping[str, Any]] = None) -> Any:
    return _schema(engine_call(member, *_with(args, options)))


class _EngineMembers:
    """The engine-served members of ``Type``, with Pi's names in snake case and the options as the last, optional argument."""

    @staticmethod
    def call(target: Any, arguments: Sequence[Any]) -> Any:
        return _schema(engine_call("Call", target, list(arguments)))

    @staticmethod
    def capitalize(schema: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("Capitalize", [schema], options)

    @staticmethod
    def lowercase(schema: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("Lowercase", [schema], options)

    @staticmethod
    def uncapitalize(schema: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("Uncapitalize", [schema], options)

    @staticmethod
    def uppercase(schema: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("Uppercase", [schema], options)

    @staticmethod
    def conditional(left: Any, right: Any, true_type: Any, false_type: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        """``Type.Conditional``: TypeScript's ``left extends right ? true_type : false_type``."""
        return _run("Conditional", [left, right, true_type, false_type], options)

    @staticmethod
    def constructor(parameters: Sequence[Any], instance_type: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("Constructor", [list(parameters), instance_type], options)

    @staticmethod
    def constructor_parameters(schema: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("ConstructorParameters", [schema], options)

    @staticmethod
    def parameters(schema: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("Parameters", [schema], options)

    @staticmethod
    def instance_type(schema: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("InstanceType", [schema], options)

    @staticmethod
    def return_type(schema: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("ReturnType", [schema], options)

    @staticmethod
    def cyclic(defs: Mapping[str, Any], ref: str, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("Cyclic", [dict(defs), ref], options)

    @staticmethod
    def dependent(if_type: Any, then_type: Any, else_type: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("Dependent", [if_type, then_type, else_type], options)

    @staticmethod
    def evaluate(schema: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("Evaluate", [schema], options)

    @staticmethod
    def exclude(left: Any, right: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("Exclude", [left, right], options)

    @staticmethod
    def extract(left: Any, right: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("Extract", [left, right], options)

    @staticmethod
    def extends(inferred: Optional[Mapping[str, Any]], left: Any, right: Any) -> Any:
        """``Type.Extends(inferred, left, right)``: an ExtendsTrue, ExtendsUnion or ExtendsFalse value; ``inferred`` holds the types an ``infer`` already captured (None is none)."""
        return _schema(engine_call("Extends", dict(inferred or {}), left, right))

    @staticmethod
    def function(parameters: Sequence[Any], return_type: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("Function", [list(parameters), return_type], options)

    @staticmethod
    def generic(parameters: Sequence[Any], expression: Any) -> Any:
        return _schema(engine_call("Generic", list(parameters), expression))

    @staticmethod
    def identifier(name: str) -> Any:
        return _schema(engine_call("Identifier", name))

    @staticmethod
    def index(schema: Any, indexer: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        """``Type.Index(type, indexer)``: TypeScript's ``type[indexer]``; ``indexer`` is a schema or a list of keys."""
        return _run("Index", [schema, indexer], options)

    @staticmethod
    def infer(*args: Any) -> Any:
        """``Type.Infer(name)`` or ``(name, extends)``. Any other argument count raises ``ValueError("Invalid Arguments")``, as Pi's does (TypeBox matches the arguments by count)."""
        _match_count(args, 1, 2)
        return _schema(engine_call("Infer", *args))

    @staticmethod
    def instantiate(context: Optional[Mapping[str, Any]], schema: Any) -> Any:
        return _schema(engine_call("Instantiate", dict(context or {}), schema))

    @staticmethod
    def interface(heritage: Sequence[Any], properties: Mapping[str, Any], options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("Interface", [list(heritage), dict(properties)], options)

    @staticmethod
    def key_of(schema: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("KeyOf", [schema], options)

    @staticmethod
    def mapped(identifier: Any, schema: Any, as_: Any, property: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        """``Type.Mapped(identifier, type, as, property)``: TypeScript's ``{ [identifier in type as as]: property }``."""
        return _run("Mapped", [identifier, schema, as_, property], options)

    @staticmethod
    def module(declarations: Optional[Mapping[str, Any]] = None, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("Module", [dict(declarations or {})], options)

    @staticmethod
    def non_nullable(schema: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("NonNullable", [schema], options)

    @staticmethod
    def omit(schema: Any, keys: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        """``Type.Omit(type, keys)``; ``keys`` is a list of keys or a schema of them."""
        return _run("Omit", [schema, keys], options)

    @staticmethod
    def pick(schema: Any, keys: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        """``Type.Pick(type, keys)``; ``keys`` is a list of keys or a schema of them."""
        return _run("Pick", [schema, keys], options)

    @staticmethod
    def parameter(*args: Any) -> Any:
        """``Type.Parameter(name)``, ``(name, extends)`` or ``(name, extends, equals)``. Any other argument count raises ``ValueError("Invalid Arguments")``, as Pi's does."""
        _match_count(args, 1, 3)
        return _schema(engine_call("Parameter", *args))

    @staticmethod
    def partial(schema: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("Partial", [schema], options)

    @staticmethod
    def required(schema: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("Required", [schema], options)

    @staticmethod
    def readonly_object(schema: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("ReadonlyObject", [schema], options)

    @staticmethod
    def readonly_type(schema: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("ReadonlyType", [schema], options)

    @staticmethod
    def record_key(record: Any) -> Any:
        return _schema(engine_call("RecordKey", record))

    @staticmethod
    def record_pattern(record: Any) -> Any:
        return engine_call("RecordPattern", record)

    @staticmethod
    def record_value(record: Any) -> Any:
        return _schema(engine_call("RecordValue", record))

    @staticmethod
    def rest(schema: Any) -> Any:
        return _schema(engine_call("Rest", schema))

    @staticmethod
    def script(*args: Any) -> Any:
        """``Type.Script(script)``, ``(script, options)``, ``(context, script)`` or ``(context, script, options)``: TypeScript type text, parsed and instantiated. Any other argument count
        raises as Pi's does."""
        _match_count(args, 1, 3)
        return _schema(engine_call("Script", *args))

    @staticmethod
    def template_literal(input: Any, options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("TemplateLiteral", [input], options)

    @staticmethod
    def this(options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("This", [], options)

    @staticmethod
    def tuple(types: Sequence[Any], options: Optional[Mapping[str, Any]] = None) -> Any:
        return _run("Tuple", [list(types)], options)


class _ExtendsResult:
    """``Type.ExtendsResult``: the values ``Type.extends`` answers with and the guards and matcher over them. They compute nothing the host has to, so they run here."""

    @staticmethod
    def _outcome(kind: str, inferred: Optional[Mapping[str, Any]]) -> Any:
        from .type_schema import _create

        return _create(kind, [] if inferred is None else ["inferred", dict(inferred)], None)

    def extends_true(self, inferred: Optional[Mapping[str, Any]] = None) -> Any:
        return self._outcome("ExtendsTrue", inferred or {})

    def extends_union(self, inferred: Optional[Mapping[str, Any]] = None) -> Any:
        return self._outcome("ExtendsUnion", inferred or {})

    def extends_false(self) -> Any:
        return self._outcome("ExtendsFalse", None)

    @staticmethod
    def _has_inferred(value: Any) -> bool:
        from .type_schema import _is_schema, _member

        found, inferred = _member(value, "inferred")
        return found and _is_schema(inferred)

    def is_extends_union(self, value: Any) -> bool:
        from .type_schema import Type

        return Type.is_kind(value, "ExtendsUnion") and self._has_inferred(value)

    def is_extends_true(self, value: Any) -> bool:
        from .type_schema import Type

        return Type.is_kind(value, "ExtendsTrue") and self._has_inferred(value)

    def is_extends_false(self, value: Any) -> bool:
        from .type_schema import Type

        return Type.is_kind(value, "ExtendsFalse")

    def is_extends_true_like(self, value: Any) -> bool:
        return self.is_extends_union(value) or self.is_extends_true(value)

    def match(self, result: Any, on_true: Callable[[Any], Any], on_false: Callable[[], Any]) -> Any:
        """``Type.ExtendsResult.Match``: ``on_true`` with the inferred types of a true-like result, ``on_false`` otherwise."""
        from .type_schema import _member

        if self.is_extends_true_like(result):
            return on_true(_member(result, "inferred")[1])
        return on_false()


EXTENDS_RESULT = _ExtendsResult()
