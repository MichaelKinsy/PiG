"""pi-ai's ``Type`` (TypeBox 1.3.27's builder namespace).

A :class:`TSchema` is a ``dict`` whose items are the JSON Schema TypeBox wrote, in the order TypeBox wrote them, so ``json.dumps`` of it is what ``JSON.stringify`` of
Pi's value is. It also carries TypeBox's hidden members (``~kind``, ``~optional``, ``~readonly``, ``~immutable``, ``~unsafe``), which JSON drops but which the constructors and
the guards read: ``Type.object`` builds ``required`` from the properties that are not ``~optional``, and ``Type.is_string`` is true for ``Type.string()`` and false for the JSON
``{"type": "string"}``. A TSchema is not changed after it is built: every constructor returns a new one. The Codec family (``Codec``, ``Decode``, ``Encode``, ``DecodeBuilder``,
``EncodeBuilder``, ``IsCodec``) is a numbered divergence (pig divergence (D198)) and is absent.
"""
import json
import math
import re
from typing import Any, Callable, Dict, List, Mapping, NamedTuple, Optional, Sequence

from .compaction_estimate import _js_number_string


class TSchema(dict):
    """A TypeBox schema value: a ``dict`` of the enumerable members with the hidden ones in :attr:`hidden` (the kind as a string, True for a marker, None for ``~unsafe``)."""

    __slots__ = ("hidden",)

    def __init__(self, items: Any = (), hidden: Optional[Dict[str, Any]] = None) -> None:
        super().__init__(items)
        self.hidden: Dict[str, Any] = dict(hidden) if hidden else {}

    @property
    def kind(self) -> str:
        """The value of ``~kind``, or "" for a value without one (``Type.unsafe`` of a plain schema)."""
        kind = self.hidden.get("~kind")
        return kind if isinstance(kind, str) else ""

    def to_json(self) -> str:
        """What JSON.stringify of the TypeBox value writes: the members in order, numbers as JavaScript prints them (1e21, 1e-7, 0 for -0) and only the escapes JSON.stringify writes."""
        return type_to_json(self)

    def __repr__(self) -> str:
        return f"TSchema({dict.__repr__(self)}, hidden={self.hidden!r})"


class Refinement(NamedTuple):
    """One entry of a schema's hidden ``~refine`` list (TypeBox's ``{check, error}``): ``check(value)`` decides whether a value passes and ``error(value)`` words the failure (None is
    TypeBox's "Refine Error"). Pi runs them while it validates a tool call's arguments; an extension tool's refinements run in this process when the host asks for them. An exception
    raised by either is what a thrown JavaScript error is: it ends the validation and the tool call fails with its message."""

    check: Callable[[Any], Any]
    error: Optional[Callable[[Any], str]] = None


class RefinementSite(NamedTuple):
    """A schema node that carries refinements: its path in the schema (the keywords that lead to it: ``/properties/a``, ``/anyOf/1``, ``/items``) and the refinements in the order added."""

    path: str
    refinements: List[Refinement]


_KEYWORDS_WITH_CHILD = ("additionalProperties", "additionalItems", "contains", "propertyNames", "not", "if", "then", "else", "items", "unevaluatedItems", "unevaluatedProperties")
_KEYWORDS_WITH_MEMBERS = ("properties", "patternProperties", "dependentSchemas", "dependencies", "$defs", "definitions")
_KEYWORDS_WITH_LIST = ("allOf", "anyOf", "oneOf", "prefixItems", "items")


def _pointer_segment(key: str) -> str:
    return key.replace("~", "~0").replace("/", "~1")


def refinement_sites(root: Any) -> List[RefinementSite]:
    """The nodes of a schema that carry refinements, in the order they are found. A node reached only through a keyword whose value is plain JSON has none."""
    sites: List[RefinementSite] = []

    def visit(node: Any, path: str) -> None:
        if not isinstance(node, TSchema):
            return
        refinements = node.hidden.get("~refine")
        if refinements:
            sites.append(RefinementSite(path, list(refinements)))
        for key, value in node.items():
            if key in _KEYWORDS_WITH_MEMBERS and isinstance(value, dict):
                for name, member in value.items():
                    visit(member, f"{path}/{key}/{_pointer_segment(str(name))}")
            elif key in _KEYWORDS_WITH_LIST and isinstance(value, (list, tuple)):
                for index, member in enumerate(value):
                    visit(member, f"{path}/{key}/{index}")
            elif key in _KEYWORDS_WITH_CHILD:
                visit(value, f"{path}/{key}")

    visit(root, "")
    return sites


def validation_schema(node: Any) -> Any:
    """The schema as the host validates arguments with it: the same tree with TypeBox's ``~kind`` appended to every node that has one. JSON drops those, but the host's port of
    Value.Convert reads them (the Node runtime sends the same form as ``validation_parameters``)."""
    if isinstance(node, TSchemaArray):
        return [validation_schema(item) for item in node]
    if isinstance(node, dict):
        out = {key: validation_schema(member) for key, member in node.items()}
        if isinstance(node, TSchema) and "~kind" in node.hidden:
            out["~kind"] = node.hidden["~kind"]
        return out
    if isinstance(node, (list, tuple)):
        return [validation_schema(item) for item in node]
    return node


def count_refinements(node: Any) -> int:
    """Every refinement in a schema, wherever it hangs: more than :func:`refinement_sites` finds means a refinement sits under a keyword whose refinements the host does not run."""
    total = 0
    if isinstance(node, (TSchema, TSchemaArray)):
        total += len(node.hidden.get("~refine") or [])
    if isinstance(node, dict):
        total += sum(count_refinements(member) for member in node.values())
    elif isinstance(node, (list, tuple)):
        total += sum(count_refinements(member) for member in node)
    return total


class TSchemaArray(list):
    """A schema that is a list: Pi's ``Type.Unsafe``, ``Type.Optional`` and the other updates accept an array, whose own members JSON.stringify ignores. ``hidden`` holds the array's
    ``length`` as well as the markers."""

    __slots__ = ("hidden",)

    def __init__(self, items: Any = (), hidden: Optional[Dict[str, Any]] = None) -> None:
        super().__init__(items)
        self.hidden: Dict[str, Any] = dict(hidden) if hidden else {}

    @property
    def kind(self) -> str:
        kind = self.hidden.get("~kind")
        return kind if isinstance(kind, str) else ""

    def to_json(self) -> str:
        return type_to_json(self)


def type_to_json(value: Any) -> str:
    """JSON.stringify of a schema (or any value built from dicts, lists, strings, numbers, booleans and None): the dict's insertion order, numbers through the one JavaScript number formatter
    of the SDK (a number beyond 2**53 is the float it rounds to, as JavaScript reads it), strings with only the escapes JSON.stringify writes."""
    if value is None:
        return "null"
    if value is True:
        return "true"
    if value is False:
        return "false"
    if isinstance(value, str):
        return json.dumps(value, ensure_ascii=False)
    if isinstance(value, (int, float)):
        return _js_number_string(value)
    if isinstance(value, dict):
        return "{" + ",".join(json.dumps(str(key), ensure_ascii=False) + ":" + type_to_json(member) for key, member in value.items()) + "}"
    if isinstance(value, (list, tuple)):
        return "[" + ",".join(type_to_json(member) for member in value) + "]"
    raise TypeError(f"cannot write a {type(value).__name__} as JSON")


def _index_key(key: str) -> Optional[int]:
    """The array index a canonical array-index key stands for: a JavaScript object orders those keys before every other, ascending."""
    if not key or len(key) > 10 or not key.isascii() or not key.isdigit() or (len(key) > 1 and key[0] == "0"):
        return None
    number = int(key)
    return number if number < 2**32 - 1 else None


def _js_order(items: Mapping[str, Any]) -> Dict[str, Any]:
    keys = list(items)
    indexes = sorted((key for key in keys if _index_key(key) is not None), key=lambda key: _index_key(key) or 0)
    ordered = indexes + [key for key in keys if _index_key(key) is None]
    return {key: items[key] for key in ordered}


def _create(kind: str, enumerable: Sequence[Any], options: Optional[Mapping[str, Any]]) -> TSchema:
    """TypeBox's ``Memory.Create(hidden, enumerable, options)``: ``{...enumerable, ...options}`` with the hidden members attached."""
    merged: Dict[str, Any] = {}
    for index in range(0, len(enumerable), 2):
        merged[enumerable[index]] = enumerable[index + 1]
    if options:
        merged.update(options)
    return TSchema(_js_order(merged), {"~kind": kind})


def _update(current: Any, hidden: Optional[Mapping[str, Any]], enumerable: Optional[Mapping[str, Any]]) -> Any:
    """TypeBox's ``Memory.Update``: a copy of ``current`` with the hidden members set and the enumerable ones assigned (replaced in place, or appended)."""
    if isinstance(current, (list, tuple)):
        # A copy of the array, whose own length is a hidden member, as JavaScript's is; the options are members JSON.stringify ignores.
        # TypeBox's Clone of an array is ``map``, which drops every non-index member, so a marker set by one update is gone after the next: Type.optional, readonly and immutable
        # (two updates) leave an array with only its length, and Type.unsafe (one) keeps ``~unsafe``.
        kept = {"length": len(current)}
        if hidden and ("~unsafe" in hidden or "~refine" in hidden):
            kept.update(hidden)
        return TSchemaArray(list(current), kept)  # type: ignore[return-value]
    if isinstance(current, TSchema):
        merged, kept = dict(current), dict(current.hidden)
    elif isinstance(current, dict):
        merged, kept = dict(current), {}
    else:
        raise TypeError(f"cannot update a {type(current).__name__}, a schema is an object")
    if hidden:
        kept.update(hidden)
    if enumerable:
        merged.update(enumerable)
    return TSchema(_js_order(merged), kept)


def _is_schema(value: Any) -> bool:
    """TypeBox's ``IsSchema``: any object (a list counts, None does not)."""
    return value is not None and not isinstance(value, (bool, int, float, str, bytes))


def _member(value: Any, name: str) -> Any:
    """``(found, member)`` of an own member of ``value``, enumerable or hidden."""
    if isinstance(value, TSchema):
        if name in value.hidden:
            return True, value.hidden[name]
        if name in value:
            return True, value[name]
        return False, None
    if isinstance(value, TSchemaArray):
        return (True, value.hidden[name]) if name in value.hidden else (False, None)
    if isinstance(value, dict) and name in value:
        return True, value[name]
    return False, None


def _is_number(value: Any) -> bool:
    return isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value)


def _js_number(value: Any) -> Any:
    """A JavaScript number as the value JSON writes: an integral float is the integer (``2.0`` is ``2``)."""
    if isinstance(value, float) and value.is_integer() and abs(value) < 1e21:
        return int(value)
    return value


def _js_number_key(number: Any) -> str:
    """``String(number)``: the property name a JavaScript number makes."""
    return _js_number_string(number)


_NUMERIC_STRING = re.compile(r"^(?:[+-]?(?:Infinity|(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?)|0[xX][0-9a-fA-F]+|0[oO][0-7]+|0[bB][01]+)$")
_JS_SPACE = " \t\n\v\f\r\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"


def _is_nan_string(key: str) -> bool:
    """``isNaN(key)`` for a string: whether ``Number(key)`` is NaN. Number trims whitespace and reads the empty string as 0."""
    trimmed = key.strip(_JS_SPACE)
    return trimmed != "" and _NUMERIC_STRING.match(trimmed) is None


_INTEGER_KEY = r"^-?(?:0|[1-9][0-9]*)$"
_NUMBER_KEY = r"^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$"
_STRING_KEY = r"^.*$"


def _create_record(pattern: str, value: Any) -> TSchema:
    return _create("Record", ["type", "object", "patternProperties", {pattern: value}], None)


def _flatten_union(any_of: Any) -> List[Any]:
    out: List[Any] = []
    for member in any_of or []:
        if Type.is_union(member):
            out.extend(_flatten_union(_member(member, "anyOf")[1]))
        else:
            out.append(member)
    return out


def _deferred(action: str, parameters: List[Any], options: Optional[Mapping[str, Any]]) -> TSchema:
    return _create("Deferred", ["type", "deferred", "action", action, "parameters", parameters, "options", dict(options) if options else {}], None)


class _Type:
    """pi-ai's ``Type``: ``Type.object``, ``Type.string``, ``Type.optional``, ... Pi's ``Type.With`` is ``with_`` and ``Type.BigInt`` is ``big_int``; the guards are ``is_string`` and so on.
    The JSON Schema each returns is what Pi's ``Type`` returns, member order included (test/parity/sdk-vectors/pi-ai/Type.json)."""

    # guards
    @staticmethod
    def is_schema(value: Any) -> bool:
        return _is_schema(value)

    @staticmethod
    def is_kind(value: Any, kind: str) -> bool:
        if not _is_schema(value):
            return False
        found, have = _member(value, "~kind")
        return found and have == kind

    @staticmethod
    def is_optional(value: Any) -> bool:
        return _is_schema(value) and _member(value, "~optional")[0]

    @staticmethod
    def is_readonly(value: Any) -> bool:
        return _is_schema(value) and _member(value, "~readonly")[0]

    @staticmethod
    def is_immutable(value: Any) -> bool:
        return _is_schema(value) and _member(value, "~immutable")[0]

    @staticmethod
    def is_unsafe(value: Any) -> bool:
        if isinstance(value, (list, tuple)):
            return False
        found, have = _member(value, "~unsafe")
        return found and have is None

    @staticmethod
    def is_enum_value(value: Any) -> bool:
        return isinstance(value, str) or _is_number(value)

    # constructors
    @staticmethod
    def any(options: Optional[Mapping[str, Any]] = None) -> TSchema:
        return _create("Any", [], options)

    @staticmethod
    def unknown(options: Optional[Mapping[str, Any]] = None) -> TSchema:
        return _create("Unknown", [], options)

    @staticmethod
    def string(options: Optional[Mapping[str, Any]] = None) -> TSchema:
        return _create("String", ["type", "string"], options)

    @staticmethod
    def number(options: Optional[Mapping[str, Any]] = None) -> TSchema:
        return _create("Number", ["type", "number"], options)

    @staticmethod
    def integer(options: Optional[Mapping[str, Any]] = None) -> TSchema:
        return _create("Integer", ["type", "integer"], options)

    @staticmethod
    def boolean(options: Optional[Mapping[str, Any]] = None) -> TSchema:
        return _create("Boolean", ["type", "boolean"], options)

    @staticmethod
    def null(options: Optional[Mapping[str, Any]] = None) -> TSchema:
        return _create("Null", ["type", "null"], options)

    @staticmethod
    def never(options: Optional[Mapping[str, Any]] = None) -> TSchema:
        return _create("Never", ["not", {}], options)

    @staticmethod
    def undefined(options: Optional[Mapping[str, Any]] = None) -> TSchema:
        return _create("Undefined", ["type", "undefined"], options)

    @staticmethod
    def void(options: Optional[Mapping[str, Any]] = None) -> TSchema:
        return _create("Void", ["type", "void"], options)

    @staticmethod
    def symbol(options: Optional[Mapping[str, Any]] = None) -> TSchema:
        return _create("Symbol", ["type", "symbol"], options)

    @staticmethod
    def big_int(options: Optional[Mapping[str, Any]] = None) -> TSchema:
        return _create("BigInt", ["type", "bigint"], options)

    @staticmethod
    def ref(ref: str, options: Optional[Mapping[str, Any]] = None) -> TSchema:
        return _create("Ref", ["$ref", ref], options)

    @staticmethod
    def array(items: Any, options: Optional[Mapping[str, Any]] = None) -> TSchema:
        return _create("Array", ["type", "array", "items", items], options)

    @staticmethod
    def union(any_of: Sequence[Any], options: Optional[Mapping[str, Any]] = None) -> TSchema:
        return _create("Union", ["anyOf", list(any_of)], options)

    @staticmethod
    def intersect(types: Sequence[Any], options: Optional[Mapping[str, Any]] = None) -> TSchema:
        return _create("Intersect", ["allOf", list(types)], options)

    @staticmethod
    def object(properties: Optional[Mapping[str, Any]] = None, options: Optional[Mapping[str, Any]] = None) -> TSchema:
        """``Type.Object(properties, options)``: ``required`` lists, in the object's key order, the properties that are not ``~optional`` and is left out when there are none.
        Keys that are array indexes come first and ascending, as in a JavaScript object."""
        ordered = _js_order(properties or {})
        required = [key for key in ordered if not Type.is_optional(ordered[key])]
        enumerable: List[Any] = ["type", "object"]
        if required:
            enumerable += ["required", required]
        enumerable += ["properties", ordered]
        return _create("Object", enumerable, options)

    @staticmethod
    def optional(schema: Any) -> TSchema:
        return _update(schema, {"~optional": True}, None)

    @staticmethod
    def readonly(schema: Any) -> TSchema:
        return _update(schema, {"~readonly": True}, None)

    @staticmethod
    def immutable(schema: Any) -> TSchema:
        return _update(schema, {"~immutable": True}, None)

    @staticmethod
    def refine(schema: Any, check: Callable[[Any], Any], error: Optional[Callable[[Any], str]] = None) -> TSchema:
        """``Type.Refine(type, check, error?)``: a copy of the schema whose hidden ``~refine`` list has the refinement appended; the schema's JSON does not change. The refinements run
        when the host validates a tool call's arguments (see :meth:`Extension.tool`)."""
        existing = schema.hidden.get("~refine", []) if isinstance(schema, TSchema) else []
        return _update(schema, {"~refine": [*existing, Refinement(check, error)]}, None)

    @staticmethod
    def is_refine(value: Any) -> bool:
        return isinstance(value, TSchema) and bool(value.hidden.get("~refine"))

    @staticmethod
    def unsafe(schema: Any) -> TSchema:
        """``Type.Unsafe(schema)``: a copy of the object (or list) schema with ``~unsafe`` (None). Any other value raises TypeError."""
        return _update(schema, {"~unsafe": None}, None)

    @staticmethod
    def with_(schema: Any, options: Optional[Mapping[str, Any]] = None) -> TSchema:
        """``Type.With(type, options)``: a copy with the options assigned (a Ref is not evaluated: the result is TypeBox's deferred With)."""
        if Type.is_ref(schema):
            return _deferred("With", [schema, options], None)
        return _update(schema, None, options)

    @staticmethod
    def literal(value: Any, options: Optional[Mapping[str, Any]] = None) -> TSchema:
        """``Type.Literal(value, options)``: ``{"type": <string|number|boolean>, "const": value}``. Anything else raises TypeError("Invalid Literal value")."""
        if isinstance(value, str):
            name = "string"
        elif isinstance(value, bool):
            name = "boolean"
        elif _is_number(value):
            name, value = "number", _js_number(value)
        else:
            raise TypeError("Invalid Literal value")
        return _create("Literal", ["type", name, "const", value], options)

    @staticmethod
    def enum(value: Any, options: Optional[Mapping[str, Any]] = None) -> TSchema:
        """``Type.Enum(value, options)``: ``{"enum": values}``. ``value`` is a list of values, or a dict in the manner of a TypeScript enum, whose values for the keys that are not
        numbers are the enum's (a TypeScript numeric enum also maps each number back to its name, which is dropped)."""
        if isinstance(value, dict):
            values = [value[key] for key in _js_order(value) if _is_nan_string(key)]
        elif isinstance(value, (list, tuple)):
            values = list(value)
        else:
            raise TypeError("Type.enum takes a list or a dict")
        return _create("Enum", ["enum", values], options)

    @staticmethod
    def record(key: Any, value: Any, options: Optional[Mapping[str, Any]] = None) -> TSchema:
        """``Type.Record(key, value, options)``: for a string, number or integer key ``{"type": "object", "patternProperties": {<pattern>: value}}``; for a literal, boolean or
        union-of-literals key the object with those properties. A key Pi evaluates through a part of TypeBox this SDK has not ported (an enum, an intersection or a template
        literal) raises NotImplementedError until it is."""
        if Type.is_ref(key):
            return _deferred("Record", [key, value], options)
        return _update(_record_from_key(key, value), None, options)


def _record_from_key(key: Any, value: Any) -> TSchema:
    t = Type
    if t.is_any(key):
        return _create_record(_STRING_KEY, value)
    if t.is_boolean(key):
        return t.object({"true": value, "false": value})
    if t.is_enum(key) or t.is_intersect(key) or t.is_kind(key, "TemplateLiteral"):
        raise NotImplementedError("Type.record: a key of this kind is evaluated by a part of TypeBox this SDK has not ported yet")
    if t.is_integer(key):
        return _create_record(_INTEGER_KEY, value)
    if t.is_literal(key):
        constant = _member(key, "const")[1]
        if isinstance(constant, str):
            return t.object({constant: value})
        if isinstance(constant, bool):
            return t.object({"true" if constant else "false": value})
        if _is_number(constant):
            return t.object({_js_number_key(constant): value})
        return t.object({})
    if t.is_number(key):
        return _create_record(_NUMBER_KEY, value)
    if t.is_union(key):
        members = _flatten_union(_member(key, "anyOf")[1])
        if any(t.is_string(m) or t.is_number(m) or t.is_integer(m) for m in members):
            return _create_record(_STRING_KEY, value)
        properties: Dict[str, Any] = {}
        for member in members:
            if not t.is_literal(member):
                continue
            constant = _member(member, "const")[1]
            if isinstance(constant, str):
                properties[constant] = value
            elif _is_number(constant):
                properties[_js_number_key(constant)] = value
        return t.object(properties)
    if t.is_string(key):
        found, pattern = _member(key, "pattern")
        if found and isinstance(pattern, str):
            return _create_record(pattern, value)
        return _create_record(_STRING_KEY, value)
    return t.object({})


def _install_guards() -> None:
    for name, kind in {
        "any": "Any", "array": "Array", "big_int": "BigInt", "boolean": "Boolean", "enum": "Enum", "integer": "Integer", "intersect": "Intersect", "literal": "Literal",
        "never": "Never", "null": "Null", "number": "Number", "object": "Object", "record": "Record", "ref": "Ref", "string": "String", "symbol": "Symbol",
        "undefined": "Undefined", "union": "Union", "unknown": "Unknown", "void": "Void",
    }.items():
        setattr(_Type, f"is_{name}", staticmethod(lambda value, _kind=kind: _Type.is_kind(value, _kind)))


_install_guards()

#: pi-ai's ``Type``.
Type = _Type()
