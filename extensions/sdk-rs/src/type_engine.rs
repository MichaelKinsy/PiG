//! The members of pi-ai's `Type` that compute a schema (`pick`, `omit`, `partial`, `required`, `key_of`, `index`, `mapped`, `conditional`, `extends`, `evaluate`, `instantiate`,
//! `cyclic`, `module`, `script`, ...), served by TypeBox's type engine, which the host runs: one port of TypeBox's `build/type/{action,engine,extends,script}` for every SDK
//! (`internal/typebox/engine`). A call encodes its arguments in the engine's wire form, asks the host (the `type.engine` call, which the host answers before the extension
//! registers, so a schema built before `run` works) and decodes the schema the host returns: the enumerable members in the order TypeBox wrote them and the hidden ones (`~kind`,
//! `~optional`, ...). What TypeBox throws is the `Err` with its message.

use std::collections::BTreeMap;

use serde_json::{Map, Value, json};

use crate::type_schema::{BigInt, Refinement, TSchema, TValue, Type};

const ENVELOPE: &str = "\u{0}";
const CALL_TYPE_ENGINE: &str = "type.engine";

type Options<'a> = Option<&'a Map<String, Value>>;

impl TValue {
    /// A list of members, such as the parameters of a function or the types of a tuple.
    pub fn list<T: Into<TValue>>(items: impl IntoIterator<Item = T>) -> TValue {
        TValue::List(items.into_iter().map(Into::into).collect())
    }

    /// An object of members in the order given, such as a context or the declarations of a module.
    pub fn object<K: Into<String>, V: Into<TValue>>(pairs: impl IntoIterator<Item = (K, V)>) -> TValue {
        TValue::Object(pairs.into_iter().map(|(key, value)| (key.into(), value.into())).collect())
    }
}

impl<const N: usize> From<[&str; N]> for TValue {
    fn from(keys: [&str; N]) -> Self {
        TValue::List(keys.iter().map(|key| TValue::Json(Value::String((*key).to_string()))).collect())
    }
}
impl From<Vec<&str>> for TValue {
    fn from(keys: Vec<&str>) -> Self {
        TValue::List(keys.into_iter().map(|key| TValue::Json(Value::String(key.to_string()))).collect())
    }
}
impl From<Vec<TValue>> for TValue {
    fn from(items: Vec<TValue>) -> Self {
        TValue::List(items)
    }
}
impl From<Vec<TSchema>> for TValue {
    fn from(items: Vec<TSchema>) -> Self {
        TValue::List(items.into_iter().map(TValue::Schema).collect())
    }
}
impl From<String> for TValue {
    fn from(text: String) -> Self {
        TValue::Json(Value::String(text))
    }
}
impl From<f64> for TValue {
    fn from(number: f64) -> Self {
        TValue::Json(json!(number))
    }
}
impl From<bool> for TValue {
    fn from(flag: bool) -> Self {
        TValue::Json(Value::Bool(flag))
    }
}
impl From<&Map<String, Value>> for TValue {
    fn from(map: &Map<String, Value>) -> Self {
        TValue::Json(Value::Object(map.clone()))
    }
}

// ---- wire: encode ----

fn encode_object(members: Map<String, Value>) -> Value {
    if members.keys().any(|key| key.starts_with(ENVELOPE)) {
        let mut envelope = Map::new();
        envelope.insert(ENVELOPE.to_string(), json!("o"));
        envelope.insert("e".to_string(), Value::Object(members));
        return Value::Object(envelope);
    }
    Value::Object(members)
}

fn encode_json(value: &Value) -> Value {
    match value {
        Value::Array(items) => Value::Array(items.iter().map(encode_json).collect()),
        Value::Object(map) => encode_object(map.iter().map(|(key, member)| (key.clone(), encode_json(member))).collect()),
        other => other.clone(),
    }
}

fn encode(value: &TValue, refs: &mut Vec<Refinement>) -> Value {
    match value {
        TValue::Json(value) => encode_json(value),
        TValue::BigInt(value) => json!({ENVELOPE: "n", "v": value.digits()}),
        TValue::List(items) => Value::Array(items.iter().map(|item| encode(item, refs)).collect()),
        TValue::Object(pairs) => encode_object(pairs.iter().map(|(key, member)| (key.clone(), encode(member, refs))).collect()),
        TValue::Schema(schema) => {
            if let Some(items) = &schema.items {
                return Value::Array(items.iter().map(|item| encode(item, refs)).collect());
            }
            let mut hidden = Map::new();
            if let Some(kind) = schema.hidden.get("~kind") {
                hidden.insert("~kind".to_string(), kind.clone());
            }
            for (name, member) in &schema.hidden {
                if name != "~kind" {
                    hidden.insert(name.clone(), member.clone());
                }
            }
            if !schema.refine.is_empty() {
                let mut list = Vec::new();
                for refinement in &schema.refine {
                    refs.push(refinement.clone());
                    list.push(json!({ENVELOPE: "f", "i": refs.len() - 1}));
                }
                hidden.insert("~refine".to_string(), Value::Array(list));
            }
            let enumerable: Map<String, Value> = schema.fields.iter().map(|(key, member)| (key.clone(), encode(member, refs))).collect();
            let mut envelope = Map::new();
            envelope.insert(ENVELOPE.to_string(), json!("o"));
            if !hidden.is_empty() {
                envelope.insert("h".to_string(), Value::Object(hidden));
            }
            envelope.insert("e".to_string(), Value::Object(enumerable));
            Value::Object(envelope)
        }
    }
}

// ---- wire: decode ----

/// The member of a reply as a [`TValue`]; `None` is JavaScript's undefined, which JSON.stringify leaves out of an object and writes null in an array.
fn decode(value: &Value, refs: &[Refinement]) -> Result<Option<TValue>, String> {
    match value {
        Value::Array(items) => {
            let mut out = Vec::with_capacity(items.len());
            for item in items {
                out.push(decode(item, refs)?.unwrap_or(TValue::Json(Value::Null)));
            }
            Ok(Some(TValue::List(out)))
        }
        Value::Object(map) => {
            if map.keys().next().map(String::as_str) == Some(ENVELOPE) {
                return match map.get(ENVELOPE).and_then(Value::as_str) {
                    Some("u") => Ok(None),
                    Some("n") => map.get("v").and_then(Value::as_str).ok_or_else(|| "the host answered a bigint without digits".to_string()).and_then(BigInt::parse).map(|value| Some(TValue::BigInt(value))),
                    Some("o") => decode_schema(map, refs).map(|schema| Some(TValue::Schema(schema))),
                    other => Err(format!("the host answered an unknown envelope {other:?}")),
                };
            }
            Ok(Some(TValue::Object(decode_members(map, refs)?)))
        }
        other => Ok(Some(TValue::Json(other.clone()))),
    }
}

fn decode_members(map: &Map<String, Value>, refs: &[Refinement]) -> Result<Vec<(String, TValue)>, String> {
    let mut out = Vec::with_capacity(map.len());
    for (key, member) in map {
        if let Some(member) = decode(member, refs)? {
            out.push((key.clone(), member));
        }
    }
    Ok(out)
}

fn decode_schema(envelope: &Map<String, Value>, refs: &[Refinement]) -> Result<TSchema, String> {
    let mut hidden: BTreeMap<String, Value> = BTreeMap::new();
    let mut refine = Vec::new();
    if let Some(Value::Object(members)) = envelope.get("h") {
        for (name, member) in members {
            if name == "~refine" {
                for item in member.as_array().into_iter().flatten() {
                    let index = item.get("i").and_then(Value::as_u64).unwrap_or(u64::MAX) as usize;
                    refine.push(refs.get(index).cloned().ok_or("the host answered an unknown refinement")?);
                }
            } else {
                hidden.insert(name.clone(), member.clone());
            }
        }
    }
    let fields = match envelope.get("e") {
        Some(Value::Object(members)) => decode_members(members, refs)?,
        _ => Vec::new(),
    };
    Ok(TSchema { fields, hidden, refine, items: None })
}

/// The wire form of a value, for the tests of the reader and writer.
#[cfg(test)]
pub(crate) fn encode_wire(value: &TValue) -> Value {
    encode(value, &mut Vec::new())
}

/// A reply's value as a [`TValue`], for the tests of the reader and writer.
#[cfg(test)]
pub(crate) fn decode_wire(value: &Value, refs: &[Refinement]) -> Result<TValue, String> {
    decode(value, refs)?.ok_or_else(|| "undefined".to_string())
}

/// Run `member` of the type engine on `args` and return the value it answers.
pub(crate) fn engine_call(member: &str, args: Vec<TValue>) -> Result<TValue, String> {
    let mut refs = Vec::new();
    let encoded: Vec<Value> = args.iter().map(|arg| encode(arg, &mut refs)).collect();
    let reply = crate::host_helpers::helper_call(CALL_TYPE_ENGINE, json!({"member": member, "args": encoded})).map_err(|error| format!("Type.{member}: {error}"))?.unwrap_or(Value::Null);
    if let Some(error) = reply.get("error") {
        return Err(error.get("message").and_then(Value::as_str).unwrap_or_default().to_string());
    }
    let value = reply.get("value").ok_or_else(|| format!("Type.{member}: the host answered no value"))?;
    decode(value, &refs)?.ok_or_else(|| format!("Type.{member}: the host answered undefined"))
}

/// A result that is an object, as a schema: a plain object (Module's result, an Extends answer's members) has no hidden members.
fn as_schema(value: TValue) -> Result<TSchema, String> {
    match value {
        TValue::Schema(schema) => Ok(schema),
        TValue::Object(fields) => Ok(TSchema { fields, hidden: BTreeMap::new(), refine: Vec::new(), items: None }),
        other => Err(format!("TypeError: the engine answered {other:?}, not a schema")),
    }
}

fn run(member: &str, mut args: Vec<TValue>, options: Options) -> Result<TSchema, String> {
    if let Some(options) = options {
        args.push(TValue::Json(Value::Object(options.clone())));
    }
    as_schema(engine_call(member, args)?)
}

impl Type {
    /// `Type.Call(target, arguments)`: a generic called with arguments, instantiated.
    pub fn call(target: impl Into<TValue>, arguments: impl Into<TValue>) -> Result<TSchema, String> {
        run("Call", vec![target.into(), arguments.into()], None)
    }
    /// `Type.Capitalize(type)`.
    pub fn capitalize(schema: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Capitalize", vec![schema.into()], options)
    }
    /// `Type.Lowercase(type)`.
    pub fn lowercase(schema: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Lowercase", vec![schema.into()], options)
    }
    /// `Type.Uncapitalize(type)`.
    pub fn uncapitalize(schema: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Uncapitalize", vec![schema.into()], options)
    }
    /// `Type.Uppercase(type)`.
    pub fn uppercase(schema: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Uppercase", vec![schema.into()], options)
    }
    /// `Type.Conditional(left, right, trueType, falseType)`: TypeScript's `left extends right ? trueType : falseType`.
    pub fn conditional(left: impl Into<TValue>, right: impl Into<TValue>, true_type: impl Into<TValue>, false_type: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Conditional", vec![left.into(), right.into(), true_type.into(), false_type.into()], options)
    }
    /// `Type.Constructor(parameters, instanceType)`.
    pub fn constructor(parameters: impl Into<TValue>, instance_type: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Constructor", vec![parameters.into(), instance_type.into()], options)
    }
    /// `Type.ConstructorParameters(type)`.
    pub fn constructor_parameters(schema: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("ConstructorParameters", vec![schema.into()], options)
    }
    /// `Type.Parameters(type)`.
    pub fn parameters(schema: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Parameters", vec![schema.into()], options)
    }
    /// `Type.InstanceType(type)`.
    pub fn instance_type(schema: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("InstanceType", vec![schema.into()], options)
    }
    /// `Type.ReturnType(type)`.
    pub fn return_type(schema: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("ReturnType", vec![schema.into()], options)
    }
    /// `Type.Cyclic(defs, ref)`: recursive definitions, each given its `$id`.
    pub fn cyclic(defs: impl Into<TValue>, reference: &str, options: Options) -> Result<TSchema, String> {
        run("Cyclic", vec![defs.into(), reference.into()], options)
    }
    /// `Type.Dependent(if, then, else)`.
    pub fn dependent(if_type: impl Into<TValue>, then_type: impl Into<TValue>, else_type: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Dependent", vec![if_type.into(), then_type.into(), else_type.into()], options)
    }
    /// `Type.Evaluate(type)`: unions and intersections simplified.
    pub fn evaluate(schema: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Evaluate", vec![schema.into()], options)
    }
    /// `Type.Exclude(left, right)`.
    pub fn exclude(left: impl Into<TValue>, right: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Exclude", vec![left.into(), right.into()], options)
    }
    /// `Type.Extract(left, right)`.
    pub fn extract(left: impl Into<TValue>, right: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Extract", vec![left.into(), right.into()], options)
    }
    /// `Type.Extends(inferred, left, right)`: an ExtendsTrue, ExtendsUnion or ExtendsFalse value; `inferred` holds the types an `infer` already captured (`None` is none).
    pub fn extends(inferred: Option<TValue>, left: impl Into<TValue>, right: impl Into<TValue>) -> Result<TSchema, String> {
        run("Extends", vec![inferred.unwrap_or_else(|| TValue::Object(Vec::new())), left.into(), right.into()], None)
    }
    /// `Type.Function(parameters, returnType)`.
    pub fn function(parameters: impl Into<TValue>, return_type: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Function", vec![parameters.into(), return_type.into()], options)
    }
    /// `Type.Generic(parameters, expression)`.
    pub fn generic(parameters: impl Into<TValue>, expression: impl Into<TValue>) -> Result<TSchema, String> {
        run("Generic", vec![parameters.into(), expression.into()], None)
    }
    /// `Type.Identifier(name)`.
    pub fn identifier(name: &str) -> Result<TSchema, String> {
        run("Identifier", vec![name.into()], None)
    }
    /// `Type.Index(type, indexer)`: TypeScript's `type[indexer]`; `indexer` is a schema or a list of keys.
    pub fn index(schema: impl Into<TValue>, indexer: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Index", vec![schema.into(), indexer.into()], options)
    }
    /// `Type.Infer(name)` or `Type.Infer(name, extends)`.
    pub fn infer(name: &str, extends: Option<TValue>) -> Result<TSchema, String> {
        let mut args: Vec<TValue> = vec![name.into()];
        args.extend(extends);
        run("Infer", args, None)
    }
    /// `Type.Instantiate(context, type)`: the references of the type resolved in the context and the deferred actions it holds computed.
    pub fn instantiate(context: impl Into<TValue>, schema: impl Into<TValue>) -> Result<TSchema, String> {
        run("Instantiate", vec![context.into(), schema.into()], None)
    }
    /// `Type.Interface(heritage, properties)`: TypeScript's `interface X extends heritage { properties }`.
    pub fn interface(heritage: impl Into<TValue>, properties: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Interface", vec![heritage.into(), properties.into()], options)
    }
    /// `Type.KeyOf(type)`: TypeScript's `keyof type`.
    pub fn key_of(schema: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("KeyOf", vec![schema.into()], options)
    }
    /// `Type.Mapped(identifier, type, as, property)`: TypeScript's `{ [identifier in type as as]: property }`.
    pub fn mapped(identifier: impl Into<TValue>, schema: impl Into<TValue>, as_: impl Into<TValue>, property: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Mapped", vec![identifier.into(), schema.into(), as_.into(), property.into()], options)
    }
    /// `Type.Module(declarations)`: the declarations instantiated; the result is an object of the instantiated types.
    pub fn module(declarations: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Module", vec![declarations.into()], options)
    }
    /// `Type.NonNullable(type)`.
    pub fn non_nullable(schema: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("NonNullable", vec![schema.into()], options)
    }
    /// `Type.Omit(type, keys)`; `keys` is a list of keys or a schema of them.
    pub fn omit(schema: impl Into<TValue>, keys: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Omit", vec![schema.into(), keys.into()], options)
    }
    /// `Type.Pick(type, keys)`; `keys` is a list of keys or a schema of them.
    pub fn pick(schema: impl Into<TValue>, keys: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Pick", vec![schema.into(), keys.into()], options)
    }
    /// `Type.Parameter(name)`, `(name, extends)` or `(name, extends, equals)`: a generic's parameter.
    pub fn parameter(name: &str, extends: Option<TValue>, equals: Option<TValue>) -> Result<TSchema, String> {
        let mut args: Vec<TValue> = vec![name.into()];
        args.extend(extends);
        if args.len() > 1 {
            args.extend(equals);
        }
        run("Parameter", args, None)
    }
    /// `Type.Partial(type)`.
    pub fn partial(schema: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Partial", vec![schema.into()], options)
    }
    /// `Type.Required(type)`.
    pub fn required(schema: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Required", vec![schema.into()], options)
    }
    /// `Type.ReadonlyObject(type)`.
    pub fn readonly_object(schema: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("ReadonlyObject", vec![schema.into()], options)
    }
    /// `Type.ReadonlyType(type)`.
    pub fn readonly_type(schema: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("ReadonlyType", vec![schema.into()], options)
    }
    /// `Type.RecordKey(record)`: the type of a record's keys.
    pub fn record_key(record: impl Into<TValue>) -> Result<TSchema, String> {
        run("RecordKey", vec![record.into()], None)
    }
    /// `Type.RecordPattern(record)`: the pattern of a record's keys.
    pub fn record_pattern(record: impl Into<TValue>) -> Result<String, String> {
        match engine_call("RecordPattern", vec![record.into()])? {
            TValue::Json(Value::String(pattern)) => Ok(pattern),
            other => Err(format!("TypeError: the engine answered {other:?}, not a pattern")),
        }
    }
    /// `Type.RecordValue(record)`: a record's value type.
    pub fn record_value(record: impl Into<TValue>) -> Result<TSchema, String> {
        run("RecordValue", vec![record.into()], None)
    }
    /// `Type.Rest(type)`: a `...type` element of a tuple or parameter list.
    pub fn rest(schema: impl Into<TValue>) -> Result<TSchema, String> {
        run("Rest", vec![schema.into()], None)
    }
    /// `Type.Script(script)`, `(script, options)`, `(context, script)` or `(context, script, options)`: TypeScript type text, parsed and instantiated. Any other argument count is
    /// an error, as Pi's is.
    pub fn script(args: Vec<TValue>) -> Result<TSchema, String> {
        run("Script", args, None)
    }
    /// `Type.TemplateLiteral(input)`: template literal type text such as `a${string}`, or a list of types.
    pub fn template_literal(input: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("TemplateLiteral", vec![input.into()], options)
    }
    /// `Type.This()`.
    pub fn this(options: Options) -> Result<TSchema, String> {
        run("This", vec![], options)
    }
    /// `Type.Tuple(types)`.
    pub fn tuple(types: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        run("Tuple", vec![types.into()], options)
    }
}

/// `Type.ExtendsResult`: the values `Type::extends` answers with and the guards and matcher over them. They compute nothing the host has to, so they run here.
pub struct ExtendsResult;

impl ExtendsResult {
    fn outcome(kind: &str, inferred: Option<TValue>) -> TSchema {
        crate::type_schema::outcome(kind, inferred)
    }
    /// `ExtendsResult.ExtendsTrue(inferred)`.
    pub fn extends_true(inferred: Option<TValue>) -> TSchema {
        Self::outcome("ExtendsTrue", Some(inferred.unwrap_or_else(|| TValue::Object(Vec::new()))))
    }
    /// `ExtendsResult.ExtendsUnion(inferred)`.
    pub fn extends_union(inferred: Option<TValue>) -> TSchema {
        Self::outcome("ExtendsUnion", Some(inferred.unwrap_or_else(|| TValue::Object(Vec::new()))))
    }
    /// `ExtendsResult.ExtendsFalse()`.
    pub fn extends_false() -> TSchema {
        Self::outcome("ExtendsFalse", None)
    }
    fn has_inferred(value: &TValue) -> bool {
        value.member("inferred").is_some_and(|inferred| Type::is_schema(inferred))
    }
    /// `ExtendsResult.IsExtendsUnion`.
    pub fn is_extends_union(value: impl Into<TValue>) -> bool {
        let value = value.into();
        Type::is_kind(value.clone(), "ExtendsUnion") && Self::has_inferred(&value)
    }
    /// `ExtendsResult.IsExtendsTrue`.
    pub fn is_extends_true(value: impl Into<TValue>) -> bool {
        let value = value.into();
        Type::is_kind(value.clone(), "ExtendsTrue") && Self::has_inferred(&value)
    }
    /// `ExtendsResult.IsExtendsFalse`.
    pub fn is_extends_false(value: impl Into<TValue>) -> bool {
        Type::is_kind(value, "ExtendsFalse")
    }
    /// `ExtendsResult.IsExtendsTrueLike`.
    pub fn is_extends_true_like(value: impl Into<TValue>) -> bool {
        let value = value.into();
        Self::is_extends_union(value.clone()) || Self::is_extends_true(value)
    }
    /// `ExtendsResult.Match(result, onTrue, onFalse)`: `on_true` with the inferred types of a true-like result, `on_false` otherwise.
    pub fn match_<T>(result: impl Into<TValue>, on_true: impl FnOnce(TValue) -> T, on_false: impl FnOnce() -> T) -> T {
        let result = result.into();
        if Self::is_extends_true_like(result.clone()) {
            return on_true(result.member("inferred").unwrap_or(TValue::Json(Value::Null)));
        }
        on_false()
    }
}
