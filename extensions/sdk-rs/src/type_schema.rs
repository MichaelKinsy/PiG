//! pi-ai's `Type` (TypeBox 1.3.27's builder namespace).
//!
//! A [`TSchema`] is the JSON Schema TypeBox writes, its members in the order TypeBox wrote them (`to_json` of it is what `JSON.stringify` of Pi's value is), together with TypeBox's
//! hidden members (`~kind`, `~optional`, `~readonly`, `~immutable`, `~unsafe`). JSON drops those, but the constructors and the guards read them: `Type::object` builds `required` from
//! the properties that are not `~optional`, and `Type::is_string` is true for `Type::string(None)` and false for the JSON `{"type":"string"}`. A `TSchema` is not changed after it is
//! built: every constructor returns a new one. The Codec family (`Codec`, `Decode`, `Encode`, `DecodeBuilder`, `EncodeBuilder`, `IsCodec`) is a numbered divergence (D198) and is absent.

use serde::{Serialize, Serializer};
use serde_json::{Map, Number, Value, json};
use std::collections::BTreeMap;
use std::sync::Arc;

/// One entry of a schema's hidden `~refine` list (TypeBox's `{check, error}`): `check` decides whether a value passes and `error` words the failure (`None` is TypeBox's
/// "Refine Error"). Pi runs them while it validates a tool call's arguments; an extension tool's refinements run in this process when the host asks for them. An `Err` is what a
/// thrown JavaScript error is: it ends the validation and the tool call fails with its message.
#[derive(Clone)]
pub struct Refinement {
    pub check: Arc<dyn Fn(&Value) -> Result<bool, String> + Send + Sync>,
    pub error: Option<Arc<dyn Fn(&Value) -> Result<String, String> + Send + Sync>>,
}

impl std::fmt::Debug for Refinement {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("Refinement")
    }
}

impl PartialEq for Refinement {
    fn eq(&self, other: &Self) -> bool {
        Arc::ptr_eq(&self.check, &other.check)
    }
}

/// A schema node that carries refinements: its path in the schema (the keywords that lead to it: `/properties/a`, `/anyOf/1`, `/items`) and the refinements in the order added.
#[derive(Clone, Debug, PartialEq)]
pub struct RefinementSite {
    pub path: String,
    pub refinements: Vec<Refinement>,
}

/// A member of a schema: a plain JSON value, a schema (which keeps its hidden members), a list or an object of members.
#[derive(Clone, Debug, PartialEq)]
pub enum TValue {
    Json(Value),
    Schema(TSchema),
    List(Vec<TValue>),
    Object(Vec<(String, TValue)>),
}

impl From<Value> for TValue {
    fn from(value: Value) -> Self {
        TValue::Json(value)
    }
}
impl From<TSchema> for TValue {
    fn from(schema: TSchema) -> Self {
        TValue::Schema(schema)
    }
}
impl From<&TSchema> for TSchema {
    fn from(schema: &TSchema) -> Self {
        schema.clone()
    }
}
impl From<&TSchema> for TValue {
    fn from(schema: &TSchema) -> Self {
        TValue::Schema(schema.clone())
    }
}
impl From<&str> for TValue {
    fn from(text: &str) -> Self {
        TValue::Json(Value::String(text.to_string()))
    }
}

impl TValue {
    /// The member as JSON: the enumerable members of a schema, nested, in order.
    pub fn to_value(&self) -> Value {
        match self {
            TValue::Json(value) => value.clone(),
            TValue::Schema(schema) => schema.to_value(),
            TValue::List(items) => Value::Array(items.iter().map(TValue::to_value).collect()),
            TValue::Object(pairs) => Value::Object(pairs.iter().map(|(key, value)| (key.clone(), value.to_value())).collect()),
        }
    }

    /// The schema this member is, if it is one.
    pub fn as_schema(&self) -> Option<&TSchema> {
        match self {
            TValue::Schema(schema) => Some(schema),
            _ => None,
        }
    }

    fn member(&self, name: &str) -> Option<TValue> {
        match self {
            TValue::Schema(schema) => schema.hidden.get(name).map(|value| TValue::Json(value.clone())).or_else(|| schema.get(name).cloned()),
            TValue::Json(Value::Object(map)) => map.get(name).map(|value| TValue::Json(value.clone())),
            TValue::Object(pairs) => pairs.iter().find(|(key, _)| key == name).map(|(_, value)| value.clone()),
            _ => None,
        }
    }
}

/// A TypeBox schema value; see the module documentation.
#[derive(Clone, Debug, PartialEq)]
pub struct TSchema {
    fields: Vec<(String, TValue)>,
    hidden: BTreeMap<String, Value>,
    /// The hidden `~refine` list.
    refine: Vec<Refinement>,
    /// The list a schema made of a JavaScript array is (`Type::unsafe_`, `Type::optional` and the other updates accept an array, whose own members JSON.stringify ignores).
    items: Option<Vec<TValue>>,
}

impl Serialize for TSchema {
    fn serialize<S: Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        self.to_value().serialize(serializer)
    }
}

impl TSchema {
    /// The enumerable members as a JSON object, in order, with every nested schema as JSON too.
    pub fn to_value(&self) -> Value {
        if let Some(items) = &self.items {
            return Value::Array(items.iter().map(TValue::to_value).collect());
        }
        Value::Object(self.fields.iter().map(|(key, value)| (key.clone(), value.to_value())).collect())
    }

    /// The schema as the host validates arguments with it: the JSON with TypeBox's `~kind` appended to every node that has one (the Node runtime sends the same form as
    /// `validation_parameters`).
    pub fn to_validation_value(&self) -> Value {
        fn convert(value: &TValue) -> Value {
            match value {
                TValue::Schema(schema) => schema.to_validation_value(),
                TValue::List(items) => Value::Array(items.iter().map(convert).collect()),
                TValue::Object(pairs) => Value::Object(pairs.iter().map(|(key, member)| (key.clone(), convert(member))).collect()),
                TValue::Json(value) => value.clone(),
            }
        }
        if let Some(items) = &self.items {
            return Value::Array(items.iter().map(convert).collect());
        }
        let mut object: Map<String, Value> = self.fields.iter().map(|(key, value)| (key.clone(), convert(value))).collect();
        if let Some(kind) = self.hidden.get("~kind") {
            object.insert("~kind".into(), kind.clone());
        }
        Value::Object(object)
    }

    /// What JSON.stringify of the TypeBox value writes: the members in order, numbers as JavaScript prints them (`1e21`, `1e-7`, `0` for -0) through the SDK's one JavaScript
    /// number formatter, and only the escapes JSON.stringify writes.
    pub fn to_json(&self) -> String {
        let mut out = String::new();
        write_js_json(&self.to_value(), &mut out);
        out
    }

    /// The enumerable member `key`.
    pub fn get(&self, key: &str) -> Option<&TValue> {
        self.fields.iter().find(|(name, _)| name == key).map(|(_, value)| value)
    }

    /// The enumerable keys in order.
    pub fn keys(&self) -> Vec<&str> {
        self.fields.iter().map(|(key, _)| key.as_str()).collect()
    }

    /// The hidden member `name`: the kind as a string, `true` for a marker, null for `~unsafe`.
    pub fn hidden(&self, name: &str) -> Option<&Value> {
        self.hidden.get(name)
    }

    /// The refinements, in the order they were added.
    pub fn refinements(&self) -> &[Refinement] {
        &self.refine
    }

    /// How many refinements the schema has anywhere: more than [`TSchema::refinement_sites`] finds means a refinement sits under a keyword whose refinements the host does not run.
    pub fn refinement_count(&self) -> usize {
        fn count_value(value: &TValue) -> usize {
            match value {
                TValue::Schema(schema) => schema.refinement_count(),
                TValue::List(items) => items.iter().map(count_value).sum(),
                TValue::Object(pairs) => pairs.iter().map(|(_, member)| count_value(member)).sum(),
                TValue::Json(_) => 0,
            }
        }
        self.refine.len() + self.fields.iter().map(|(_, value)| count_value(value)).sum::<usize>() + self.items.iter().flatten().map(count_value).sum::<usize>()
    }

    /// The nodes of the schema that carry refinements, in the order they are found, with the path of each.
    pub fn refinement_sites(&self) -> Vec<RefinementSite> {
        let mut sites = Vec::new();
        visit_refinements(self, "", &mut sites);
        sites
    }

    /// Every hidden member, by name.
    pub fn hidden_members(&self) -> &BTreeMap<String, Value> {
        &self.hidden
    }

    /// The value of `~kind`, or "" for a value without one.
    pub fn kind(&self) -> &str {
        self.hidden.get("~kind").and_then(Value::as_str).unwrap_or("")
    }
}

/// JSON.stringify of a JSON value: object members in order, numbers through [`crate::tui::js_number_string`] (a non-finite number is `null`).
fn write_js_json(value: &Value, out: &mut String) {
    match value {
        Value::Null => out.push_str("null"),
        Value::Bool(flag) => out.push_str(if *flag { "true" } else { "false" }),
        Value::Number(number) => match number.as_f64().filter(|n| n.is_finite()) {
            Some(number) => out.push_str(&crate::tui::js_number_string(number)),
            None => out.push_str("null"),
        },
        Value::String(text) => out.push_str(&serde_json::to_string(text).unwrap_or_default()),
        Value::Array(items) => {
            out.push('[');
            for (index, item) in items.iter().enumerate() {
                if index > 0 {
                    out.push(',');
                }
                write_js_json(item, out);
            }
            out.push(']');
        }
        Value::Object(map) => {
            out.push('{');
            for (index, (key, member)) in map.iter().enumerate() {
                if index > 0 {
                    out.push(',');
                }
                out.push_str(&serde_json::to_string(key).unwrap_or_default());
                out.push(':');
                write_js_json(member, out);
            }
            out.push('}');
        }
    }
}

/// An array index a JavaScript object orders before every other key, ascending.
fn index_key(key: &str) -> Option<u64> {
    if key.is_empty() || key.len() > 10 || !key.bytes().all(|byte| byte.is_ascii_digit()) || (key.len() > 1 && key.starts_with('0')) {
        return None;
    }
    key.parse::<u64>().ok().filter(|number| *number < (1 << 32) - 1)
}

fn js_order(mut pairs: Vec<(String, TValue)>) -> Vec<(String, TValue)> {
    pairs.sort_by_key(|(key, _)| match index_key(key) {
        Some(number) => (0, number),
        None => (1, 0),
    });
    pairs
}

fn set(pairs: &mut Vec<(String, TValue)>, key: &str, value: TValue) {
    match pairs.iter_mut().find(|(name, _)| name == key) {
        Some(slot) => slot.1 = value,
        None => pairs.push((key.to_string(), value)),
    }
}

/// TypeBox's `Memory.Create(hidden, enumerable, options)`: `{...enumerable, ...options}` with the hidden members attached.
fn create(kind: &str, enumerable: Vec<(&str, TValue)>, options: Option<&Map<String, Value>>) -> TSchema {
    let mut fields: Vec<(String, TValue)> = Vec::new();
    for (key, value) in enumerable {
        set(&mut fields, key, value);
    }
    for (key, value) in options.into_iter().flatten() {
        set(&mut fields, key, TValue::Json(value.clone()));
    }
    TSchema { fields: js_order(fields), hidden: BTreeMap::from([("~kind".to_string(), json!(kind))]), items: None, refine: Vec::new() }
}

/// TypeBox's `Memory.Update`: a copy of `current` with the hidden members set and the enumerable ones assigned (replaced in place, or appended).
fn update(current: &TValue, hidden: &[(&str, Value)], enumerable: Option<&Map<String, Value>>) -> Result<TSchema, String> {
    if let Some(items) = match current {
        TValue::List(items) => Some(items.clone()),
        TValue::Json(Value::Array(items)) => Some(items.iter().cloned().map(TValue::Json).collect()),
        TValue::Schema(schema) if schema.items.is_some() => schema.items.clone(),
        _ => None,
    } {
        // A copy of the array, whose own `length` is a hidden member, as JavaScript's is; the options are members JSON.stringify ignores.
        // TypeBox's Clone of an array is `map`, which drops every non-index member, so a marker set by one update is gone after the next: `optional`, `readonly` and
        // `immutable` (two updates) leave an array with only its length, and `unsafe_` (one) keeps `~unsafe`.
        let mut kept = BTreeMap::from([("length".to_string(), json!(items.len()))]);
        for (name, value) in hidden.iter().filter(|(name, _)| *name == "~unsafe") {
            kept.insert((*name).to_string(), value.clone());
        }
        return Ok(TSchema { fields: Vec::new(), hidden: kept, items: Some(items), refine: Vec::new() });
    }
    let refine = match current {
        TValue::Schema(schema) => schema.refine.clone(),
        _ => Vec::new(),
    };
    let (mut fields, mut kept) = match current {
        TValue::Schema(schema) => (schema.fields.clone(), schema.hidden.clone()),
        TValue::Json(Value::Object(map)) => (map.iter().map(|(key, value)| (key.clone(), TValue::Json(value.clone()))).collect(), BTreeMap::new()),
        TValue::Object(pairs) => (pairs.clone(), BTreeMap::new()),
        _ => return Err("TypeError: a schema is an object".to_string()),
    };
    for (name, value) in hidden {
        kept.insert((*name).to_string(), value.clone());
    }
    for (key, value) in enumerable.into_iter().flatten() {
        set(&mut fields, key, TValue::Json(value.clone()));
    }
    Ok(TSchema { fields: js_order(fields), hidden: kept, items: None, refine })
}

fn mark(mut schema: TSchema, name: &str) -> TSchema {
    if schema.items.is_some() {
        // A list schema loses the marker to the second clone TypeBox makes (see `update`).
        schema.hidden = BTreeMap::from([("length".to_string(), json!(schema.items.as_ref().map_or(0, Vec::len)))]);
        return schema;
    }
    schema.hidden.insert(name.to_string(), json!(true));
    schema
}

fn mark_value(value: TValue, name: &str) -> Result<TSchema, String> {
    match value {
        TValue::Schema(schema) => Ok(mark(schema, name)),
        other => update(&other, &[(name, json!(true))], None),
    }
}

fn is_schema(value: &TValue) -> bool {
    !matches!(value, TValue::Json(Value::Null | Value::Bool(_) | Value::Number(_) | Value::String(_)))
}

/// A JavaScript number as the JSON value it writes: an integral number is an integer (`2.0` is `2`).
fn js_number(number: f64) -> Value {
    if number.fract() == 0.0 && number.abs() < 9.007_199_254_740_992e15 {
        return json!(number as i64);
    }
    Number::from_f64(number).map_or(Value::Null, Value::Number)
}

fn js_number_key(number: f64) -> String {
    crate::tui::js_number_string(number)
}

const INTEGER_KEY: &str = "^-?(?:0|[1-9][0-9]*)$";
const NUMBER_KEY: &str = "^-?(?:0|[1-9][0-9]*)(?:\\.[0-9]+)?$";
const STRING_KEY: &str = "^.*$";

/// `isNaN(key)` for a string: whether `Number(key)` is NaN. Number trims whitespace and reads the empty string as 0.
fn is_nan_string(key: &str) -> bool {
    let trimmed = key.trim_matches(|c: char| c.is_whitespace() || c == '\u{feff}');
    if trimmed.is_empty() {
        return false;
    }
    let unsigned = trimmed.strip_prefix(['+', '-']).unwrap_or(trimmed);
    let radix = |prefixes: [&str; 2], radix: u32| prefixes.iter().find_map(|prefix| trimmed.strip_prefix(prefix)).is_some_and(|digits| !digits.is_empty() && digits.chars().all(|c| c.is_digit(radix)));
    if radix(["0x", "0X"], 16) || radix(["0o", "0O"], 8) || radix(["0b", "0B"], 2) || unsigned == "Infinity" {
        return false;
    }
    let (mantissa, exponent) = match unsigned.find(['e', 'E']) {
        Some(at) => (&unsigned[..at], Some(&unsigned[at + 1..])),
        None => (unsigned, None),
    };
    let digits_ok = |text: &str| text.bytes().all(|byte| byte.is_ascii_digit());
    let mantissa_ok = match mantissa.split_once('.') {
        Some((whole, fraction)) => digits_ok(whole) && digits_ok(fraction) && !(whole.is_empty() && fraction.is_empty()),
        None => !mantissa.is_empty() && digits_ok(mantissa),
    };
    let exponent_ok = exponent.is_none_or(|text| {
        let digits = text.strip_prefix(['+', '-']).unwrap_or(text);
        !digits.is_empty() && digits_ok(digits)
    });
    !(mantissa_ok && exponent_ok)
}

fn create_record(pattern: &str, value: TValue) -> TSchema {
    create("Record", vec![("type", "object".into()), ("patternProperties", TValue::Object(vec![(pattern.to_string(), value)]))], None)
}

fn deferred(action: &str, parameters: Vec<TValue>, options: Option<&Map<String, Value>>) -> TSchema {
    let options = TValue::Object(options.map(|map| map.iter().map(|(key, value)| (key.clone(), TValue::Json(value.clone()))).collect()).unwrap_or_default());
    create("Deferred", vec![("type", "deferred".into()), ("action", action.into()), ("parameters", TValue::List(parameters)), ("options", options)], None)
}

fn flatten_union(any_of: Option<TValue>) -> Vec<TValue> {
    let Some(TValue::List(members)) = any_of else { return Vec::new() };
    let mut out = Vec::new();
    for member in members {
        if Type::is_union(member.clone()) {
            out.extend(flatten_union(member.member("anyOf")));
        } else {
            out.push(member);
        }
    }
    out
}

const KEYWORDS_WITH_CHILD: [&str; 11] = ["additionalProperties", "additionalItems", "contains", "propertyNames", "not", "if", "then", "else", "items", "unevaluatedItems", "unevaluatedProperties"];
const KEYWORDS_WITH_MEMBERS: [&str; 6] = ["properties", "patternProperties", "dependentSchemas", "dependencies", "$defs", "definitions"];
const KEYWORDS_WITH_LIST: [&str; 5] = ["allOf", "anyOf", "oneOf", "prefixItems", "items"];

fn pointer_segment(key: &str) -> String {
    key.replace('~', "~0").replace('/', "~1")
}

fn visit_refinements(schema: &TSchema, path: &str, sites: &mut Vec<RefinementSite>) {
    if !schema.refine.is_empty() {
        sites.push(RefinementSite { path: path.to_string(), refinements: schema.refine.clone() });
    }
    for (key, value) in &schema.fields {
        let key = key.as_str();
        match value {
            TValue::Object(members) if KEYWORDS_WITH_MEMBERS.contains(&key) => {
                for (name, member) in members {
                    if let TValue::Schema(child) = member {
                        visit_refinements(child, &format!("{path}/{key}/{}", pointer_segment(name)), sites);
                    }
                }
            }
            TValue::List(members) if KEYWORDS_WITH_LIST.contains(&key) => {
                for (index, member) in members.iter().enumerate() {
                    if let TValue::Schema(child) = member {
                        visit_refinements(child, &format!("{path}/{key}/{index}"), sites);
                    }
                }
            }
            TValue::Schema(child) if KEYWORDS_WITH_CHILD.contains(&key) => visit_refinements(child, &format!("{path}/{key}"), sites),
            _ => {}
        }
    }
}

/// What `Type.Literal` takes: a string, a boolean or a number.
#[derive(Clone, Debug, PartialEq)]
pub enum LiteralValue {
    String(String),
    Bool(bool),
    Number(f64),
}
impl From<&str> for LiteralValue {
    fn from(text: &str) -> Self {
        LiteralValue::String(text.to_string())
    }
}
impl From<String> for LiteralValue {
    fn from(text: String) -> Self {
        LiteralValue::String(text)
    }
}
impl From<bool> for LiteralValue {
    fn from(flag: bool) -> Self {
        LiteralValue::Bool(flag)
    }
}
impl From<f64> for LiteralValue {
    fn from(number: f64) -> Self {
        LiteralValue::Number(number)
    }
}
impl From<i64> for LiteralValue {
    fn from(number: i64) -> Self {
        LiteralValue::Number(number as f64)
    }
}
impl From<i32> for LiteralValue {
    fn from(number: i32) -> Self {
        LiteralValue::Number(f64::from(number))
    }
}

/// pi-ai's `Type`. Every member is an associated function: `Type::object(...)`, `Type::string(None)`, `Type::optional(...)`. Pi's `Type.Ref` is `ref_`, `Type.Enum` is `enum_` and
/// `Type.Unsafe` is `unsafe_`; the guards are `is_string` and so on. The JSON Schema each returns is what Pi's `Type` returns, member order included
/// (test/parity/sdk-vectors/pi-ai/Type.json).
///
/// The Codec family is absent (pig divergence (D198)); a call does not compile:
///
/// ```compile_fail
/// let _ = pig_sdk::Type::codec;
/// ```
pub struct Type;

type Options<'a> = Option<&'a Map<String, Value>>;

impl Type {
    // guards
    pub fn is_schema(value: impl Into<TValue>) -> bool {
        is_schema(&value.into())
    }

    pub fn is_kind(value: impl Into<TValue>, kind: &str) -> bool {
        let value = value.into();
        is_schema(&value) && value.member("~kind").is_some_and(|have| have == TValue::Json(json!(kind)))
    }

    pub fn is_optional(value: impl Into<TValue>) -> bool {
        let value = value.into();
        is_schema(&value) && value.member("~optional").is_some()
    }

    pub fn is_readonly(value: impl Into<TValue>) -> bool {
        let value = value.into();
        is_schema(&value) && value.member("~readonly").is_some()
    }

    pub fn is_immutable(value: impl Into<TValue>) -> bool {
        let value = value.into();
        is_schema(&value) && value.member("~immutable").is_some()
    }

    /// An object (not a list) whose `~unsafe` is null.
    pub fn is_unsafe(value: impl Into<TValue>) -> bool {
        let value = value.into();
        !matches!(&value, TValue::List(_) | TValue::Json(Value::Array(_))) && !matches!(&value, TValue::Schema(schema) if schema.items.is_some()) && value.member("~unsafe") == Some(TValue::Json(Value::Null))
    }

    /// A string or a finite number.
    pub fn is_enum_value(value: &Value) -> bool {
        value.is_string() || value.as_f64().is_some_and(f64::is_finite)
    }

    // constructors
    pub fn any(options: Options) -> TSchema {
        create("Any", vec![], options)
    }
    pub fn unknown(options: Options) -> TSchema {
        create("Unknown", vec![], options)
    }
    pub fn string(options: Options) -> TSchema {
        create("String", vec![("type", "string".into())], options)
    }
    pub fn number(options: Options) -> TSchema {
        create("Number", vec![("type", "number".into())], options)
    }
    pub fn integer(options: Options) -> TSchema {
        create("Integer", vec![("type", "integer".into())], options)
    }
    pub fn boolean(options: Options) -> TSchema {
        create("Boolean", vec![("type", "boolean".into())], options)
    }
    pub fn null(options: Options) -> TSchema {
        create("Null", vec![("type", "null".into())], options)
    }
    pub fn never(options: Options) -> TSchema {
        create("Never", vec![("not", TValue::Object(vec![]))], options)
    }
    pub fn undefined(options: Options) -> TSchema {
        create("Undefined", vec![("type", "undefined".into())], options)
    }
    pub fn void(options: Options) -> TSchema {
        create("Void", vec![("type", "void".into())], options)
    }
    pub fn symbol(options: Options) -> TSchema {
        create("Symbol", vec![("type", "symbol".into())], options)
    }
    pub fn big_int(options: Options) -> TSchema {
        create("BigInt", vec![("type", "bigint".into())], options)
    }
    pub fn ref_(reference: &str, options: Options) -> TSchema {
        create("Ref", vec![("$ref", reference.into())], options)
    }
    pub fn array(items: impl Into<TValue>, options: Options) -> TSchema {
        create("Array", vec![("type", "array".into()), ("items", items.into())], options)
    }
    pub fn union<T: Into<TValue>>(any_of: impl IntoIterator<Item = T>, options: Options) -> TSchema {
        create("Union", vec![("anyOf", TValue::List(any_of.into_iter().map(Into::into).collect()))], options)
    }
    pub fn intersect<T: Into<TValue>>(types: impl IntoIterator<Item = T>, options: Options) -> TSchema {
        create("Intersect", vec![("allOf", TValue::List(types.into_iter().map(Into::into).collect()))], options)
    }

    /// `Type.Object(properties, options)`: `required` lists, in the key order, the properties that are not `~optional` and is left out when there are none. Keys that are array
    /// indexes come first and ascending, as in a JavaScript object.
    pub fn object<K: Into<String>, V: Into<TValue>>(properties: impl IntoIterator<Item = (K, V)>, options: Options) -> TSchema {
        let mut pairs: Vec<(String, TValue)> = Vec::new();
        for (key, value) in properties {
            set(&mut pairs, &key.into(), value.into());
        }
        let properties = js_order(pairs);
        let required: Vec<TValue> = properties.iter().filter(|(_, value)| !Type::is_optional(value.clone())).map(|(key, _)| TValue::Json(json!(key))).collect();
        let mut enumerable: Vec<(&str, TValue)> = vec![("type", "object".into())];
        if !required.is_empty() {
            enumerable.push(("required", TValue::List(required)));
        }
        enumerable.push(("properties", TValue::Object(properties)));
        create("Object", enumerable, options)
    }

    /// `Type.Optional(type)`: a copy of the schema with the hidden `~optional`, which `Type::object` reads.
    pub fn optional(schema: impl Into<TSchema>) -> TSchema {
        mark(schema.into(), "~optional")
    }
    /// `Type.Readonly(type)`: a copy of the schema with the hidden `~readonly`.
    pub fn readonly(schema: impl Into<TSchema>) -> TSchema {
        mark(schema.into(), "~readonly")
    }
    /// `Type.Immutable(type)`: a copy of the schema with the hidden `~immutable`.
    pub fn immutable(schema: impl Into<TSchema>) -> TSchema {
        mark(schema.into(), "~immutable")
    }
    /// `Type.Optional(value)` of a value that is not a [`TSchema`]: a plain JSON object, or a list (whose copy loses the marker, see [`Type::unsafe_`]). Any other value is an error.
    /// `Type.Refine(type, check, error?)`: a copy of the schema whose hidden `~refine` list has the refinement appended; the schema's JSON does not change. The refinements run when
    /// the host validates a tool call's arguments (see [`crate::Extension::tool`]).
    pub fn refine(
        schema: impl Into<TSchema>,
        check: impl Fn(&Value) -> Result<bool, String> + Send + Sync + 'static,
        error: Option<Box<dyn Fn(&Value) -> Result<String, String> + Send + Sync>>,
    ) -> TSchema {
        let mut schema: TSchema = schema.into();
        schema.refine.push(Refinement { check: Arc::new(check), error: error.map(|error| Arc::from(error) as Arc<dyn Fn(&Value) -> Result<String, String> + Send + Sync>) });
        schema
    }

    /// `Type.Refine(value, check, error?)` of a value that is not a [`TSchema`]: a plain JSON object or a list. Any other value is an error.
    pub fn refine_value(
        value: impl Into<TValue>,
        check: impl Fn(&Value) -> Result<bool, String> + Send + Sync + 'static,
        error: Option<Box<dyn Fn(&Value) -> Result<String, String> + Send + Sync>>,
    ) -> Result<TSchema, String> {
        let mut schema = match value.into() {
            TValue::Schema(schema) => schema,
            other => update(&other, &[], None)?,
        };
        schema.refine.push(Refinement { check: Arc::new(check), error: error.map(|error| Arc::from(error) as Arc<dyn Fn(&Value) -> Result<String, String> + Send + Sync>) });
        Ok(schema)
    }

    /// `Type.IsRefine`: the value carries a `~refine` list.
    pub fn is_refine(value: &TSchema) -> bool {
        !value.refine.is_empty()
    }

    pub fn optional_value(value: impl Into<TValue>) -> Result<TSchema, String> {
        mark_value(value.into(), "~optional")
    }
    /// `Type.Readonly(value)` of a value that is not a [`TSchema`]; see [`Type::optional_value`].
    pub fn readonly_value(value: impl Into<TValue>) -> Result<TSchema, String> {
        mark_value(value.into(), "~readonly")
    }
    /// `Type.Immutable(value)` of a value that is not a [`TSchema`]; see [`Type::optional_value`].
    pub fn immutable_value(value: impl Into<TValue>) -> Result<TSchema, String> {
        mark_value(value.into(), "~immutable")
    }

    /// `Type.Unsafe(schema)`: a copy of the object (or list) schema with `~unsafe` (null). Any other value is an error.
    pub fn unsafe_(schema: impl Into<TValue>) -> Result<TSchema, String> {
        update(&schema.into(), &[("~unsafe", Value::Null)], None)
    }

    /// `Type.With(type, options)`: a copy with the options assigned (a Ref is not evaluated: the result is TypeBox's deferred With).
    pub fn with(schema: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        let schema = schema.into();
        if Type::is_ref(schema.clone()) {
            let options_value = options.map_or(TValue::Json(Value::Null), |map| TValue::Json(Value::Object(map.clone())));
            return Ok(deferred("With", vec![schema, options_value], None));
        }
        update(&schema, &[], options)
    }

    /// `Type.Literal(value, options)`: `{"type": <string|number|boolean>, "const": value}`. A number that is NaN or infinite is Pi's "Invalid Literal value".
    pub fn literal(value: impl Into<LiteralValue>, options: Options) -> Result<TSchema, String> {
        let (name, constant) = match value.into() {
            LiteralValue::String(text) => ("string", json!(text)),
            LiteralValue::Bool(flag) => ("boolean", json!(flag)),
            LiteralValue::Number(number) if number.is_finite() => ("number", js_number(number)),
            LiteralValue::Number(_) => return Err("Invalid Literal value".to_string()),
        };
        Ok(create("Literal", vec![("type", name.into()), ("const", TValue::Json(constant))], options))
    }

    /// `Type.Enum(value, options)`: `{"enum": values}`. `value` is a JSON array of values, or an object in the manner of a TypeScript enum, whose values for the keys that are not
    /// numbers are the enum's.
    pub fn enum_(value: &Value, options: Options) -> Result<TSchema, String> {
        let values: Vec<TValue> = match value {
            Value::Array(items) => items.iter().cloned().map(TValue::Json).collect(),
            Value::Object(map) => {
                let pairs: Vec<(String, TValue)> = map.iter().map(|(key, value)| (key.clone(), TValue::Json(value.clone()))).collect();
                js_order(pairs).into_iter().filter(|(key, _)| is_nan_string(key)).map(|(_, value)| value).collect()
            }
            _ => return Err("Type.enum_ takes an array or an object".to_string()),
        };
        Ok(create("Enum", vec![("enum", TValue::List(values))], options))
    }

    /// `Type.Record(key, value, options)`: for a string, number or integer key `{"type":"object","patternProperties":{<pattern>: value}}`; for a literal, boolean or
    /// union-of-literals key the object with those properties. A key Pi evaluates through a part of TypeBox this SDK has not ported (an enum, an intersection or a template
    /// literal) is an error until it is.
    pub fn record(key: impl Into<TValue>, value: impl Into<TValue>, options: Options) -> Result<TSchema, String> {
        let (key, value) = (key.into(), value.into());
        if Type::is_ref(key.clone()) {
            return Ok(deferred("Record", vec![key, value], options));
        }
        update(&TValue::Schema(record_from_key(&key, value)?), &[], options)
    }
}

macro_rules! guards {
    ($($name:ident => $kind:literal),* $(,)?) => {
        impl Type {
            $(pub fn $name(value: impl Into<TValue>) -> bool { Type::is_kind(value, $kind) })*
        }
    };
}
guards!(
    is_any => "Any", is_array => "Array", is_big_int => "BigInt", is_boolean => "Boolean", is_enum => "Enum", is_integer => "Integer", is_intersect => "Intersect",
    is_literal => "Literal", is_never => "Never", is_null => "Null", is_number => "Number", is_object => "Object", is_record => "Record", is_ref => "Ref",
    is_string => "String", is_symbol => "Symbol", is_undefined => "Undefined", is_union => "Union", is_unknown => "Unknown", is_void => "Void",
);

fn literal_number(constant: &TValue) -> Option<f64> {
    match constant {
        TValue::Json(Value::Number(number)) => number.as_f64().filter(|number| number.is_finite()),
        _ => None,
    }
}

fn record_from_key(key: &TValue, value: TValue) -> Result<TSchema, String> {
    if Type::is_any(key.clone()) {
        return Ok(create_record(STRING_KEY, value));
    }
    if Type::is_boolean(key.clone()) {
        return Ok(Type::object([("true", value.clone()), ("false", value)], None));
    }
    if Type::is_enum(key.clone()) || Type::is_intersect(key.clone()) || Type::is_kind(key.clone(), "TemplateLiteral") {
        return Err("Type.record: a key of this kind is evaluated by a part of TypeBox this SDK has not ported yet".to_string());
    }
    if Type::is_integer(key.clone()) {
        return Ok(create_record(INTEGER_KEY, value));
    }
    if Type::is_literal(key.clone()) {
        let constant = key.member("const");
        return Ok(match &constant {
            Some(TValue::Json(Value::String(text))) => Type::object([(text.clone(), value)], None),
            Some(TValue::Json(Value::Bool(flag))) => Type::object([(flag.to_string(), value)], None),
            Some(other) => match literal_number(other) {
                Some(number) => Type::object([(js_number_key(number), value)], None),
                None => Type::object(Vec::<(String, TValue)>::new(), None),
            },
            None => Type::object(Vec::<(String, TValue)>::new(), None),
        });
    }
    if Type::is_number(key.clone()) {
        return Ok(create_record(NUMBER_KEY, value));
    }
    if Type::is_union(key.clone()) {
        let members = flatten_union(key.member("anyOf"));
        if members.iter().any(|member| Type::is_string(member.clone()) || Type::is_number(member.clone()) || Type::is_integer(member.clone())) {
            return Ok(create_record(STRING_KEY, value));
        }
        let mut properties: Vec<(String, TValue)> = Vec::new();
        for member in &members {
            if !Type::is_literal(member.clone()) {
                continue;
            }
            match member.member("const") {
                Some(TValue::Json(Value::String(text))) => set(&mut properties, &text, value.clone()),
                Some(other) => {
                    if let Some(number) = literal_number(&other) {
                        set(&mut properties, &js_number_key(number), value.clone());
                    }
                }
                None => {}
            }
        }
        return Ok(Type::object(properties, None));
    }
    if Type::is_string(key.clone()) {
        if let Some(TValue::Json(Value::String(pattern))) = key.member("pattern") {
            return Ok(create_record(&pattern, value));
        }
        return Ok(create_record(STRING_KEY, value));
    }
    Ok(Type::object(Vec::<(String, TValue)>::new(), None))
}
