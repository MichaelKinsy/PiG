import {
  ArrayOptions,
  Clone,
  Evaluate,
  Flatten,
  Get,
  Instantiate,
  Intersect,
  IsArray,
  IsBigInt,
  IsBoolean,
  IsCodec,
  IsConstructor,
  IsCyclic,
  IsEnum,
  IsFunction,
  IsInteger,
  IsIntersect,
  IsLiteral,
  IsLiteralBigInt,
  IsLiteralBoolean,
  IsLiteralNumber,
  IsLiteralString,
  IsNever,
  IsNull,
  IsNumber,
  IsObject,
  IsOptional,
  IsRecord,
  IsRef,
  IsRefine,
  IsSchema,
  IsString,
  IsSymbol,
  IsTemplateLiteral,
  IsTuple,
  IsUndefined,
  IsUnion,
  IsVoid,
  Literal,
  Priority,
  Record,
  RecordKey,
  RecordPattern,
  RecordValue,
  Ref,
  String as String2,
  TemplateLiteralDecode,
  Tuple,
  Union,
  Unknown,
  Unreachable,
  With,
  _Array_,
  _Object_,
  __export,
  arguments_exports,
  emit_exports,
  environment_exports,
  globals_exports,
  guard_default,
  guard_exports,
  hash_exports,
  settings_exports
} from "./typebox-shared-52AGGYDU.mjs";

// node_modules/typebox/build/schema/pointer/pointer.mjs
var pointer_exports = {};
__export(pointer_exports, {
  Delete: () => Delete,
  Get: () => Get2,
  Has: () => Has,
  Indices: () => Indices,
  Set: () => Set2
});
function AssertNotRoot(indices) {
  if (indices.length === 0)
    throw Error("Cannot set root");
}
function AssertCanSet(value) {
  if (!guard_exports.IsObject(value))
    throw Error("Cannot set value");
}
function AssertIndex(index3) {
  if (guard_exports.IsUnsafePropertyKey(index3))
    throw Error("Pointer contains unsafe property key");
}
function AssertIndices(indices) {
  for (const index3 of indices)
    AssertIndex(index3);
}
function IsNumericIndex(index3) {
  return /^(0|[1-9]\d*)$/.test(index3);
}
function TakeIndexRight(indices) {
  return [
    indices.slice(0, indices.length - 1),
    indices.slice(indices.length - 1)[0]
  ];
}
function HasIndex(index3, value) {
  return guard_exports.IsObject(value) && guard_exports.HasPropertyKey(value, index3);
}
function GetIndex(index3, value) {
  return guard_exports.IsObject(value) && !guard_exports.IsUnsafePropertyKey(index3) ? value[index3] : void 0;
}
function GetIndices(indices, value) {
  return indices.reduce((value2, index3) => GetIndex(index3, value2), value);
}
function Indices(pointer) {
  if (guard_exports.IsEqual(pointer.length, 0))
    return [];
  const indices = pointer.split("/").map((index3) => index3.replace(/~1/g, "/").replace(/~0/g, "~"));
  return indices.length > 0 && indices[0] === "" ? indices.slice(1) : indices;
}
function Has(value, pointer) {
  let current = value;
  return Indices(pointer).every((index3) => {
    if (!HasIndex(index3, current))
      return false;
    current = current[index3];
    return true;
  });
}
function Get2(value, pointer) {
  const indices = Indices(pointer);
  return GetIndices(indices, value);
}
function Set2(value, pointer, next) {
  const indices = Indices(pointer);
  AssertNotRoot(indices);
  AssertIndices(indices);
  const [head, index3] = TakeIndexRight(indices);
  const parent = GetIndices(head, value);
  AssertCanSet(parent);
  parent[index3] = next;
  return value;
}
function Delete(value, pointer) {
  const indices = Indices(pointer);
  AssertNotRoot(indices);
  AssertIndices(indices);
  const [head, index3] = TakeIndexRight(indices);
  const parent = GetIndices(head, value);
  AssertCanSet(parent);
  if (guard_exports.IsArray(parent) && IsNumericIndex(index3)) {
    parent.splice(+index3, 1);
  } else {
    delete parent[index3];
  }
  return value;
}

// node_modules/typebox/build/schema/types/_refine.mjs
function IsRefine2(value) {
  return guard_exports.HasPropertyKey(value, "~refine") && guard_exports.IsArray(value["~refine"]) && guard_exports.Every(value["~refine"], 0, (value2) => guard_exports.IsObject(value2) && guard_exports.HasPropertyKey(value2, "check") && guard_exports.HasPropertyKey(value2, "error") && guard_exports.IsFunction(value2.check) && guard_exports.IsFunction(value2.error));
}

// node_modules/typebox/build/schema/types/schema.mjs
function IsSchemaObject(value) {
  return guard_exports.IsObject(value) && !guard_exports.IsArray(value);
}
function IsSchemaBoolean(value) {
  return guard_exports.IsBoolean(value);
}
function IsSchema2(value) {
  return IsSchemaObject(value) || IsSchemaBoolean(value);
}

// node_modules/typebox/build/schema/types/additionalItems.mjs
function IsAdditionalItems(schema) {
  return guard_exports.HasPropertyKey(schema, "additionalItems") && IsSchema2(schema.additionalItems);
}

// node_modules/typebox/build/schema/types/additionalProperties.mjs
function IsAdditionalProperties(schema) {
  return guard_exports.HasPropertyKey(schema, "additionalProperties") && IsSchema2(schema.additionalProperties);
}

// node_modules/typebox/build/schema/types/allOf.mjs
function IsAllOf(schema) {
  return guard_exports.HasPropertyKey(schema, "allOf") && guard_exports.IsArray(schema.allOf) && schema.allOf.every((value) => IsSchema2(value));
}

// node_modules/typebox/build/schema/types/anchor.mjs
function IsAnchor(schema) {
  return guard_exports.HasPropertyKey(schema, "$anchor") && guard_exports.IsString(schema.$anchor);
}

// node_modules/typebox/build/schema/types/anyOf.mjs
function IsAnyOf(schema) {
  return guard_exports.HasPropertyKey(schema, "anyOf") && guard_exports.IsArray(schema.anyOf) && schema.anyOf.every((value) => IsSchema2(value));
}

// node_modules/typebox/build/schema/types/const.mjs
function IsConst(value) {
  return guard_exports.HasPropertyKey(value, "const");
}

// node_modules/typebox/build/schema/types/contains.mjs
function IsContains(schema) {
  return guard_exports.HasPropertyKey(schema, "contains") && IsSchema2(schema.contains);
}

// node_modules/typebox/build/schema/types/default.mjs
function IsDefault(schema) {
  return guard_exports.HasPropertyKey(schema, "default");
}

// node_modules/typebox/build/schema/types/dependencies.mjs
function IsDependencies(schema) {
  return guard_exports.HasPropertyKey(schema, "dependencies") && guard_exports.IsObject(schema.dependencies) && Object.values(schema.dependencies).every((value) => IsSchema2(value) || guard_exports.IsArray(value) && value.every((value2) => guard_exports.IsString(value2)));
}

// node_modules/typebox/build/schema/types/dependentRequired.mjs
function IsDependentRequired(schema) {
  return guard_exports.HasPropertyKey(schema, "dependentRequired") && guard_exports.IsObject(schema.dependentRequired) && Object.values(schema.dependentRequired).every((value) => guard_exports.IsArray(value) && value.every((value2) => guard_exports.IsString(value2)));
}

// node_modules/typebox/build/schema/types/dependentSchemas.mjs
function IsDependentSchemas(schema) {
  return guard_exports.HasPropertyKey(schema, "dependentSchemas") && guard_exports.IsObject(schema.dependentSchemas) && Object.values(schema.dependentSchemas).every((value) => IsSchema2(value));
}

// node_modules/typebox/build/schema/types/dynamicAnchor.mjs
function IsDynamicAnchor(schema) {
  return guard_exports.HasPropertyKey(schema, "$dynamicAnchor") && guard_exports.IsString(schema.$dynamicAnchor);
}

// node_modules/typebox/build/schema/types/dynamicRef.mjs
function IsDynamicRef(schema) {
  return guard_exports.HasPropertyKey(schema, "$dynamicRef") && guard_exports.IsString(schema.$dynamicRef);
}

// node_modules/typebox/build/schema/types/else.mjs
function IsElse(schema) {
  return guard_exports.HasPropertyKey(schema, "else") && IsSchema2(schema.else);
}

// node_modules/typebox/build/schema/types/enum.mjs
function IsEnum2(schema) {
  return guard_exports.HasPropertyKey(schema, "enum") && guard_exports.IsArray(schema.enum);
}

// node_modules/typebox/build/schema/types/exclusiveMaximum.mjs
function IsExclusiveMaximum(schema) {
  return guard_exports.HasPropertyKey(schema, "exclusiveMaximum") && (guard_exports.IsNumber(schema.exclusiveMaximum) || guard_exports.IsBigInt(schema.exclusiveMaximum));
}

// node_modules/typebox/build/schema/types/exclusiveMinimum.mjs
function IsExclusiveMinimum(schema) {
  return guard_exports.HasPropertyKey(schema, "exclusiveMinimum") && (guard_exports.IsNumber(schema.exclusiveMinimum) || guard_exports.IsBigInt(schema.exclusiveMinimum));
}

// node_modules/typebox/build/schema/types/format.mjs
function IsFormat(schema) {
  return guard_exports.HasPropertyKey(schema, "format") && guard_exports.IsString(schema.format);
}

// node_modules/typebox/build/schema/types/id.mjs
function IsId(schema) {
  return guard_exports.HasPropertyKey(schema, "$id") && guard_exports.IsString(schema.$id);
}

// node_modules/typebox/build/schema/types/if.mjs
function IsIf(schema) {
  return guard_exports.HasPropertyKey(schema, "if") && IsSchema2(schema.if);
}

// node_modules/typebox/build/schema/types/items.mjs
function IsItems(schema) {
  return guard_exports.HasPropertyKey(schema, "items") && (IsSchema2(schema.items) || guard_exports.IsArray(schema.items) && schema.items.every((value) => {
    return IsSchema2(value);
  }));
}
function IsItemsSized(schema) {
  return IsItems(schema) && guard_exports.IsArray(schema.items);
}

// node_modules/typebox/build/schema/types/maximum.mjs
function IsMaximum(schema) {
  return guard_exports.HasPropertyKey(schema, "maximum") && (guard_exports.IsNumber(schema.maximum) || guard_exports.IsBigInt(schema.maximum));
}

// node_modules/typebox/build/schema/types/maxContains.mjs
function IsMaxContains(schema) {
  return guard_exports.HasPropertyKey(schema, "maxContains") && guard_exports.IsNumber(schema.maxContains);
}

// node_modules/typebox/build/schema/types/maxItems.mjs
function IsMaxItems(schema) {
  return guard_exports.HasPropertyKey(schema, "maxItems") && guard_exports.IsNumber(schema.maxItems);
}

// node_modules/typebox/build/schema/types/maxLength.mjs
function IsMaxLength(schema) {
  return guard_exports.HasPropertyKey(schema, "maxLength") && guard_exports.IsNumber(schema.maxLength);
}

// node_modules/typebox/build/schema/types/maxProperties.mjs
function IsMaxProperties(schema) {
  return guard_exports.HasPropertyKey(schema, "maxProperties") && guard_exports.IsNumber(schema.maxProperties);
}

// node_modules/typebox/build/schema/types/minimum.mjs
function IsMinimum(schema) {
  return guard_exports.HasPropertyKey(schema, "minimum") && (guard_exports.IsNumber(schema.minimum) || guard_exports.IsBigInt(schema.minimum));
}

// node_modules/typebox/build/schema/types/minContains.mjs
function IsMinContains(schema) {
  return guard_exports.HasPropertyKey(schema, "minContains") && guard_exports.IsNumber(schema.minContains);
}

// node_modules/typebox/build/schema/types/minItems.mjs
function IsMinItems(schema) {
  return guard_exports.HasPropertyKey(schema, "minItems") && guard_exports.IsNumber(schema.minItems);
}

// node_modules/typebox/build/schema/types/minLength.mjs
function IsMinLength(schema) {
  return guard_exports.HasPropertyKey(schema, "minLength") && guard_exports.IsNumber(schema.minLength);
}

// node_modules/typebox/build/schema/types/minProperties.mjs
function IsMinProperties(schema) {
  return guard_exports.HasPropertyKey(schema, "minProperties") && guard_exports.IsNumber(schema.minProperties);
}

// node_modules/typebox/build/schema/types/multipleOf.mjs
function IsMultipleOf(schema) {
  return guard_exports.HasPropertyKey(schema, "multipleOf") && (guard_exports.IsNumber(schema.multipleOf) || guard_exports.IsBigInt(schema.multipleOf));
}

// node_modules/typebox/build/schema/types/not.mjs
function IsNot(schema) {
  return guard_exports.HasPropertyKey(schema, "not") && IsSchema2(schema.not);
}

// node_modules/typebox/build/schema/types/oneOf.mjs
function IsOneOf(schema) {
  return guard_exports.HasPropertyKey(schema, "oneOf") && guard_exports.IsArray(schema.oneOf) && schema.oneOf.every((value) => IsSchema2(value));
}

// node_modules/typebox/build/schema/types/pattern.mjs
function IsPattern(schema) {
  return guard_exports.HasPropertyKey(schema, "pattern") && (guard_exports.IsString(schema.pattern) || schema.pattern instanceof RegExp);
}

// node_modules/typebox/build/schema/types/patternProperties.mjs
function IsPatternProperties(schema) {
  return guard_exports.HasPropertyKey(schema, "patternProperties") && guard_exports.IsObject(schema.patternProperties) && Object.values(schema.patternProperties).every((value) => IsSchema2(value));
}

// node_modules/typebox/build/schema/types/prefixItems.mjs
function IsPrefixItems(schema) {
  return guard_exports.HasPropertyKey(schema, "prefixItems") && guard_exports.IsArray(schema.prefixItems) && schema.prefixItems.every((schema2) => IsSchema2(schema2));
}

// node_modules/typebox/build/schema/types/properties.mjs
function IsProperties(schema) {
  return guard_exports.HasPropertyKey(schema, "properties") && guard_exports.IsObject(schema.properties) && Object.values(schema.properties).every((value) => IsSchema2(value));
}

// node_modules/typebox/build/schema/types/propertyNames.mjs
function IsPropertyNames(schema) {
  return guard_exports.HasPropertyKey(schema, "propertyNames") && (guard_exports.IsObject(schema.propertyNames) || IsSchema2(schema.propertyNames));
}

// node_modules/typebox/build/schema/types/recursiveAnchor.mjs
function IsRecursiveAnchor(schema) {
  return guard_exports.HasPropertyKey(schema, "$recursiveAnchor") && guard_exports.IsBoolean(schema.$recursiveAnchor);
}
function IsRecursiveAnchorTrue(schema) {
  return IsRecursiveAnchor(schema) && guard_exports.IsEqual(schema.$recursiveAnchor, true);
}

// node_modules/typebox/build/schema/types/recursiveRef.mjs
function IsRecursiveRef(schema) {
  return guard_exports.HasPropertyKey(schema, "$recursiveRef") && guard_exports.IsString(schema.$recursiveRef);
}

// node_modules/typebox/build/schema/types/ref.mjs
function IsRef2(schema) {
  return guard_exports.HasPropertyKey(schema, "$ref") && guard_exports.IsString(schema.$ref);
}

// node_modules/typebox/build/schema/types/required.mjs
function IsRequired(schema) {
  return guard_exports.HasPropertyKey(schema, "required") && guard_exports.IsArray(schema.required) && schema.required.every((value) => guard_exports.IsString(value));
}

// node_modules/typebox/build/schema/types/then.mjs
function IsThen(schema) {
  return guard_exports.HasPropertyKey(schema, "then") && IsSchema2(schema.then);
}

// node_modules/typebox/build/schema/types/type.mjs
function IsType(schema) {
  return guard_exports.HasPropertyKey(schema, "type") && (guard_exports.IsString(schema.type) || guard_exports.IsArray(schema.type) && schema.type.every((value) => guard_exports.IsString(value)));
}

// node_modules/typebox/build/schema/types/uniqueItems.mjs
function IsUniqueItems(schema) {
  return guard_exports.HasPropertyKey(schema, "uniqueItems") && guard_exports.IsBoolean(schema.uniqueItems);
}

// node_modules/typebox/build/schema/types/unevaluatedItems.mjs
function IsUnevaluatedItems(schema) {
  return guard_exports.HasPropertyKey(schema, "unevaluatedItems") && IsSchema2(schema.unevaluatedItems);
}

// node_modules/typebox/build/schema/types/unevaluatedProperties.mjs
function IsUnevaluatedProperties(schema) {
  return guard_exports.HasPropertyKey(schema, "unevaluatedProperties") && IsSchema2(schema.unevaluatedProperties);
}

// node_modules/typebox/build/schema/engine/_context.mjs
function HasUnevaluatedFromObject(value) {
  return IsUnevaluatedItems(value) || IsUnevaluatedProperties(value) || guard_exports.Some(guard_exports.Keys(value), (key) => HasUnevaluatedFromUnknown(value[key]));
}
function HasUnevaluatedFromArray(value) {
  return guard_exports.Some(value, (value2) => HasUnevaluatedFromUnknown(value2));
}
function HasUnevaluatedFromUnknown(value) {
  return guard_exports.IsArray(value) ? HasUnevaluatedFromArray(value) : guard_exports.IsObject(value) ? HasUnevaluatedFromObject(value) : false;
}
function HasUnevaluated(context, schema) {
  return HasUnevaluatedFromUnknown(schema) || guard_exports.Some(guard_exports.Keys(context), (key) => HasUnevaluatedFromUnknown(context[key]));
}
var BuildContext = class {
  constructor(hasUnevaluated) {
    this.hasUnevaluated = hasUnevaluated;
  }
  UseUnevaluated() {
    return this.hasUnevaluated;
  }
  // ----------------------------------------------------------------
  // Stack
  // ----------------------------------------------------------------
  Push() {
    return emit_exports.Call(emit_exports.Member("context", "Push"), []);
  }
  Pop() {
    return emit_exports.Call(emit_exports.Member("context", "Pop"), []);
  }
  // ----------------------------------------------------------------
  // Top
  // ----------------------------------------------------------------
  AddIndex(index3) {
    return emit_exports.Call(emit_exports.Member("context", "AddIndex"), [index3]);
  }
  AddKey(key) {
    return emit_exports.Call(emit_exports.Member("context", "AddKey"), [key]);
  }
  Merge(results) {
    return emit_exports.Call(emit_exports.Member("context", "Merge"), [results]);
  }
};
var CheckContext = class {
  constructor() {
    const indices = /* @__PURE__ */ new Set();
    const keys = /* @__PURE__ */ new Set();
    this.stack = [{ indices, keys }];
  }
  // ----------------------------------------------------------------
  // Stack
  // ----------------------------------------------------------------
  Push() {
    const indices = /* @__PURE__ */ new Set();
    const keys = /* @__PURE__ */ new Set();
    this.stack.push({ indices, keys });
    return true;
  }
  Pop() {
    this.stack.pop();
    return true;
  }
  // ----------------------------------------------------------------
  // Top
  // ----------------------------------------------------------------
  AddIndex(index3) {
    this.GetIndices().add(index3);
    return true;
  }
  AddKey(key) {
    this.GetKeys().add(key);
    return true;
  }
  GetIndices() {
    const top = this.stack[this.stack.length - 1];
    return top.indices;
  }
  GetKeys() {
    const top = this.stack[this.stack.length - 1];
    return top.keys;
  }
  Merge(results) {
    for (const context of results) {
      context.GetIndices().forEach((value) => this.GetIndices().add(value));
      context.GetKeys().forEach((value) => this.GetKeys().add(value));
    }
    return true;
  }
};
var ErrorContext = class extends CheckContext {
  constructor() {
    super();
    this.errors = [];
  }
  AtCapacity() {
    return this.errors.length >= settings_exports.Get().maxErrors;
  }
  AddError(error) {
    if (!this.AtCapacity())
      this.errors.push(error);
    return false;
  }
  GetErrors() {
    return this.errors;
  }
};

// node_modules/typebox/build/schema/engine/_externals.mjs
var state = {
  identifier: "External",
  variables: []
};
function CreateVariable(value) {
  const call = `External[${state.variables.length}]`;
  state.variables.push(value);
  return call;
}
function ResetExternal() {
  state.variables = [];
}
function GetExternal() {
  return { ...state };
}

// node_modules/typebox/build/schema/engine/_refine.mjs
function BuildRefine(_stack, _context, schema, value) {
  const refinements = CreateVariable(schema["~refine"].map((refinement) => refinement));
  return emit_exports.Every(refinements, emit_exports.Constant(0), ["refinement", "_"], emit_exports.Call(emit_exports.Member("refinement", "check"), [value]));
}
function CheckRefine(_stack, _context, schema, value) {
  return guard_exports.Every(schema["~refine"], 0, (refinement, _) => refinement.check(value));
}
function ErrorRefine(_stack, context, schemaPath, instancePath, schema, value) {
  return guard_exports.EveryAll(schema["~refine"], 0, (refinement, index3) => {
    return refinement.check(value) || context.AddError({
      keyword: "~refine",
      schemaPath,
      instancePath,
      params: { index: index3, message: refinement.error(value) }
    });
  });
}

// node_modules/typebox/build/schema/engine/_unique.mjs
var index = 0;
function Unique() {
  return `var_${index++}`;
}

// node_modules/typebox/build/schema/engine/additionalItems.mjs
function IsValid(schema) {
  return IsItems(schema) && guard_exports.IsArray(schema.items);
}
function BuildAdditionalItemsStandard(stack, context, schema, value) {
  const [item, index3] = [Unique(), Unique()];
  const isSchema = BuildSchemaPushStack(stack, context, schema.additionalItems, item);
  const isLength = emit_exports.IsLessThan(index3, emit_exports.Constant(schema.items.length));
  const addIndex = context.AddIndex(index3);
  return emit_exports.Every(value, emit_exports.Constant(0), [item, index3], emit_exports.Or(isLength, emit_exports.And(isSchema, addIndex)));
}
function BuildAdditionalItemsFast(stack, context, schema, value) {
  const [item, index3] = [Unique(), Unique()];
  const isSchema = BuildSchemaPushStack(stack, context, schema.additionalItems, item);
  const isLength = emit_exports.IsLessThan(index3, emit_exports.Constant(schema.items.length));
  return emit_exports.Every(value, emit_exports.Constant(0), [item, index3], emit_exports.Or(isLength, isSchema));
}
function BuildAdditionalItems(stack, context, schema, value) {
  if (!IsValid(schema))
    return emit_exports.Constant(true);
  return context.UseUnevaluated() ? BuildAdditionalItemsStandard(stack, context, schema, value) : BuildAdditionalItemsFast(stack, context, schema, value);
}
function CheckAdditionalItems(stack, context, schema, value) {
  if (!IsValid(schema))
    return true;
  const isAdditionalItems = guard_exports.Every(value, 0, (item, index3) => {
    return guard_exports.IsLessThan(index3, schema.items.length) || CheckSchemaPushStack(stack, context, schema.additionalItems, item) && context.AddIndex(index3);
  });
  return isAdditionalItems;
}
function ErrorAdditionalItems(stack, context, schemaPath, instancePath, schema, value) {
  if (!IsValid(schema))
    return true;
  const isAdditionalItems = guard_exports.Every(value, 0, (item, index3) => {
    const nextSchemaPath = `${schemaPath}/additionalItems`;
    const nextInstancePath = `${instancePath}/${index3}`;
    return guard_exports.IsLessThan(index3, schema.items.length) || ErrorSchemaPushStack(stack, context, nextSchemaPath, nextInstancePath, schema.additionalItems, item) && context.AddIndex(index3);
  });
  return isAdditionalItems;
}

// node_modules/typebox/build/schema/engine/_regexp.mjs
function UnicodeRegExp(pattern) {
  return new RegExp(pattern, "u");
}

// node_modules/typebox/build/schema/engine/additionalProperties.mjs
function IsAdditionalPropertiesIgnored(context, additionalProperties) {
  return !context.UseUnevaluated() && (guard_exports.IsEqual(additionalProperties, true) || guard_exports.IsObject(additionalProperties) && guard_exports.IsEqual(guard_exports.Keys(additionalProperties).length, 0));
}
function GetPropertyKeyAsPattern(key) {
  const escaped = key.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return `^${escaped}$`;
}
function GetPropertiesPattern(schema) {
  const patterns = [];
  if (IsPatternProperties(schema))
    patterns.push(...guard_exports.Keys(schema.patternProperties));
  if (IsProperties(schema))
    patterns.push(...guard_exports.Keys(schema.properties).map(GetPropertyKeyAsPattern));
  return guard_exports.IsEqual(patterns.length, 0) ? "(?!)" : `(${patterns.join("|")})`;
}
function CanAdditionalPropertiesFast(_context, schema, _value) {
  return IsRequired(schema) && IsProperties(schema) && !IsPatternProperties(schema) && guard_exports.IsEqual(schema.additionalProperties, false) && guard_exports.IsEqual(guard_exports.Keys(schema.properties).length, schema.required.length);
}
function BuildAdditionalPropertiesFast(_context, schema, value) {
  return emit_exports.IsEqual(emit_exports.Member(emit_exports.Call(emit_exports.Member("Object", "getOwnPropertyNames"), [value]), "length"), emit_exports.Constant(schema.required.length));
}
function BuildAdditionalPropertiesStandard(stack, context, schema, value) {
  const [key, _index] = [Unique(), Unique()];
  const regexp = CreateVariable(UnicodeRegExp(GetPropertiesPattern(schema)));
  const isSchema = BuildSchemaPushStack(stack, context, schema.additionalProperties, `${value}[${key}]`);
  const isKey = emit_exports.Call(emit_exports.Member(regexp, "test"), [key]);
  const addKey = context.AddKey(key);
  const guarded = context.UseUnevaluated() ? emit_exports.Or(isKey, emit_exports.And(isSchema, addKey)) : emit_exports.Or(isKey, isSchema);
  const result = emit_exports.Every(emit_exports.Keys(value), emit_exports.Constant(0), [key, _index], guarded);
  return result;
}
function BuildAdditionalProperties(stack, context, schema, value) {
  if (IsAdditionalPropertiesIgnored(context, schema.additionalProperties))
    return emit_exports.Constant(true);
  return CanAdditionalPropertiesFast(context, schema, value) ? BuildAdditionalPropertiesFast(context, schema, value) : BuildAdditionalPropertiesStandard(stack, context, schema, value);
}
function CheckAdditionalProperties(stack, context, schema, value) {
  const regexp = UnicodeRegExp(GetPropertiesPattern(schema));
  const isAdditionalProperties = guard_exports.Every(guard_exports.Keys(value), 0, (key, _index) => {
    return regexp.test(key) || CheckSchemaPushStack(stack, context, schema.additionalProperties, value[key]) && context.AddKey(key);
  });
  return isAdditionalProperties;
}
function ErrorAdditionalProperties(stack, context, schemaPath, instancePath, schema, value) {
  const regexp = UnicodeRegExp(GetPropertiesPattern(schema));
  const additionalProperties = [];
  const isAdditionalProperties = guard_exports.EveryAll(guard_exports.Keys(value), 0, (key, _index) => {
    const nextSchemaPath = `${schemaPath}/additionalProperties`;
    const nextInstancePath = `${instancePath}/${key}`;
    const isAdditionalProperty = regexp.test(key) || ErrorSchemaPushStack(stack, context, nextSchemaPath, nextInstancePath, schema.additionalProperties, value[key]) && context.AddKey(key);
    if (!isAdditionalProperty)
      additionalProperties.push(key);
    return isAdditionalProperty;
  });
  return isAdditionalProperties || context.AddError({
    keyword: "additionalProperties",
    schemaPath,
    instancePath,
    params: { additionalProperties }
  });
}

// node_modules/typebox/build/schema/engine/_reducer.mjs
function Reducer(stack, context, schemas, value, check) {
  const results = emit_exports.ConstDeclaration("results", "[]");
  const context_n = schemas.map((_schema, index3) => emit_exports.ConstDeclaration(`context_${index3}`, emit_exports.New("CheckContext", [])));
  const condition_n = schemas.map((schema, index3) => emit_exports.ConstDeclaration(`condition_${index3}`, emit_exports.Call(emit_exports.ArrowFunction(["context"], BuildSchema(stack, context, schema, value)), [`context_${index3}`])));
  const checks = schemas.map((_schema, index3) => emit_exports.If(`condition_${index3}`, emit_exports.Call(emit_exports.Member("results", "push"), [`context_${index3}`])));
  const returns = emit_exports.Return(emit_exports.And(check, context.Merge("results")));
  return emit_exports.Call(emit_exports.ArrowFunction([], emit_exports.Statements([results, ...context_n, ...condition_n, ...checks, returns])), []);
}

// node_modules/typebox/build/schema/engine/allOf.mjs
function BuildAllOfStandard(stack, context, schema, value) {
  return Reducer(stack, context, schema.allOf, value, emit_exports.IsEqual(emit_exports.Member("results", "length"), emit_exports.Constant(schema.allOf.length)));
}
function BuildAllOfFast(stack, context, schema, value) {
  return emit_exports.ReduceAnd(schema.allOf.map((schema2) => BuildSchema(stack, context, schema2, value)));
}
function BuildAllOf(stack, context, schema, value) {
  return context.UseUnevaluated() ? BuildAllOfStandard(stack, context, schema, value) : BuildAllOfFast(stack, context, schema, value);
}
function CheckAllOf(stack, context, schema, value) {
  const results = schema.allOf.reduce((result, schema2) => {
    const nextContext = new CheckContext();
    return CheckSchema(stack, nextContext, schema2, value) ? [...result, nextContext] : result;
  }, []);
  return guard_exports.IsEqual(results.length, schema.allOf.length) && context.Merge(results);
}
function ErrorAllOf(stack, context, schemaPath, instancePath, schema, value) {
  const failedContexts = [];
  const results = schema.allOf.reduce((result, schema2, index3) => {
    const nextSchemaPath = `${schemaPath}/allOf/${index3}`;
    const nextContext = new ErrorContext();
    const isSchema = ErrorSchema(stack, nextContext, nextSchemaPath, instancePath, schema2, value);
    if (!isSchema)
      failedContexts.push(nextContext);
    return isSchema ? [...result, nextContext] : result;
  }, []);
  const isAllOf = guard_exports.IsEqual(results.length, schema.allOf.length) && context.Merge(results);
  if (!isAllOf)
    failedContexts.forEach((failed) => failed.GetErrors().forEach((error) => context.AddError(error)));
  return isAllOf;
}

// node_modules/typebox/build/schema/engine/anyOf.mjs
function BuildAnyOfStandard(stack, context, schema, value) {
  return Reducer(stack, context, schema.anyOf, value, emit_exports.IsGreaterThan(emit_exports.Member("results", "length"), emit_exports.Constant(0)));
}
function BuildAnyOfFast(stack, context, schema, value) {
  return emit_exports.ReduceOr(schema.anyOf.map((schema2) => BuildSchema(stack, context, schema2, value)));
}
function BuildAnyOf(stack, context, schema, value) {
  return context.UseUnevaluated() ? BuildAnyOfStandard(stack, context, schema, value) : BuildAnyOfFast(stack, context, schema, value);
}
function CheckAnyOf(stack, context, schema, value) {
  const results = schema.anyOf.reduce((result, schema2) => {
    const nextContext = new CheckContext();
    return CheckSchema(stack, nextContext, schema2, value) ? [...result, nextContext] : result;
  }, []);
  return guard_exports.IsGreaterThan(results.length, 0) && context.Merge(results);
}
function ErrorAnyOf(stack, context, schemaPath, instancePath, schema, value) {
  const failedContexts = [];
  const results = schema.anyOf.reduce((result, schema2, index3) => {
    const nextContext = new ErrorContext();
    const nextSchemaPath = `${schemaPath}/anyOf/${index3}`;
    const isSchema = ErrorSchema(stack, nextContext, nextSchemaPath, instancePath, schema2, value);
    if (!isSchema)
      failedContexts.push(nextContext);
    return isSchema ? [...result, nextContext] : result;
  }, []);
  const isAnyOf = guard_exports.IsGreaterThan(results.length, 0) && context.Merge(results);
  if (!isAnyOf)
    failedContexts.forEach((failed) => failed.GetErrors().forEach((error) => context.AddError(error)));
  return isAnyOf || context.AddError({
    keyword: "anyOf",
    schemaPath,
    instancePath,
    params: {}
  });
}

// node_modules/typebox/build/schema/engine/boolean.mjs
function BuildSchemaBoolean(_stack, _context, schema, _value) {
  return schema ? emit_exports.Constant(true) : emit_exports.Constant(false);
}
function CheckSchemaBoolean(_stack, _context, schema, _value) {
  return schema;
}
function ErrorSchemaBoolean(stack, context, schemaPath, instancePath, schema, value) {
  return CheckSchemaBoolean(stack, context, schema, value) || context.AddError({
    keyword: "boolean",
    schemaPath,
    instancePath,
    params: {}
  });
}

// node_modules/typebox/build/schema/engine/const.mjs
function BuildConst(_stack, _context, schema, value) {
  return guard_exports.IsValueLike(schema.const) ? emit_exports.IsEqual(value, emit_exports.Constant(schema.const)) : emit_exports.IsDeepEqual(value, CreateVariable(schema.const));
}
function CheckConst(_stack, _context, schema, value) {
  return guard_exports.IsValueLike(schema.const) ? guard_exports.IsEqual(value, schema.const) : guard_exports.IsDeepEqual(value, schema.const);
}
function ErrorConst(stack, context, schemaPath, instancePath, schema, value) {
  return CheckConst(stack, context, schema, value) || context.AddError({
    keyword: "const",
    schemaPath,
    instancePath,
    params: { allowedValue: schema.const }
  });
}

// node_modules/typebox/build/schema/engine/contains.mjs
function IsValid2(schema) {
  return !(IsMinContains(schema) && guard_exports.IsEqual(schema.minContains, 0));
}
function BuildContainsStandard(stack, context, schema, value) {
  const [item, index3] = [Unique(), Unique()];
  const isLength = emit_exports.Not(emit_exports.IsEqual(emit_exports.Member(value, "length"), emit_exports.Constant(0)));
  const isSome = emit_exports.SomeAll(value, [item, index3], emit_exports.And(BuildSchema(stack, context, schema.contains, item), context.AddIndex(index3)));
  return emit_exports.And(isLength, isSome);
}
function BuildContainsFast(stack, context, schema, value) {
  const [item] = [Unique()];
  const isLength = emit_exports.Not(emit_exports.IsEqual(emit_exports.Member(value, "length"), emit_exports.Constant(0)));
  const isSome = emit_exports.Some(value, [item, "_"], BuildSchema(stack, context, schema.contains, item));
  return emit_exports.And(isLength, isSome);
}
function BuildContains(stack, context, schema, value) {
  if (!IsValid2(schema))
    return emit_exports.Constant(true);
  return context.UseUnevaluated() ? BuildContainsStandard(stack, context, schema, value) : BuildContainsFast(stack, context, schema, value);
}
function CheckContains(stack, context, schema, value) {
  if (!IsValid2(schema))
    return true;
  return !guard_exports.IsEqual(value.length, 0) && guard_exports.SomeAll(value, (item, index3) => {
    return CheckSchema(stack, context, schema.contains, item) && context.AddIndex(index3);
  });
}
function ErrorContains(stack, context, schemaPath, instancePath, schema, value) {
  return CheckContains(stack, context, schema, value) || context.AddError({
    keyword: "contains",
    schemaPath,
    instancePath,
    params: { minContains: 1 }
  });
}

// node_modules/typebox/build/schema/engine/dependencies.mjs
function BuildDependencies(stack, context, schema, value) {
  const isLength = emit_exports.IsEqual(emit_exports.Member(emit_exports.Keys(value), "length"), emit_exports.Constant(0));
  const isEveryDependency = emit_exports.ReduceAnd(guard_exports.Entries(schema.dependencies).map(([key, schema2]) => {
    const notKey = emit_exports.Not(emit_exports.HasPropertyKey(value, emit_exports.Constant(key)));
    const isSchema = BuildSchema(stack, context, schema2, value);
    const isEveryKey = (schema3) => emit_exports.ReduceAnd(schema3.map((key2) => emit_exports.HasPropertyKey(value, emit_exports.Constant(key2))));
    return emit_exports.Or(notKey, guard_exports.IsArray(schema2) ? isEveryKey(schema2) : isSchema);
  }));
  return emit_exports.Or(isLength, isEveryDependency);
}
function CheckDependencies(stack, context, schema, value) {
  const isLength = guard_exports.IsEqual(guard_exports.Keys(value).length, 0);
  const isEvery = guard_exports.Every(guard_exports.Entries(schema.dependencies), 0, ([key, schema2]) => {
    return !guard_exports.HasPropertyKey(value, key) || (guard_exports.IsArray(schema2) ? schema2.every((key2) => guard_exports.HasPropertyKey(value, key2)) : CheckSchema(stack, context, schema2, value));
  });
  return isLength || isEvery;
}
function ErrorDependencies(stack, context, schemaPath, instancePath, schema, value) {
  const isLength = guard_exports.IsEqual(guard_exports.Keys(value).length, 0);
  const isEvery = guard_exports.EveryAll(guard_exports.Entries(schema.dependencies), 0, ([key, schema2]) => {
    const nextSchemaPath = `${schemaPath}/dependencies/${key}`;
    return !guard_exports.HasPropertyKey(value, key) || (guard_exports.IsArray(schema2) ? schema2.every((dependency) => guard_exports.HasPropertyKey(value, dependency) || context.AddError({
      keyword: "dependencies",
      schemaPath,
      instancePath,
      params: { property: key, dependencies: schema2 }
    })) : ErrorSchema(stack, context, nextSchemaPath, instancePath, schema2, value));
  });
  return isLength || isEvery;
}

// node_modules/typebox/build/schema/engine/dependentRequired.mjs
function BuildDependentRequired(_stack, _context, schema, value) {
  const isLength = emit_exports.IsEqual(emit_exports.Member(emit_exports.Keys(value), "length"), emit_exports.Constant(0));
  const isEvery = emit_exports.ReduceAnd(guard_exports.Entries(schema.dependentRequired).map(([key, keys]) => {
    const notKey = emit_exports.Not(emit_exports.HasPropertyKey(value, emit_exports.Constant(key)));
    const everyKey = emit_exports.ReduceAnd(keys.map((key2) => emit_exports.HasPropertyKey(value, emit_exports.Constant(key2))));
    return emit_exports.Or(notKey, everyKey);
  }));
  return emit_exports.Or(isLength, isEvery);
}
function CheckDependentRequired(_stack, _context, schema, value) {
  const isLength = guard_exports.IsEqual(guard_exports.Keys(value).length, 0);
  const isEvery = guard_exports.Every(guard_exports.Entries(schema.dependentRequired), 0, ([key, keys]) => {
    return !guard_exports.HasPropertyKey(value, key) || keys.every((key2) => guard_exports.HasPropertyKey(value, key2));
  });
  return isLength || isEvery;
}
function ErrorDependentRequired(_stack, context, schemaPath, instancePath, schema, value) {
  const isLength = guard_exports.IsEqual(guard_exports.Keys(value).length, 0);
  const isEveryEntry = guard_exports.EveryAll(guard_exports.Entries(schema.dependentRequired), 0, ([key, keys]) => {
    return !guard_exports.HasPropertyKey(value, key) || guard_exports.EveryAll(keys, 0, (dependency) => guard_exports.HasPropertyKey(value, dependency) || context.AddError({
      keyword: "dependentRequired",
      schemaPath,
      instancePath,
      params: { property: key, dependencies: keys }
    }));
  });
  return isLength || isEveryEntry;
}

// node_modules/typebox/build/schema/engine/dependentSchemas.mjs
function BuildDependentSchemas(stack, context, schema, value) {
  const isLength = emit_exports.IsEqual(emit_exports.Member(emit_exports.Keys(value), "length"), emit_exports.Constant(0));
  const isEvery = emit_exports.ReduceAnd(guard_exports.Entries(schema.dependentSchemas).map(([key, schema2]) => {
    const notKey = emit_exports.Not(emit_exports.HasPropertyKey(value, emit_exports.Constant(key)));
    const isSchema = BuildSchema(stack, context, schema2, value);
    return emit_exports.Or(notKey, isSchema);
  }));
  return emit_exports.Or(isLength, isEvery);
}
function CheckDependentSchemas(stack, context, schema, value) {
  const isLength = guard_exports.IsEqual(guard_exports.Keys(value).length, 0);
  const isEvery = guard_exports.Every(guard_exports.Entries(schema.dependentSchemas), 0, ([key, schema2]) => {
    return !guard_exports.HasPropertyKey(value, key) || CheckSchema(stack, context, schema2, value);
  });
  return isLength || isEvery;
}
function ErrorDependentSchemas(stack, context, schemaPath, instancePath, schema, value) {
  const isLength = guard_exports.IsEqual(guard_exports.Keys(value).length, 0);
  const isEvery = guard_exports.EveryAll(guard_exports.Entries(schema.dependentSchemas), 0, ([key, schema2]) => {
    const nextSchemaPath = `${schemaPath}/dependentSchemas/${key}`;
    return !guard_exports.HasPropertyKey(value, key) || ErrorSchema(stack, context, nextSchemaPath, instancePath, schema2, value);
  });
  return isLength || isEvery;
}

// node_modules/typebox/build/schema/engine/dynamicRef.mjs
function BuildDynamicRef(stack, context, schema, value) {
  const target = stack.DynamicRef(schema) ?? false;
  return CreateFunction(stack, context, target, value);
}
function CheckDynamicRef(stack, context, schema, value) {
  const target = stack.DynamicRef(schema) ?? false;
  return IsSchema2(target) && CheckSchema(stack, context, target, value);
}
function ErrorDynamicRef(stack, context, _schemaPath, instancePath, schema, value) {
  const target = stack.DynamicRef(schema) ?? false;
  return IsSchema2(target) && ErrorSchema(stack, context, "#", instancePath, target, value);
}

// node_modules/typebox/build/schema/engine/enum.mjs
function BuildEnum(_stack, _context, schema, value) {
  return emit_exports.ReduceOr(schema.enum.map((option) => {
    if (guard_exports.IsValueLike(option))
      return emit_exports.IsEqual(value, emit_exports.Constant(option));
    const variable = CreateVariable(option);
    return emit_exports.IsDeepEqual(value, variable);
  }));
}
function CheckEnum(_stack, _context, schema, value) {
  return guard_exports.Some(schema.enum, (option) => guard_exports.IsValueLike(option) ? guard_exports.IsEqual(value, option) : guard_exports.IsDeepEqual(value, option));
}
function ErrorEnum(stack, context, schemaPath, instancePath, schema, value) {
  return CheckEnum(stack, context, schema, value) || context.AddError({
    keyword: "enum",
    schemaPath,
    instancePath,
    params: { allowedValues: schema.enum }
  });
}

// node_modules/typebox/build/schema/engine/exclusiveMaximum.mjs
function BuildExclusiveMaximum(_stack, _context, schema, value) {
  return emit_exports.IsLessThan(value, emit_exports.Constant(schema.exclusiveMaximum));
}
function CheckExclusiveMaximum(_stack, _context, schema, value) {
  return guard_exports.IsLessThan(value, schema.exclusiveMaximum);
}
function ErrorExclusiveMaximum(stack, context, schemaPath, instancePath, schema, value) {
  return CheckExclusiveMaximum(stack, context, schema, value) || context.AddError({
    keyword: "exclusiveMaximum",
    schemaPath,
    instancePath,
    params: { comparison: "<", limit: schema.exclusiveMaximum }
  });
}

// node_modules/typebox/build/schema/engine/exclusiveMinimum.mjs
function BuildExclusiveMinimum(_stack, _context, schema, value) {
  return emit_exports.IsGreaterThan(value, emit_exports.Constant(schema.exclusiveMinimum));
}
function CheckExclusiveMinimum(_stack, _context, schema, value) {
  return guard_exports.IsGreaterThan(value, schema.exclusiveMinimum);
}
function ErrorExclusiveMinimum(stack, context, schemaPath, instancePath, schema, value) {
  return CheckExclusiveMinimum(stack, context, schema, value) || context.AddError({
    keyword: "exclusiveMinimum",
    schemaPath,
    instancePath,
    params: { comparison: ">", limit: schema.exclusiveMinimum }
  });
}

// node_modules/typebox/build/format/format.mjs
var format_exports = {};
__export(format_exports, {
  Clear: () => Clear,
  Entries: () => Entries,
  Get: () => Get3,
  Has: () => Has2,
  IsDate: () => IsDate,
  IsDateTime: () => IsDateTime,
  IsDuration: () => IsDuration,
  IsEmail: () => IsEmail,
  IsHostname: () => IsHostname2,
  IsIPv4: () => IsIPv4,
  IsIPv6: () => IsIPv6,
  IsIdnEmail: () => IsIdnEmail,
  IsIdnHostname: () => IsIdnHostname2,
  IsIri: () => IsIri,
  IsIriReference: () => IsIriReference,
  IsJsonPointer: () => IsJsonPointer,
  IsJsonPointerUriFragment: () => IsJsonPointerUriFragment,
  IsRegex: () => IsRegex,
  IsRelativeJsonPointer: () => IsRelativeJsonPointer,
  IsTime: () => IsTime,
  IsUri: () => IsUri,
  IsUriReference: () => IsUriReference,
  IsUriTemplate: () => IsUriTemplate,
  IsUrl: () => IsUrl,
  IsUuid: () => IsUuid,
  Reset: () => Reset,
  Set: () => Set3,
  Test: () => Test
});

// node_modules/typebox/build/format/date.mjs
var DAYS = [0, 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
var DATE = /^(\d\d\d\d)-(\d\d)-(\d\d)$/;
function IsLeapYear(year) {
  return year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
}
function IsDate(value) {
  const matches = DATE.exec(value);
  if (!matches)
    return false;
  const year = +matches[1];
  const month = +matches[2];
  const day = +matches[3];
  return month >= 1 && month <= 12 && day >= 1 && day <= (month === 2 && IsLeapYear(year) ? 29 : DAYS[month]);
}

// node_modules/typebox/build/format/time.mjs
var TIME = /^(\d\d):(\d\d):(\d\d)(?:\.\d+)?(?:([Zz])|([+-])(\d\d):(\d\d))?$/;
function IsTime(value, strictTimeZone = true) {
  const matches = TIME.exec(value);
  if (!matches)
    return false;
  if (strictTimeZone && !matches[4] && !matches[5])
    return false;
  const hr = +matches[1];
  const min = +matches[2];
  const sec = +matches[3];
  if (hr > 23 || min > 59 || sec > 60)
    return false;
  if (matches[5]) {
    const tzH2 = +matches[6];
    const tzM2 = +matches[7];
    if (tzH2 > 23 || tzM2 > 59)
      return false;
  }
  if (sec < 60)
    return true;
  const tzSign = matches[5] === "-" ? -1 : 1;
  const tzH = +(matches[6] || 0);
  const tzM = +(matches[7] || 0);
  const totalUtcMin = hr * 60 + min - tzSign * (tzH * 60 + tzM);
  return (totalUtcMin % 1440 + 1440) % 1440 === 1439;
}

// node_modules/typebox/build/format/date_time.mjs
function IsDateTime(value) {
  const dateTime = value.split(/T/i);
  return dateTime.length === 2 && IsDate(dateTime[0]) && IsTime(dateTime[1]);
}

// node_modules/typebox/build/format/duration.mjs
var Duration = /^P((\d+Y(\d+M(\d+D)?)?|\d+M(\d+D)?|\d+D)(T(\d+H(\d+M(\d+S)?)?|\d+M(\d+S)?|\d+S))?|T(\d+H(\d+M(\d+S)?)?|\d+M(\d+S)?|\d+S)|\d+W)$/;
function IsDuration(value) {
  return Duration.test(value);
}

// node_modules/typebox/build/format/email.mjs
var Email = /^(?:[a-z0-9!#$%&'*+/=?^_`{|}~-]+(?:\.[a-z0-9!#$%&'*+/=?^_`{|}~-]+)*|"(?:[^"\\]|\\[\x20-\x7e])*")@(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*|\[(?:IPv6:[a-f0-9:]+|(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])(?:\.(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])){3})\])$/i;
function IsEmail(value) {
  return Email.test(value);
}

// node_modules/typebox/build/format/idna/pattern/pattern.mjs
var RE_RULE_HYPHEN_PLACEMENT = /^(?!-).*(?<!-)$/;
var RE_RULE_NOT_RESERVED_ACE = /^(?!..--)/;
var RE_ASCII_LDH = /^[a-zA-Z0-9-]*$/;
var RE_NON_ASCII = /[^\p{ASCII}]/u;
var RE_ASCII_DIGIT = /[0-9]/;
var RE_ARABIC_INDIC_DIGIT = /[\u{0660}-\u{0669}]/u;
var RE_EXT_ARABIC_INDIC_DIGIT = /[\u{06f0}-\u{06f9}]/u;
var RE_COMMON_SEPARATOR = /[\u{002e}\u{002c}\u{003a}\u{002f}]/u;
var RE_EUROPEAN_SEPARATOR = /[\u{002d}\u{002b}]/u;
var RE_MARK_NONSPACING = /\p{Mn}/u;
var RE_MARK_SPACING_COMBINING = /\p{Mc}/u;
var RE_COMBINING_MARK = /[\p{Mn}\p{Mc}\p{Me}]/u;
var RE_LETTER = /\p{L}/u;
var RE_NUMBER_DECIMAL = /\p{Nd}/u;
var RE_SCRIPT_GREEK = /\p{Script=Greek}/u;
var RE_SCRIPT_HEBREW = /\p{Script=Hebrew}/u;
var RE_SCRIPT_JAPANESE = /[\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Han}]/u;
var RE_SCRIPT_ARABIC_LETTER = /[\p{Script=Arabic}\p{Script=Syriac}\p{Script=Thaana}\p{Script=Mandaic}]/u;
var RE_VIRAMA = /[\u{094d}\u{09cd}\u{0a4d}\u{0acd}\u{0b4d}\u{0bcd}\u{0c4d}\u{0ccd}\u{0d3b}\u{0d3c}\u{0d4d}\u{0dca}\u{1b44}\u{1baa}\u{1bab}\u{a9c0}\u{11046}\u{1107f}\u{110b9}\u{11133}\u{11134}\u{111c0}\u{11235}\u{1134d}\u{11442}\u{114c2}\u{115bf}\u{1163f}\u{116b6}\u{11c3f}\u{11d44}\u{11d45}]/u;
var RE_RFC5892_DISALLOWED = /[\u{0640}\u{07fa}\u{302e}\u{302f}\u{3031}\u{3032}\u{3033}\u{3034}\u{3035}\u{303b}]/u;
var RE_CONTEXTO_EXCEPTIONS = /[\u{00b7}\u{0375}\u{05f3}\u{05f4}\u{200c}\u{200d}\u{30fb}]/u;
var RE_PVALID_EXCEPTIONS = /[\u{00df}\u{03c2}\u{06fd}\u{06fe}\u{0f0b}\u{3007}]/u;
var RE_EUROPEAN_NUMBER = new RegExp([
  RE_ASCII_DIGIT,
  RE_EXT_ARABIC_INDIC_DIGIT
].map((regexp) => regexp.source).join("|"), "u");
var RE_PERMITTED_CATEGORY = new RegExp([
  RE_LETTER,
  RE_EUROPEAN_SEPARATOR,
  RE_COMMON_SEPARATOR,
  RE_NUMBER_DECIMAL,
  RE_MARK_NONSPACING,
  RE_MARK_SPACING_COMBINING,
  RE_CONTEXTO_EXCEPTIONS,
  RE_PVALID_EXCEPTIONS
].map((regexp) => regexp.source).join("|"), "u");

// node_modules/typebox/build/format/idna/label/ascii.mjs
function IsAsciiLabel(value) {
  return RE_RULE_HYPHEN_PLACEMENT.test(value) && RE_RULE_NOT_RESERVED_ACE.test(value) && RE_ASCII_LDH.test(value);
}

// node_modules/typebox/build/format/idna/format/puny.mjs
var PUNYCODE_BASE = 36;
var PUNYCODE_TMIN = 1;
var PUNYCODE_TMAX = 26;
var PUNYCODE_SKEW = 38;
var PUNYCODE_DAMP = 700;
var PUNYCODE_INITIAL_BIAS = 72;
var PUNYCODE_INITIAL_N = 128;
function ThrowDontCare() {
  throw null;
}
function IsAcePrefixed(value) {
  return value.toLowerCase().startsWith("xn--");
}
function Adapt(delta, numPoints, firstTime) {
  delta = firstTime ? Math.floor(delta / PUNYCODE_DAMP) : delta >> 1;
  delta += Math.floor(delta / numPoints);
  let k = 0;
  while (delta > (PUNYCODE_BASE - PUNYCODE_TMIN) * PUNYCODE_TMAX >> 1) {
    delta = Math.floor(delta / (PUNYCODE_BASE - PUNYCODE_TMIN));
    k += PUNYCODE_BASE;
  }
  return k + Math.floor((PUNYCODE_BASE - PUNYCODE_TMIN + 1) * delta / (delta + PUNYCODE_SKEW));
}
function Decode(value) {
  const output = [];
  let n = PUNYCODE_INITIAL_N;
  let i = 0;
  let bias = PUNYCODE_INITIAL_BIAS;
  const delimIdx = value.lastIndexOf("-");
  if (delimIdx > 0) {
    for (let j = 0; j < delimIdx; j++) {
      const cp = value.charCodeAt(j);
      if (cp >= 128)
        ThrowDontCare();
      output.push(cp);
    }
  }
  let inIdx = delimIdx < 0 ? 0 : delimIdx + 1;
  while (inIdx < value.length) {
    const oldi = i;
    let w = 1;
    let k = PUNYCODE_BASE;
    while (true) {
      if (inIdx >= value.length)
        ThrowDontCare();
      const ch = value.charCodeAt(inIdx++);
      let digit;
      if (ch >= 97 && ch <= 122)
        digit = ch - 97;
      else if (ch >= 48 && ch <= 57)
        digit = ch - 48 + 26;
      else
        ThrowDontCare();
      i += digit * w;
      const t = k <= bias ? PUNYCODE_TMIN : k >= bias + PUNYCODE_TMAX ? PUNYCODE_TMAX : k - bias;
      if (digit < t)
        break;
      w *= PUNYCODE_BASE - t;
      k += PUNYCODE_BASE;
    }
    const outLen = output.length + 1;
    bias = Adapt(i - oldi, outLen, oldi === 0);
    n += Math.floor(i / outLen);
    i %= outLen;
    output.splice(i, 0, n);
    i++;
  }
  return String.fromCodePoint(...output);
}
function DigitToChar(digit) {
  return digit < 26 ? String.fromCharCode(digit + 97) : String.fromCharCode(digit - 26 + 48);
}
var RE_REPLACE_NON_ASCII = new RegExp(RE_NON_ASCII.source, "gu");
function Encode(input) {
  const basic = input.replace(RE_REPLACE_NON_ASCII, "");
  const basicLength = basic.length;
  const result = basicLength > 0 ? [basic, "-"] : [];
  const codePoints = Array.from(input, (char) => char.codePointAt(0));
  let n = PUNYCODE_INITIAL_N;
  let delta = 0;
  let bias = PUNYCODE_INITIAL_BIAS;
  let handledCPCount = basicLength;
  while (handledCPCount < codePoints.length) {
    let m = Infinity;
    for (const cp of codePoints) {
      if (cp >= n && cp < m)
        m = cp;
    }
    delta += (m - n) * (handledCPCount + 1);
    n = m;
    for (const cp of codePoints) {
      if (cp < n)
        delta++;
      if (cp === n) {
        let q = delta;
        for (let k = PUNYCODE_BASE; ; k += PUNYCODE_BASE) {
          const t = k <= bias ? PUNYCODE_TMIN : k >= bias + PUNYCODE_TMAX ? PUNYCODE_TMAX : k - bias;
          if (q < t)
            break;
          const digit = t + (q - t) % (PUNYCODE_BASE - t);
          result.push(DigitToChar(digit));
          q = Math.floor((q - t) / (PUNYCODE_BASE - t));
        }
        result.push(DigitToChar(q));
        bias = Adapt(delta, handledCPCount + 1, handledCPCount === basicLength);
        delta = 0;
        handledCPCount++;
      }
    }
    delta++;
    n++;
  }
  return result.join("");
}

// node_modules/typebox/build/format/idna/format/bidi.mjs
var RE_RTL_ALLOWED = /^(?:R|AL|AN|EN|ES|CS|ET|ON|BN|NSM)$/;
var RE_LTR_ALLOWED = /^(?:L|EN|ES|CS|ET|ON|BN|NSM)$/;
var RE_RTL_CLASSES = /^(?:R|AL|AN)$/;
function HasBidiChars(value) {
  if (IsAcePrefixed(value)) {
    try {
      return HasRightToLeftCharacters(Decode(value.slice(4).toLowerCase()));
    } catch {
      return false;
    }
  }
  return HasRightToLeftCharacters(value);
}
function GetBidiClass(codePoint) {
  const char = String.fromCodePoint(codePoint);
  return RE_EUROPEAN_NUMBER.test(char) ? "EN" : RE_ARABIC_INDIC_DIGIT.test(char) ? "AN" : (
    // Pattern.RE_EUROPEAN_SEPARATOR.test(char) ? 'ES' : // (no-spec-coverage)
    // Pattern.RE_COMMON_SEPARATOR.test(char) ? 'CS' : // (no-spec-coverage)
    RE_MARK_NONSPACING.test(char) ? "NSM" : RE_SCRIPT_HEBREW.test(char) ? "R" : RE_SCRIPT_ARABIC_LETTER.test(char) ? "AL" : RE_LETTER.test(char) ? "L" : "ON"
  );
}
function HasRightToLeftCharacters(value) {
  for (const ch of value)
    if (RE_RTL_CLASSES.test(GetBidiClass(ch.codePointAt(0))))
      return true;
  return false;
}
function SatisfiesBidiRule(value) {
  let isRtl = false;
  let allowed = RE_LTR_ALLOWED;
  let sawEN = false;
  let sawAN = false;
  let isFirst = true;
  for (const ch of value) {
    const bidiClass = GetBidiClass(ch.codePointAt(0));
    if (isFirst) {
      if (bidiClass !== "L" && bidiClass !== "R" && bidiClass !== "AL")
        return false;
      isRtl = bidiClass === "R" || bidiClass === "AL";
      allowed = isRtl ? RE_RTL_ALLOWED : RE_LTR_ALLOWED;
      isFirst = false;
    }
    if (!allowed.test(bidiClass))
      return false;
    if (bidiClass === "EN")
      sawEN = true;
    else if (bidiClass === "AN")
      sawAN = true;
  }
  if (isRtl && sawEN && sawAN)
    return false;
  return true;
}

// node_modules/typebox/build/format/idna/label/unicode.mjs
function ExceedsMaxALabelLength(value) {
  return RE_NON_ASCII.test(value) && Encode(value).length + 4 > 63;
}
function HasInvalidHyphens(chars) {
  if (chars[0] === "-" || chars[chars.length - 1] === "-")
    return true;
  return chars.slice(2).join("").startsWith("--");
}
function IsUnicodeLabel(value) {
  if (ExceedsMaxALabelLength(value))
    return false;
  if (HasRightToLeftCharacters(value) && !SatisfiesBidiRule(value))
    return false;
  const chars = [...value];
  const codePoints = chars.map((c) => c.codePointAt(0));
  const length = codePoints.length;
  if (HasInvalidHyphens(chars))
    return false;
  if (RE_COMBINING_MARK.test(chars[0]))
    return false;
  let hasJapanese = false;
  for (let i = 0; i < length; i++) {
    const codePoint = codePoints[i];
    const char = chars[i];
    if (RE_RFC5892_DISALLOWED.test(char))
      return false;
    if (!RE_PERMITTED_CATEGORY.test(char))
      return false;
    if (RE_SCRIPT_JAPANESE.test(char))
      hasJapanese = true;
    const prev = codePoints[i - 1], next = codePoints[i + 1];
    switch (codePoint) {
      case 183:
        if (prev !== 108 || next !== 108)
          return false;
        break;
      // MIDDLE DOT (Catalan)
      case 885:
        if (!next || !RE_SCRIPT_GREEK.test(chars[i + 1]))
          return false;
        break;
      // Greek KERAIA
      case 1523:
      case 1524:
        if (!prev || !RE_SCRIPT_HEBREW.test(chars[i - 1]))
          return false;
        break;
      // Hebrew GERESH
      case 8204:
        if (!prev || prev < 128 && !RE_VIRAMA.test(chars[i - 1]))
          return false;
        break;
      case 8205:
        if (!prev || !RE_VIRAMA.test(chars[i - 1]))
          return false;
        break;
      case 12539:
        break;
    }
  }
  if (value.includes("\u30FB") && !hasJapanese)
    return false;
  return true;
}

// node_modules/typebox/build/format/idna/label/puny.mjs
function IsPunyLabel(value) {
  if (!IsAcePrefixed(value))
    return false;
  try {
    const body = value.slice(4).toLowerCase();
    if (body.lastIndexOf("-") === 0)
      return false;
    const decoded = Decode(body);
    if (!RE_NON_ASCII.test(decoded))
      return false;
    return IsUnicodeLabel(decoded);
  } catch {
    return false;
  }
}

// node_modules/typebox/build/format/idna/hostname.mjs
function IsValidLabelLength(value) {
  return value.length > 0 && value.length <= 63;
}
function IsLabel(value) {
  return IsValidLabelLength(value) && (IsPunyLabel(value) || IsAsciiLabel(value));
}
function IsHostname(value) {
  if (value.length === 0 || value.length > 253)
    return false;
  if (value.charCodeAt(value.length - 1) === 46)
    return false;
  return value.split(".").every((label) => IsLabel(label));
}

// node_modules/typebox/build/format/idna/idn-hostname.mjs
function IsValidLabelLength2(value) {
  return value.length > 0 && value.length <= 63;
}
function IsLabel2(value) {
  return IsValidLabelLength2(value) && (IsPunyLabel(value) || IsUnicodeLabel(value));
}
function NormalizeHostname(value) {
  return value.replace(/[\uff01-\uff5e]/g, (char) => String.fromCharCode(char.charCodeAt(0) - 65248)).normalize("NFC").replace(/[\u00ad\u034f\u180b-\u180d\u200b\ufe00-\ufe0f\u{e0100}-\u{e01ef}]/gu, "").replace(/[\u002E\u3002\uFF0E\uFF61]/g, ".");
}
function IsIdnHostname(value) {
  if (value.length === 0 || value.includes(" "))
    return false;
  const normalized = NormalizeHostname(value);
  if (normalized.length > 253)
    return false;
  const labels = normalized.split(".");
  const hasBidiChars = labels.some((label) => HasBidiChars(label));
  return labels.every((label) => IsLabel2(label) && (!hasBidiChars || SatisfiesBidiRule(label)));
}

// node_modules/typebox/build/format/hostname.mjs
function IsHostname2(value) {
  return IsHostname(value);
}

// node_modules/typebox/build/format/idn_email.mjs
var IdnEmail = /^(?:[A-Za-z0-9!#$%&'*+\/=?^_`{|}~\u{0080}-\u{10FFFF}-]+(?:\.[A-Za-z0-9!#$%&'*+\/=?^_`{|}~\u{0080}-\u{10FFFF}-]+)*|"(?:[^"\\]|\\.)*")@[\p{L}\p{N}](?:[\p{L}\p{N}-]{0,62})(?<!-)(?:\.[\p{L}\p{N}](?:[\p{L}\p{N}-]{0,62})(?<!-))*$/iu;
function IsIdnEmail(value) {
  return IdnEmail.test(value.normalize("NFC"));
}

// node_modules/typebox/build/format/idn_hostname.mjs
function IsIdnHostname2(value) {
  return IsIdnHostname(value);
}

// node_modules/typebox/build/format/ipv4.mjs
var IPv4 = /^(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)$/;
function IsIPv4(value) {
  return IPv4.test(value);
}

// node_modules/typebox/build/format/ipv6.mjs
var IPv6 = /^(?:(?:(?:[0-9a-f]{1,4}:){6}|::(?:[0-9a-f]{1,4}:){5}|(?:[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){4}|(?:(?:[0-9a-f]{1,4}:)?[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){3}|(?:(?:[0-9a-f]{1,4}:){0,2}[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){2}|(?:(?:[0-9a-f]{1,4}:){0,3}[0-9a-f]{1,4})?::[0-9a-f]{1,4}:|(?:(?:[0-9a-f]{1,4}:){0,4}[0-9a-f]{1,4})?::)(?:[0-9a-f]{1,4}:[0-9a-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|(?:(?:[0-9a-f]{1,4}:){0,5}[0-9a-f]{1,4})?::[0-9a-f]{1,4}|(?:(?:[0-9a-f]{1,4}:){0,6}[0-9a-f]{1,4})?::)$/i;
function IsIPv6(value) {
  return IPv6.test(value);
}

// node_modules/typebox/build/format/iri_reference.mjs
var InvalidIriChars = /[\x00-\x20\x7F\\]|%(?![0-9a-fA-F]{2})/;
var MalformedScheme = /^[a-zA-Z][a-zA-Z0-9+\-.]*\/\//;
function IsIriReference(value) {
  return !InvalidIriChars.test(value) && !MalformedScheme.test(value) && URL.canParse(value, "http://example.com");
}

// node_modules/typebox/build/format/iri.mjs
var IpvFutureMatchMaxLength = 2048;
var IpvFutureMatch = /\[[vV][0-9a-fA-F]+\.[^\]]+\]/;
var InvalidIriChars2 = /[\x00-\x20<>\^`{|}\\]/;
var InvalidPercentEncoding = /%(?![0-9a-fA-F]{2})/;
function NarrowIpvFuture(value) {
  return value.length < IpvFutureMatchMaxLength ? value.replace(IpvFutureMatch, "[::1]") : value;
}
function IsIri(value) {
  if (InvalidIriChars2.test(value))
    return false;
  if (InvalidPercentEncoding.test(value))
    return false;
  return URL.canParse(NarrowIpvFuture(value));
}

// node_modules/typebox/build/format/json_pointer_uri_fragment.mjs
var JsonPointerUriFragment = /^#(?:\/(?:[a-z0-9_\-.!$&'()*+,;:=@]|%[0-9a-f]{2}|~0|~1)*)*$/i;
function IsJsonPointerUriFragment(value) {
  return JsonPointerUriFragment.test(value);
}

// node_modules/typebox/build/format/json_pointer.mjs
var JsonPointer = /^(?:\/(?:[^~/]|~0|~1)*)*$/;
function IsJsonPointer(value) {
  return JsonPointer.test(value);
}

// node_modules/typebox/build/format/regex.mjs
function IsRegex(value) {
  try {
    new RegExp(value, "u");
    return true;
  } catch {
    return false;
  }
}

// node_modules/typebox/build/format/relative_json_pointer.mjs
var RelativeJsonPointer = /^(?:0|[1-9][0-9]*)(?:#|(?:\/(?:[^~/]|~0|~1)*)*)$/;
function IsRelativeJsonPointer(value) {
  return RelativeJsonPointer.test(value);
}

// node_modules/typebox/build/format/uri_reference.mjs
var UriReference = /^(?:[a-z][a-z0-9+\-.]*:(?:\/\/(?:(?:[-a-z0-9._~!$&'()*+,;=:]|%[0-9a-f]{2})*@)?(?:\[(?:(?:(?:[\da-f]{1,4}:){6}(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|::(?:[\da-f]{1,4}:){5}(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|(?:[\da-f]{1,4})?::(?:[\da-f]{1,4}:){4}(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|(?:(?:[\da-f]{1,4}:){0,1}[\da-f]{1,4})?::(?:[\da-f]{1,4}:){3}(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|(?:(?:[\da-f]{1,4}:){0,2}[\da-f]{1,4})?::(?:[\da-f]{1,4}:){2}(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|(?:(?:[\da-f]{1,4}:){0,3}[\da-f]{1,4})?::[\da-f]{1,4}:(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|(?:(?:[\da-f]{1,4}:){0,4}[\da-f]{1,4})?::(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|(?:(?:[\da-f]{1,4}:){0,5}[\da-f]{1,4})?::[\da-f]{1,4}|(?:(?:[\da-f]{1,4}:){0,6}[\da-f]{1,4})?::)|v[0-9a-f]+\.[-a-z0-9._~!$&'()*+,;=:]+)\]|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)|(?:[-a-z0-9._~!$&'()*+,;=]|%[0-9a-f]{2})*)(?::\d*)?(?:\/(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*|\/(?:(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})+(?:\/(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*)?|(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})+(?:\/(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*)?|(?:\/\/(?:(?:[-a-z0-9._~!$&'()*+,;=:]|%[0-9a-f]{2})*@)?(?:\[(?:(?:(?:[\da-f]{1,4}:){6}(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|::(?:[\da-f]{1,4}:){5}(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|(?:[\da-f]{1,4})?::(?:[\da-f]{1,4}:){4}(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|(?:(?:[\da-f]{1,4}:){0,1}[\da-f]{1,4})?::(?:[\da-f]{1,4}:){3}(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|(?:(?:[\da-f]{1,4}:){0,2}[\da-f]{1,4})?::(?:[\da-f]{1,4}:){2}(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|(?:(?:[\da-f]{1,4}:){0,3}[\da-f]{1,4})?::[\da-f]{1,4}:(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|(?:(?:[\da-f]{1,4}:){0,4}[\da-f]{1,4})?::(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|(?:(?:[\da-f]{1,4}:){0,5}[\da-f]{1,4})?::[\da-f]{1,4}|(?:(?:[\da-f]{1,4}:){0,6}[\da-f]{1,4})?::)|v[0-9a-f]+\.[-a-z0-9._~!$&'()*+,;=:]+)\]|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)|(?:[-a-z0-9._~!$&'()*+,;=]|%[0-9a-f]{2})*)(?::\d*)?(?:\/(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*|\/(?:(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})+(?:\/(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*)?|(?:[-a-z0-9._~!$&'()*+,;=@]|%[0-9a-f]{2})+(?:\/(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*)?)(?:\?(?:[-a-z0-9._~!$&'()*+,;=:@/?]|%[0-9a-f]{2})*)?(?:#(?:[-a-z0-9._~!$&'()*+,;=:@/?]|%[0-9a-f]{2})*)?$/i;
function IsUriReference(value) {
  return UriReference.test(value);
}

// node_modules/typebox/build/format/uri_template.mjs
var UriTemplate = /^(?:(?:[^\x00-\x20"<>%\\^`{|}\x7f]|%[0-9a-f]{2})|\{[+#./;?&=,!@|]?(?:[a-z0-9_]|%[0-9a-f]{2})+(?:\.(?:[a-z0-9_]|%[0-9a-f]{2})+)*(?::[1-9]\d{0,3}|\*)?(?:,(?:[a-z0-9_]|%[0-9a-f]{2})+(?:\.(?:[a-z0-9_]|%[0-9a-f]{2})+)*(?::[1-9]\d{0,3}|\*)?)*\})*$/i;
function IsUriTemplate(value) {
  return UriTemplate.test(value);
}

// node_modules/typebox/build/format/uri.mjs
var Uri = /^[a-z][a-z0-9+\-.]*:(?:\/\/(?:(?:[-a-z0-9._~!$&'()*+,;=:]|%[0-9a-f]{2})*@)?(?:\[(?:(?:(?:[\da-f]{1,4}:){6}(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|::(?:[\da-f]{1,4}:){5}(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|(?:[\da-f]{1,4})?::(?:[\da-f]{1,4}:){4}(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|(?:(?:[\da-f]{1,4}:){0,1}[\da-f]{1,4})?::(?:[\da-f]{1,4}:){3}(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|(?:(?:[\da-f]{1,4}:){0,2}[\da-f]{1,4})?::(?:[\da-f]{1,4}:){2}(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|(?:(?:[\da-f]{1,4}:){0,3}[\da-f]{1,4})?::[\da-f]{1,4}:(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|(?:(?:[\da-f]{1,4}:){0,4}[\da-f]{1,4})?::(?:[\da-f]{1,4}:[\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d))|(?:(?:[\da-f]{1,4}:){0,5}[\da-f]{1,4})?::[\da-f]{1,4}|(?:(?:[\da-f]{1,4}:){0,6}[\da-f]{1,4})?::)|v[0-9a-f]+\.[-a-z0-9._~!$&'()*+,;=:]+)\]|(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)|(?:[-a-z0-9._~!$&'()*+,;=]|%[0-9a-f]{2})*)(?::\d*)?(?:\/(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*|\/(?:(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})+(?:\/(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*)?|(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})+(?:\/(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*)?(?:\?(?:[-a-z0-9._~!$&'()*+,;=:@/?]|%[0-9a-f]{2})*)?(?:#(?:[-a-z0-9._~!$&'()*+,;=:@/?]|%[0-9a-f]{2})*)?$/i;
function IsUri(value) {
  return Uri.test(value);
}

// node_modules/typebox/build/format/url.mjs
function IsUrl(value) {
  return URL.canParse(value);
}

// node_modules/typebox/build/format/uuid.mjs
var Uuid = /^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i;
function IsUuid(value) {
  return Uuid.test(value);
}

// node_modules/typebox/build/format/_registry.mjs
var formats = /* @__PURE__ */ new Map();
function Clear() {
  formats.clear();
}
function Entries() {
  return [...formats.entries()];
}
function Set3(format, check) {
  formats.set(format, check);
}
function Has2(format) {
  return formats.has(format);
}
function Get3(format) {
  return formats.get(format);
}
function Test(format, value) {
  return formats.get(format)?.(value) ?? true;
}
function Reset() {
  Clear();
  formats.set("date-time", IsDateTime);
  formats.set("date", IsDate);
  formats.set("duration", IsDuration);
  formats.set("email", IsEmail);
  formats.set("hostname", IsHostname2);
  formats.set("idn-email", IsIdnEmail);
  formats.set("idn-hostname", IsIdnHostname2);
  formats.set("ipv4", IsIPv4);
  formats.set("ipv6", IsIPv6);
  formats.set("iri-reference", IsIriReference);
  formats.set("iri", IsIri);
  formats.set("json-pointer-uri-fragment", IsJsonPointerUriFragment);
  formats.set("json-pointer", IsJsonPointer);
  formats.set("regex", IsRegex);
  formats.set("relative-json-pointer", IsRelativeJsonPointer);
  formats.set("time", IsTime);
  formats.set("uri-reference", IsUriReference);
  formats.set("uri-template", IsUriTemplate);
  formats.set("uri", IsUri);
  formats.set("url", IsUrl);
  formats.set("uuid", IsUuid);
}
Reset();

// node_modules/typebox/build/schema/engine/format.mjs
function BuildFormat(_stack, _context, schema, value) {
  return emit_exports.Call(emit_exports.Member("Format", "Test"), [emit_exports.Constant(schema.format), value]);
}
function CheckFormat(_stack, _context, schema, value) {
  return format_exports.Test(schema.format, value);
}
function ErrorFormat(stack, context, schemaPath, instancePath, schema, value) {
  return CheckFormat(stack, context, schema, value) || context.AddError({
    keyword: "format",
    schemaPath,
    instancePath,
    params: { format: schema.format }
  });
}

// node_modules/typebox/build/schema/engine/if.mjs
function BuildIf(stack, context, schema, value) {
  const thenSchema = IsThen(schema) ? schema.then : true;
  const elseSchema = IsElse(schema) ? schema.else : true;
  return emit_exports.Ternary(BuildSchema(stack, context, schema.if, value), BuildSchema(stack, context, thenSchema, value), BuildSchema(stack, context, elseSchema, value));
}
function CheckIf(stack, context, schema, value) {
  const thenSchema = IsThen(schema) ? schema.then : true;
  const elseSchema = IsElse(schema) ? schema.else : true;
  return CheckSchema(stack, context, schema.if, value) ? CheckSchema(stack, context, thenSchema, value) : CheckSchema(stack, context, elseSchema, value);
}
function ErrorIf(stack, context, schemaPath, instancePath, schema, value) {
  const thenSchema = IsThen(schema) ? schema.then : true;
  const elseSchema = IsElse(schema) ? schema.else : true;
  const trueContext = new ErrorContext();
  const isIf = ErrorSchema(stack, trueContext, `${schemaPath}/if`, instancePath, schema.if, value) ? ErrorSchema(stack, trueContext, `${schemaPath}/then`, instancePath, thenSchema, value) || context.AddError({
    keyword: "if",
    schemaPath,
    instancePath,
    params: { failingKeyword: "then" }
  }) : ErrorSchema(stack, context, `${schemaPath}/else`, instancePath, elseSchema, value) || context.AddError({
    keyword: "if",
    schemaPath,
    instancePath,
    params: { failingKeyword: "else" }
  });
  if (isIf)
    context.Merge([trueContext]);
  return isIf;
}

// node_modules/typebox/build/schema/engine/items.mjs
function BuildItemsSizedStandard(stack, context, schema, value) {
  return emit_exports.ReduceAnd(schema.items.map((schema2, index3) => {
    const isLength = emit_exports.IsLessEqualThan(emit_exports.Member(value, "length"), emit_exports.Constant(index3));
    const isSchema = BuildSchemaPushStack(stack, context, schema2, `${value}[${index3}]`);
    const addIndex = context.AddIndex(emit_exports.Constant(index3));
    return emit_exports.Or(isLength, emit_exports.And(isSchema, addIndex));
  }));
}
function BuildItemsSizedFast(stack, context, schema, value) {
  return emit_exports.ReduceAnd(schema.items.map((schema2, index3) => {
    const isLength = emit_exports.IsLessEqualThan(emit_exports.Member(value, "length"), emit_exports.Constant(index3));
    const isSchema = BuildSchemaPushStack(stack, context, schema2, `${value}[${index3}]`);
    return emit_exports.Or(isLength, isSchema);
  }));
}
function BuildItemsSized(stack, context, schema, value) {
  return context.UseUnevaluated() ? BuildItemsSizedStandard(stack, context, schema, value) : BuildItemsSizedFast(stack, context, schema, value);
}
function CheckItemsSized(stack, context, schema, value) {
  return guard_exports.Every(schema.items, 0, (schema2, index3) => {
    return guard_exports.IsLessEqualThan(value.length, index3) || CheckSchemaPushStack(stack, context, schema2, value[index3]) && context.AddIndex(index3);
  });
}
function ErrorItemsSized(stack, context, schemaPath, instancePath, schema, value) {
  return guard_exports.EveryAll(schema.items, 0, (schema2, index3) => {
    const nextSchemaPath = `${schemaPath}/items/${index3}`;
    const nextInstancePath = `${instancePath}/${index3}`;
    return guard_exports.IsLessEqualThan(value.length, index3) || ErrorSchemaPushStack(stack, context, nextSchemaPath, nextInstancePath, schema2, value[index3]) && context.AddIndex(index3);
  });
}
function BuildItemsUnsizedStandard(stack, context, schema, value) {
  const offset = IsPrefixItems(schema) ? schema.prefixItems.length : 0;
  const isSchema = BuildSchemaPushStack(stack, context, schema.items, "element");
  const addIndex = context.AddIndex("index");
  return emit_exports.Every(value, emit_exports.Constant(offset), ["element", "index"], emit_exports.And(isSchema, addIndex));
}
function BuildItemsUnsizedFast(stack, context, schema, value) {
  const offset = IsPrefixItems(schema) ? schema.prefixItems.length : 0;
  const isSchema = BuildSchemaPushStack(stack, context, schema.items, "element");
  return emit_exports.Every(value, emit_exports.Constant(offset), ["element", "index"], isSchema);
}
function BuildItemsUnsized(stack, context, schema, value) {
  return context.UseUnevaluated() ? BuildItemsUnsizedStandard(stack, context, schema, value) : BuildItemsUnsizedFast(stack, context, schema, value);
}
function CheckItemsUnsized(stack, context, schema, value) {
  const offset = IsPrefixItems(schema) ? schema.prefixItems.length : 0;
  return guard_exports.Every(value, offset, (element, index3) => {
    return CheckSchemaPushStack(stack, context, schema.items, element) && context.AddIndex(index3);
  });
}
function ErrorItemsUnsized(stack, context, schemaPath, instancePath, schema, value) {
  const offset = IsPrefixItems(schema) ? schema.prefixItems.length : 0;
  return guard_exports.EveryAll(value, offset, (element, index3) => {
    const nextSchemaPath = `${schemaPath}/items`;
    const nextInstancePath = `${instancePath}/${index3}`;
    return ErrorSchemaPushStack(stack, context, nextSchemaPath, nextInstancePath, schema.items, element) && context.AddIndex(index3);
  });
}
function BuildItems(stack, context, schema, value) {
  return IsItemsSized(schema) ? BuildItemsSized(stack, context, schema, value) : BuildItemsUnsized(stack, context, schema, value);
}
function CheckItems(stack, context, schema, value) {
  return IsItemsSized(schema) ? CheckItemsSized(stack, context, schema, value) : CheckItemsUnsized(stack, context, schema, value);
}
function ErrorItems(stack, context, schemaPath, instancePath, schema, value) {
  return IsItemsSized(schema) ? ErrorItemsSized(stack, context, schemaPath, instancePath, schema, value) : ErrorItemsUnsized(stack, context, schemaPath, instancePath, schema, value);
}

// node_modules/typebox/build/schema/engine/maxContains.mjs
function IsValid3(schema) {
  return IsContains(schema);
}
function BuildMaxContains(stack, context, schema, value) {
  if (!IsValid3(schema))
    return emit_exports.Constant(true);
  const [item] = [Unique()];
  const count = emit_exports.Counted(value, [item, "_"], BuildSchema(stack, context, schema.contains, item));
  return emit_exports.IsLessEqualThan(count, emit_exports.Constant(schema.maxContains));
}
function CheckMaxContains(stack, context, schema, value) {
  if (!IsValid3(schema))
    return true;
  const count = guard_exports.Counted(value, (item) => CheckSchema(stack, context, schema.contains, item));
  return guard_exports.IsLessEqualThan(count, schema.maxContains);
}
function ErrorMaxContains(stack, context, schemaPath, instancePath, schema, value) {
  const minContains = IsMinContains(schema) ? schema.minContains : 1;
  return CheckMaxContains(stack, context, schema, value) || context.AddError({
    keyword: "contains",
    schemaPath,
    instancePath,
    params: { minContains, maxContains: schema.maxContains }
  });
}

// node_modules/typebox/build/schema/engine/maximum.mjs
function BuildMaximum(_stack, _context, schema, value) {
  return emit_exports.IsLessEqualThan(value, emit_exports.Constant(schema.maximum));
}
function CheckMaximum(_stack, _context, schema, value) {
  return guard_exports.IsLessEqualThan(value, schema.maximum);
}
function ErrorMaximum(stack, context, schemaPath, instancePath, schema, value) {
  return CheckMaximum(stack, context, schema, value) || context.AddError({
    keyword: "maximum",
    schemaPath,
    instancePath,
    params: { comparison: "<=", limit: schema.maximum }
  });
}

// node_modules/typebox/build/schema/engine/maxItems.mjs
function BuildMaxItems(_stack, _context, schema, value) {
  return emit_exports.IsLessEqualThan(emit_exports.Member(value, "length"), emit_exports.Constant(schema.maxItems));
}
function CheckMaxItems(_stack, _context, schema, value) {
  return guard_exports.IsLessEqualThan(value.length, schema.maxItems);
}
function ErrorMaxItems(stack, context, schemaPath, instancePath, schema, value) {
  return CheckMaxItems(stack, context, schema, value) || context.AddError({
    keyword: "maxItems",
    schemaPath,
    instancePath,
    params: { limit: schema.maxItems }
  });
}

// node_modules/typebox/build/schema/engine/maxLength.mjs
function BuildMaxLength(_stack, _context, schema, value) {
  return emit_exports.IsMaxLength(value, emit_exports.Constant(schema.maxLength));
}
function CheckMaxLength(_stack, _context, schema, value) {
  return guard_exports.IsMaxLength(value, schema.maxLength);
}
function ErrorMaxLength(stack, context, schemaPath, instancePath, schema, value) {
  return CheckMaxLength(stack, context, schema, value) || context.AddError({
    keyword: "maxLength",
    schemaPath,
    instancePath,
    params: { limit: schema.maxLength }
  });
}

// node_modules/typebox/build/schema/engine/maxProperties.mjs
function BuildMaxProperties(_stack, _context, schema, value) {
  return emit_exports.IsLessEqualThan(emit_exports.Member(emit_exports.Keys(value), "length"), emit_exports.Constant(schema.maxProperties));
}
function CheckMaxProperties(_stack, _context, schema, value) {
  return guard_exports.IsLessEqualThan(guard_exports.Keys(value).length, schema.maxProperties);
}
function ErrorMaxProperties(stack, context, schemaPath, instancePath, schema, value) {
  return CheckMaxProperties(stack, context, schema, value) || context.AddError({
    keyword: "maxProperties",
    schemaPath,
    instancePath,
    params: { limit: schema.maxProperties }
  });
}

// node_modules/typebox/build/schema/engine/minContains.mjs
function IsValid4(schema) {
  return IsContains(schema);
}
function BuildMinContainsStandard(stack, context, schema, value) {
  const [item, index3] = [Unique(), Unique()];
  const count = emit_exports.Counted(value, [item, index3], emit_exports.And(BuildSchema(stack, context, schema.contains, item), context.AddIndex(index3)));
  return emit_exports.IsGreaterEqualThan(count, emit_exports.Constant(schema.minContains));
}
function BuildMinContainsFast(stack, context, schema, value) {
  const [item] = [Unique()];
  const count = emit_exports.Counted(value, [item, "_"], BuildSchema(stack, context, schema.contains, item));
  return emit_exports.IsGreaterEqualThan(count, emit_exports.Constant(schema.minContains));
}
function BuildMinContains(stack, context, schema, value) {
  if (!IsValid4(schema))
    return emit_exports.Constant(true);
  return context.UseUnevaluated() ? BuildMinContainsStandard(stack, context, schema, value) : BuildMinContainsFast(stack, context, schema, value);
}
function CheckMinContains(stack, context, schema, value) {
  if (!IsValid4(schema))
    return true;
  const count = guard_exports.Counted(value, (item, index3) => CheckSchema(stack, context, schema.contains, item) && context.AddIndex(index3));
  return guard_exports.IsGreaterEqualThan(count, schema.minContains);
}
function ErrorMinContains(stack, context, schemaPath, instancePath, schema, value) {
  return CheckMinContains(stack, context, schema, value) || context.AddError({
    keyword: "contains",
    schemaPath,
    instancePath,
    params: { minContains: schema.minContains }
  });
}

// node_modules/typebox/build/schema/engine/minimum.mjs
function BuildMinimum(_stack, _context, schema, value) {
  return emit_exports.IsGreaterEqualThan(value, emit_exports.Constant(schema.minimum));
}
function CheckMinimum(_stack, _context, schema, value) {
  return guard_exports.IsGreaterEqualThan(value, schema.minimum);
}
function ErrorMinimum(stack, context, schemaPath, instancePath, schema, value) {
  return CheckMinimum(stack, context, schema, value) || context.AddError({
    keyword: "minimum",
    schemaPath,
    instancePath,
    params: { comparison: ">=", limit: schema.minimum }
  });
}

// node_modules/typebox/build/schema/engine/minItems.mjs
function BuildMinItems(_stack, _context, schema, value) {
  return emit_exports.IsGreaterEqualThan(emit_exports.Member(value, "length"), emit_exports.Constant(schema.minItems));
}
function CheckMinItems(_stack, _context, schema, value) {
  return guard_exports.IsGreaterEqualThan(value.length, schema.minItems);
}
function ErrorMinItems(stack, context, schemaPath, instancePath, schema, value) {
  return CheckMinItems(stack, context, schema, value) || context.AddError({
    keyword: "minItems",
    schemaPath,
    instancePath,
    params: { limit: schema.minItems }
  });
}

// node_modules/typebox/build/schema/engine/minLength.mjs
function BuildMinLength(_stack, _context, schema, value) {
  return emit_exports.IsMinLength(value, emit_exports.Constant(schema.minLength));
}
function CheckMinLength(_stack, _context, schema, value) {
  return guard_exports.IsMinLength(value, schema.minLength);
}
function ErrorMinLength(stack, context, schemaPath, instancePath, schema, value) {
  return CheckMinLength(stack, context, schema, value) || context.AddError({
    keyword: "minLength",
    schemaPath,
    instancePath,
    params: { limit: schema.minLength }
  });
}

// node_modules/typebox/build/schema/engine/minProperties.mjs
function BuildMinProperties(_stack, _context, schema, value) {
  return emit_exports.IsGreaterEqualThan(emit_exports.Member(emit_exports.Keys(value), "length"), emit_exports.Constant(schema.minProperties));
}
function CheckMinProperties(_stack, _context, schema, value) {
  return guard_exports.IsGreaterEqualThan(guard_exports.Keys(value).length, schema.minProperties);
}
function ErrorMinProperties(stack, context, schemaPath, instancePath, schema, value) {
  return CheckMinProperties(stack, context, schema, value) || context.AddError({
    keyword: "minProperties",
    schemaPath,
    instancePath,
    params: { limit: schema.minProperties }
  });
}

// node_modules/typebox/build/schema/engine/multipleOf.mjs
function BuildMultipleOf(_stack, _context, schema, value) {
  return emit_exports.MultipleOf(value, emit_exports.Constant(schema.multipleOf));
}
function CheckMultipleOf(_stack, _context, schema, value) {
  return guard_exports.IsMultipleOf(value, schema.multipleOf);
}
function ErrorMultipleOf(stack, context, schemaPath, instancePath, schema, value) {
  return CheckMultipleOf(stack, context, schema, value) || context.AddError({
    keyword: "multipleOf",
    schemaPath,
    instancePath,
    params: { multipleOf: schema.multipleOf }
  });
}

// node_modules/typebox/build/schema/engine/not.mjs
function BuildNotStandard(stack, context, schema, value) {
  return Reducer(stack, context, [schema.not], value, emit_exports.Not(emit_exports.IsEqual(emit_exports.Member("results", "length"), emit_exports.Constant(1))));
}
function BuildNotFast(stack, context, schema, value) {
  return emit_exports.Not(BuildSchema(stack, context, schema.not, value));
}
function BuildNot(stack, context, schema, value) {
  return context.UseUnevaluated() ? BuildNotStandard(stack, context, schema, value) : BuildNotFast(stack, context, schema, value);
}
function CheckNot(stack, context, schema, value) {
  const nextContext = new CheckContext();
  const isSchema = !CheckSchema(stack, nextContext, schema.not, value);
  const isNot = isSchema && context.Merge([nextContext]);
  return isNot;
}
function ErrorNot(stack, context, schemaPath, instancePath, schema, value) {
  return CheckNot(stack, context, schema, value) || context.AddError({
    keyword: "not",
    schemaPath,
    instancePath,
    params: {}
  });
}

// node_modules/typebox/build/schema/engine/oneOf.mjs
function BuildOneOfStandard(stack, context, schema, value) {
  return Reducer(stack, context, schema.oneOf, value, emit_exports.IsEqual(emit_exports.Member("results", "length"), emit_exports.Constant(1)));
}
function BuildOneOfFast(stack, context, schema, value) {
  const [result] = [Unique()];
  const results = emit_exports.ArrayLiteral(schema.oneOf.map((schema2) => BuildSchema(stack, context, schema2, value)));
  const count = emit_exports.Counted(results, [result, "_"], emit_exports.IsEqual(result, emit_exports.Constant(true)));
  return emit_exports.IsEqual(count, emit_exports.Constant(1));
}
function BuildOneOf(stack, context, schema, value) {
  return context.UseUnevaluated() ? BuildOneOfStandard(stack, context, schema, value) : BuildOneOfFast(stack, context, schema, value);
}
function CheckOneOf(stack, context, schema, value) {
  const passedContexts = schema.oneOf.reduce((result, schema2) => {
    const nextContext = new CheckContext();
    return CheckSchema(stack, nextContext, schema2, value) ? [...result, nextContext] : result;
  }, []);
  return guard_exports.IsEqual(passedContexts.length, 1) && context.Merge(passedContexts);
}
function ErrorOneOf(stack, context, schemaPath, instancePath, schema, value) {
  const failedContexts = [];
  const passingSchemas = [];
  const passedContexts = schema.oneOf.reduce((result, schema2, index3) => {
    const nextContext = new ErrorContext();
    const nextSchemaPath = `${schemaPath}/oneOf/${index3}`;
    const isSchema = ErrorSchema(stack, nextContext, nextSchemaPath, instancePath, schema2, value);
    if (isSchema)
      passingSchemas.push(index3);
    if (!isSchema)
      failedContexts.push(nextContext);
    return isSchema ? [...result, nextContext] : result;
  }, []);
  const isOneOf = guard_exports.IsEqual(passedContexts.length, 1) && context.Merge(passedContexts);
  if (!isOneOf && guard_exports.IsEqual(passingSchemas.length, 0))
    failedContexts.forEach((failed) => failed.GetErrors().forEach((error) => context.AddError(error)));
  return isOneOf || context.AddError({
    keyword: "oneOf",
    schemaPath,
    instancePath,
    params: { passingSchemas }
  });
}

// node_modules/typebox/build/schema/engine/pattern.mjs
function BuildPattern(_stack, _context, schema, value) {
  const regexp = CreateVariable(guard_exports.IsString(schema.pattern) ? UnicodeRegExp(schema.pattern) : schema.pattern);
  return emit_exports.Call(emit_exports.Member(regexp, "test"), [value]);
}
function CheckPattern(_stack, _context, schema, value) {
  const regexp = guard_exports.IsString(schema.pattern) ? UnicodeRegExp(schema.pattern) : schema.pattern;
  return regexp.test(value);
}
function ErrorPattern(stack, context, schemaPath, instancePath, schema, value) {
  return CheckPattern(stack, context, schema, value) || context.AddError({
    keyword: "pattern",
    schemaPath,
    instancePath,
    params: { pattern: schema.pattern }
  });
}

// node_modules/typebox/build/schema/engine/patternProperties.mjs
function BuildPatternProperties(stack, context, schema, value) {
  return emit_exports.ReduceAnd(guard_exports.Entries(schema.patternProperties).map(([pattern, schema2]) => {
    const [key, prop] = [Unique(), Unique()];
    const regexp = CreateVariable(UnicodeRegExp(pattern));
    const notKey = emit_exports.Not(emit_exports.Call(emit_exports.Member(regexp, "test"), [key]));
    const isSchema = BuildSchemaPushStack(stack, context, schema2, prop);
    const addKey = context.AddKey(key);
    const guarded = context.UseUnevaluated() ? emit_exports.Or(notKey, emit_exports.And(isSchema, addKey)) : emit_exports.Or(notKey, isSchema);
    return emit_exports.Every(emit_exports.Entries(value), emit_exports.Constant(0), [`[${key}, ${prop}]`, "_"], guarded);
  }));
}
function CheckPatternProperties(stack, context, schema, value) {
  return guard_exports.Every(guard_exports.Entries(schema.patternProperties), 0, ([pattern, schema2]) => {
    const regexp = UnicodeRegExp(pattern);
    return guard_exports.Every(guard_exports.Entries(value), 0, ([key, prop]) => {
      return !regexp.test(key) || CheckSchemaPushStack(stack, context, schema2, prop) && context.AddKey(key);
    });
  });
}
function ErrorPatternProperties(stack, context, schemaPath, instancePath, schema, value) {
  return guard_exports.EveryAll(guard_exports.Entries(schema.patternProperties), 0, ([pattern, schema2]) => {
    const nextSchemaPath = `${schemaPath}/patternProperties/${pattern}`;
    const regexp = UnicodeRegExp(pattern);
    return guard_exports.EveryAll(guard_exports.Entries(value), 0, ([key, value2]) => {
      const nextInstancePath = `${instancePath}/${key}`;
      const notKey = !regexp.test(key);
      return notKey || ErrorSchemaPushStack(stack, context, nextSchemaPath, nextInstancePath, schema2, value2) && context.AddKey(key);
    });
  });
}

// node_modules/typebox/build/schema/engine/prefixItems.mjs
function BuildPrefixItems(stack, context, schema, value) {
  return emit_exports.ReduceAnd(schema.prefixItems.map((schema2, index3) => {
    const isLength = emit_exports.IsLessEqualThan(emit_exports.Member(value, "length"), emit_exports.Constant(index3));
    const isSchema = BuildSchemaPushStack(stack, context, schema2, `${value}[${index3}]`);
    const addIndex = context.AddIndex(emit_exports.Constant(index3));
    const guarded = context.UseUnevaluated() ? emit_exports.And(isSchema, addIndex) : isSchema;
    return emit_exports.Or(isLength, guarded);
  }));
}
function CheckPrefixItems(stack, context, schema, value) {
  return guard_exports.IsEqual(value.length, 0) || guard_exports.Every(schema.prefixItems, 0, (schema2, index3) => {
    return guard_exports.IsLessEqualThan(value.length, index3) || CheckSchemaPushStack(stack, context, schema2, value[index3]) && context.AddIndex(index3);
  });
}
function ErrorPrefixItems(stack, context, schemaPath, instancePath, schema, value) {
  return guard_exports.IsEqual(value.length, 0) || guard_exports.EveryAll(schema.prefixItems, 0, (schema2, index3) => {
    const nextSchemaPath = `${schemaPath}/prefixItems/${index3}`;
    const nextInstancePath = `${instancePath}/${index3}`;
    return guard_exports.IsLessEqualThan(value.length, index3) || ErrorSchemaPushStack(stack, context, nextSchemaPath, nextInstancePath, schema2, value[index3]) && context.AddIndex(index3);
  });
}

// node_modules/typebox/build/schema/engine/_exact_optional.mjs
function IsExactOptional(required, key) {
  return required.includes(key) || settings_exports.Get().exactOptionalPropertyTypes;
}
function InexactOptionalBuild(value, key) {
  return emit_exports.IsUndefined(emit_exports.Member(value, key));
}
function InexactOptionalCheck(value, key) {
  return guard_exports.IsUndefined(value[key]);
}

// node_modules/typebox/build/schema/engine/properties.mjs
function BuildProperties(stack, context, schema, value) {
  const required = IsRequired(schema) ? schema.required : [];
  const everyKey = guard_exports.Entries(schema.properties).map(([key, schema2]) => {
    const notKey = emit_exports.Not(emit_exports.HasPropertyKey(value, emit_exports.Constant(key)));
    const isSchema = BuildSchemaPushStack(stack, context, schema2, emit_exports.Member(value, key));
    const addKey = context.AddKey(emit_exports.Constant(key));
    const guarded = context.UseUnevaluated() ? emit_exports.And(isSchema, addKey) : isSchema;
    const isProperty = required.includes(key) ? guarded : emit_exports.Or(notKey, guarded);
    return IsExactOptional(required, key) ? isProperty : emit_exports.Or(InexactOptionalBuild(value, key), isProperty);
  });
  return emit_exports.ReduceAnd(everyKey);
}
function CheckProperties(stack, context, schema, value) {
  const required = IsRequired(schema) ? schema.required : [];
  const isProperties = guard_exports.Every(guard_exports.Entries(schema.properties), 0, ([key, schema2]) => {
    const isProperty = !guard_exports.HasPropertyKey(value, key) || CheckSchemaPushStack(stack, context, schema2, value[key]) && context.AddKey(key);
    return IsExactOptional(required, key) ? isProperty : InexactOptionalCheck(value, key) || isProperty;
  });
  return isProperties;
}
function ErrorProperties(stack, context, schemaPath, instancePath, schema, value) {
  const required = IsRequired(schema) ? schema.required : [];
  const isProperties = guard_exports.EveryAll(guard_exports.Entries(schema.properties), 0, ([key, schema2]) => {
    const nextSchemaPath = `${schemaPath}/properties/${key}`;
    const nextInstancePath = `${instancePath}/${key}`;
    const isProperty = () => !guard_exports.HasPropertyKey(value, key) || ErrorSchemaPushStack(stack, context, nextSchemaPath, nextInstancePath, schema2, value[key]) && context.AddKey(key);
    return IsExactOptional(required, key) ? isProperty() : InexactOptionalCheck(value, key) || isProperty();
  });
  return isProperties;
}

// node_modules/typebox/build/schema/engine/propertyNames.mjs
function BuildPropertyNames(stack, context, schema, value) {
  const [key, _index] = [Unique(), Unique()];
  return emit_exports.Every(emit_exports.Keys(value), emit_exports.Constant(0), [key, _index], BuildSchema(stack, context, schema.propertyNames, key));
}
function CheckPropertyNames(stack, context, schema, value) {
  return guard_exports.Every(guard_exports.Keys(value), 0, (key, _index) => CheckSchema(stack, context, schema.propertyNames, key));
}
function ErrorPropertyNames(stack, context, schemaPath, instancePath, schema, value) {
  const propertyNames = [];
  const isPropertyNames = guard_exports.EveryAll(guard_exports.Keys(value), 0, (key, _index) => {
    const nextInstancePath = `${instancePath}/${key}`;
    const nextSchemaPath = `${schemaPath}/propertyNames`;
    const isPropertyName = ErrorSchema(stack, context, nextSchemaPath, nextInstancePath, schema.propertyNames, key);
    if (!isPropertyName)
      propertyNames.push(key);
    return isPropertyName;
  });
  return isPropertyNames || context.AddError({
    keyword: "propertyNames",
    schemaPath,
    instancePath,
    params: { propertyNames }
  });
}

// node_modules/typebox/build/schema/engine/recursiveRef.mjs
function BuildRecursiveRef(stack, context, schema, value) {
  const target = stack.RecursiveRef(schema) ?? false;
  return CreateFunction(stack, context, target, value);
}
function CheckRecursiveRef(stack, context, schema, value) {
  const target = stack.RecursiveRef(schema) ?? false;
  return IsSchema2(target) && CheckSchema(stack, context, target, value);
}
function ErrorRecursiveRef(stack, context, _schemaPath, instancePath, schema, value) {
  const target = stack.RecursiveRef(schema) ?? false;
  return IsSchema2(target) && ErrorSchema(stack, context, "#", instancePath, target, value);
}

// node_modules/typebox/build/schema/engine/ref.mjs
function BuildRefStandard(stack, context, target, value) {
  const interior = emit_exports.ArrowFunction(["context", "value"], CreateFunction(stack, context, target, "value"));
  const exterior = emit_exports.ArrowFunction(["context", "value"], emit_exports.Statements([
    emit_exports.ConstDeclaration("nextContext", emit_exports.New("CheckContext", [])),
    emit_exports.ConstDeclaration("result", emit_exports.Call(interior, ["nextContext", "value"])),
    emit_exports.If("result", context.Merge("[nextContext]")),
    emit_exports.Return("result")
  ]));
  return emit_exports.Call(exterior, ["context", value]);
}
function BuildRefFast(stack, context, target, value) {
  return CreateFunction(stack, context, target, value);
}
function BuildRef(stack, context, schema, value) {
  const target = stack.Ref(schema) ?? false;
  return context.UseUnevaluated() ? BuildRefStandard(stack, context, target, value) : BuildRefFast(stack, context, target, value);
}
function CheckRef(stack, context, schema, value) {
  const target = stack.Ref(schema) ?? false;
  const nextContext = new CheckContext();
  const result = IsSchema2(target) && CheckSchema(stack, nextContext, target, value);
  if (result)
    context.Merge([nextContext]);
  return result;
}
function ErrorRef(stack, context, _schemaPath, instancePath, schema, value) {
  const target = stack.Ref(schema) ?? false;
  const nextContext = new ErrorContext();
  const result = IsSchema2(target) && ErrorSchema(stack, nextContext, "#", instancePath, target, value);
  if (result)
    context.Merge([nextContext]);
  if (!result)
    nextContext.GetErrors().forEach((error) => context.AddError(error));
  return result;
}

// node_modules/typebox/build/schema/engine/required.mjs
function BuildRequired(_stack, _context, schema, value) {
  return emit_exports.ReduceAnd(schema.required.map((key) => emit_exports.HasPropertyKey(value, emit_exports.Constant(key))));
}
function CheckRequired(_stack, _context, schema, value) {
  return guard_exports.Every(schema.required, 0, (key) => guard_exports.HasPropertyKey(value, key));
}
function ErrorRequired(_stack, context, schemaPath, instancePath, schema, value) {
  const requiredProperties = [];
  const isRequired = guard_exports.EveryAll(schema.required, 0, (key) => {
    const hasKey = guard_exports.HasPropertyKey(value, key);
    if (!hasKey)
      requiredProperties.push(key);
    return hasKey;
  });
  return isRequired || context.AddError({
    keyword: "required",
    schemaPath,
    instancePath,
    params: { requiredProperties }
  });
}

// node_modules/typebox/build/schema/engine/type.mjs
function BuildTypeName(_stack, _context, type, value) {
  return (
    // jsonschema
    guard_exports.IsEqual(type, "object") ? emit_exports.IsObjectNotArray(value) : guard_exports.IsEqual(type, "array") ? emit_exports.IsArray(value) : guard_exports.IsEqual(type, "boolean") ? emit_exports.IsBoolean(value) : guard_exports.IsEqual(type, "integer") ? emit_exports.IsInteger(value) : guard_exports.IsEqual(type, "number") ? emit_exports.IsNumber(value) : guard_exports.IsEqual(type, "null") ? emit_exports.IsNull(value) : guard_exports.IsEqual(type, "string") ? emit_exports.IsString(value) : (
      // xschema
      guard_exports.IsEqual(type, "bigint") ? emit_exports.IsBigInt(value) : guard_exports.IsEqual(type, "constructor") ? emit_exports.IsConstructor(value) : guard_exports.IsEqual(type, "function") ? emit_exports.IsFunction(value) : guard_exports.IsEqual(type, "symbol") ? emit_exports.IsSymbol(value) : guard_exports.IsEqual(type, "undefined") ? emit_exports.IsUndefined(value) : guard_exports.IsEqual(type, "void") ? emit_exports.IsUndefined(value) : emit_exports.Constant(true)
    )
  );
}
function CheckTypeName(_stack, _context, type, _schema, value) {
  return (
    // jsonschema
    guard_exports.IsEqual(type, "object") ? guard_exports.IsObjectNotArray(value) : guard_exports.IsEqual(type, "array") ? guard_exports.IsArray(value) : guard_exports.IsEqual(type, "boolean") ? guard_exports.IsBoolean(value) : guard_exports.IsEqual(type, "integer") ? guard_exports.IsInteger(value) : guard_exports.IsEqual(type, "number") ? guard_exports.IsNumber(value) : guard_exports.IsEqual(type, "null") ? guard_exports.IsNull(value) : guard_exports.IsEqual(type, "string") ? guard_exports.IsString(value) : (
      // xschema
      guard_exports.IsEqual(type, "bigint") ? guard_exports.IsBigInt(value) : guard_exports.IsEqual(type, "constructor") ? guard_exports.IsConstructor(value) : guard_exports.IsEqual(type, "function") ? guard_exports.IsFunction(value) : guard_exports.IsEqual(type, "symbol") ? guard_exports.IsSymbol(value) : guard_exports.IsEqual(type, "undefined") ? guard_exports.IsUndefined(value) : guard_exports.IsEqual(type, "void") ? guard_exports.IsUndefined(value) : true
    )
  );
}
function BuildTypeNames(stack, context, typenames, value) {
  return emit_exports.ReduceOr(typenames.map((type) => BuildTypeName(stack, context, type, value)));
}
function CheckTypeNames(stack, context, types, schema, value) {
  return guard_exports.Some(types, (type) => CheckTypeName(stack, context, type, schema, value));
}
function BuildType(stack, context, schema, value) {
  return guard_exports.IsArray(schema.type) ? BuildTypeNames(stack, context, schema.type, value) : BuildTypeName(stack, context, schema.type, value);
}
function CheckType(stack, context, schema, value) {
  return guard_exports.IsArray(schema.type) ? CheckTypeNames(stack, context, schema.type, schema, value) : CheckTypeName(stack, context, schema.type, schema, value);
}
function ErrorType(stack, context, schemaPath, instancePath, schema, value) {
  const isType = guard_exports.IsArray(schema.type) ? CheckTypeNames(stack, context, schema.type, schema, value) : CheckTypeName(stack, context, schema.type, schema, value);
  return isType || context.AddError({
    keyword: "type",
    schemaPath,
    instancePath,
    params: { type: schema.type }
  });
}

// node_modules/typebox/build/schema/engine/unevaluatedItems.mjs
function BuildUnevaluatedItems(stack, context, schema, value) {
  const [index3, item] = [Unique(), Unique()];
  const indices = emit_exports.Call(emit_exports.Member("context", "GetIndices"), []);
  const hasIndex = emit_exports.Call(emit_exports.Member("indices", "has"), [index3]);
  const isSchema = BuildSchema(stack, context, schema.unevaluatedItems, item);
  const addIndex = emit_exports.Call(emit_exports.Member("context", "AddIndex"), [index3]);
  const isEvery = emit_exports.Every(value, emit_exports.Constant(0), [item, index3], emit_exports.And(emit_exports.Or(hasIndex, isSchema), addIndex));
  return emit_exports.Call(emit_exports.ArrowFunction(["context"], emit_exports.Statements([
    emit_exports.ConstDeclaration("indices", indices),
    emit_exports.Return(isEvery)
  ])), ["context"]);
}
function CheckUnevaluatedItems(stack, context, schema, value) {
  const indices = context.GetIndices();
  return guard_exports.Every(value, 0, (item, index3) => {
    return (indices.has(index3) || CheckSchema(stack, context, schema.unevaluatedItems, item)) && context.AddIndex(index3);
  });
}
function ErrorUnevaluatedItems(stack, context, schemaPath, instancePath, schema, value) {
  const indices = context.GetIndices();
  const unevaluatedItems = [];
  const isUnevaluatedItems = guard_exports.EveryAll(value, 0, (item, index3) => {
    const nextContext = new ErrorContext();
    const isEvaluatedItem = (indices.has(index3) || ErrorSchema(stack, nextContext, schemaPath, instancePath, schema.unevaluatedItems, item)) && context.AddIndex(index3);
    if (!isEvaluatedItem)
      unevaluatedItems.push(index3);
    return isEvaluatedItem;
  });
  return isUnevaluatedItems || context.AddError({
    keyword: "unevaluatedItems",
    schemaPath,
    instancePath,
    params: { unevaluatedItems }
  });
}

// node_modules/typebox/build/schema/engine/unevaluatedProperties.mjs
function BuildUnevaluatedProperties(stack, context, schema, value) {
  const [key, prop] = [Unique(), Unique()];
  const keys = emit_exports.Call(emit_exports.Member("context", "GetKeys"), []);
  const hasKey = emit_exports.Call(emit_exports.Member("keys", "has"), [key]);
  const addKey = emit_exports.Call(emit_exports.Member("context", "AddKey"), [key]);
  const isSchema = BuildSchema(stack, context, schema.unevaluatedProperties, prop);
  const isEvery = emit_exports.Every(emit_exports.Entries(value), emit_exports.Constant(0), [`[${key}, ${prop}]`, "_"], emit_exports.Or(hasKey, emit_exports.And(isSchema, addKey)));
  return emit_exports.Call(emit_exports.ArrowFunction(["context"], emit_exports.Statements([
    emit_exports.ConstDeclaration("keys", keys),
    emit_exports.Return(isEvery)
  ])), ["context"]);
}
function CheckUnevaluatedProperties(stack, context, schema, value) {
  const keys = context.GetKeys();
  return guard_exports.Every(guard_exports.Entries(value), 0, ([key, prop]) => {
    return keys.has(key) || CheckSchema(stack, context, schema.unevaluatedProperties, prop) && context.AddKey(key);
  });
}
function ErrorUnevaluatedProperties(stack, context, schemaPath, instancePath, schema, value) {
  const keys = context.GetKeys();
  const unevaluatedProperties = [];
  const isUnevaluatedProperties = guard_exports.EveryAll(guard_exports.Entries(value), 0, ([key, prop]) => {
    const nextContext = new ErrorContext();
    const isEvaluatedProperty = keys.has(key) || ErrorSchema(stack, nextContext, schemaPath, instancePath, schema.unevaluatedProperties, prop) && context.AddKey(key);
    if (!isEvaluatedProperty)
      unevaluatedProperties.push(key);
    return isEvaluatedProperty;
  });
  return isUnevaluatedProperties || context.AddError({
    keyword: "unevaluatedProperties",
    schemaPath,
    instancePath,
    params: { unevaluatedProperties }
  });
}

// node_modules/typebox/build/schema/engine/uniqueItems.mjs
function IsValid5(schema) {
  return !guard_exports.IsEqual(schema.uniqueItems, false);
}
function BuildUniqueItems(_stack, _context, schema, value) {
  if (!IsValid5(schema))
    return emit_exports.Constant(true);
  const set = emit_exports.Member(emit_exports.New("Set", [emit_exports.Call(emit_exports.Member(value, "map"), [emit_exports.Member("Hashing", "Hash")])]), "size");
  const isLength = emit_exports.Member(value, "length");
  return emit_exports.IsEqual(set, isLength);
}
function CheckUniqueItems(_stack, _context, schema, value) {
  if (!IsValid5(schema))
    return true;
  const set = new Set(value.map(hash_exports.Hash)).size;
  const isLength = value.length;
  return guard_exports.IsEqual(set, isLength);
}
function ErrorUniqueItems(_stack, context, schemaPath, instancePath, schema, value) {
  if (!IsValid5(schema))
    return true;
  const set = /* @__PURE__ */ new Set();
  const duplicateItems = value.reduce((result, value2, index3) => {
    const hash = hash_exports.Hash(value2);
    if (set.has(hash))
      return [...result, index3];
    set.add(hash);
    return result;
  }, []);
  const isUniqueItems = guard_exports.IsEqual(duplicateItems.length, 0);
  return isUniqueItems || context.AddError({
    keyword: "uniqueItems",
    schemaPath,
    instancePath,
    params: { duplicateItems }
  });
}

// node_modules/typebox/build/schema/engine/schema.mjs
function HasTypeName(schema, typename) {
  return IsType(schema) && (guard_exports.IsArray(schema.type) && guard_exports.IsGreaterThan(schema.type.length, 0) && guard_exports.Every(schema.type, 0, (type) => guard_exports.IsEqual(type, typename)) || guard_exports.IsEqual(schema.type, typename));
}
function HasObjectType(schema) {
  return HasTypeName(schema, "object");
}
function HasObjectKeywords(schema) {
  return IsSchemaObject(schema) && (IsAdditionalProperties(schema) || IsDependencies(schema) || IsDependentRequired(schema) || IsDependentSchemas(schema) || IsProperties(schema) || IsPatternProperties(schema) || IsPropertyNames(schema) || IsMinProperties(schema) || IsMaxProperties(schema) || IsRequired(schema) || IsUnevaluatedProperties(schema));
}
function HasArrayType(schema) {
  return HasTypeName(schema, "array");
}
function HasArrayKeywords(schema) {
  return IsSchemaObject(schema) && (IsAdditionalItems(schema) || IsItems(schema) || IsContains(schema) || IsMaxContains(schema) || IsMaxItems(schema) || IsMinContains(schema) || IsMinItems(schema) || IsPrefixItems(schema) || IsUnevaluatedItems(schema) || IsUniqueItems(schema));
}
function HasStringType(schema) {
  return HasTypeName(schema, "string");
}
function HasStringKeywords(schema) {
  return IsSchemaObject(schema) && (IsMinLength(schema) || IsMaxLength(schema) || IsFormat(schema) || IsPattern(schema));
}
function HasNumberType(schema) {
  return HasTypeName(schema, "number") || HasTypeName(schema, "bigint");
}
function HasNumberKeywords(schema) {
  return IsSchemaObject(schema) && (IsMinimum(schema) || IsMaximum(schema) || IsExclusiveMaximum(schema) || IsExclusiveMinimum(schema) || IsMultipleOf(schema));
}
function BuildSchemaPushStack(stack, context, schema, value) {
  return context.UseUnevaluated() ? emit_exports.And(emit_exports.And(context.Push(), BuildSchema(stack, context, schema, value)), context.Pop()) : BuildSchema(stack, context, schema, value);
}
function BuildSchema(stack, context, schema, value) {
  stack.Push(schema);
  const conditions = [];
  if (IsSchemaBoolean(schema))
    return BuildSchemaBoolean(stack, context, schema, value);
  if (IsType(schema))
    conditions.push(BuildType(stack, context, schema, value));
  if (HasObjectKeywords(schema)) {
    const constraints = [];
    if (IsRequired(schema))
      constraints.push(BuildRequired(stack, context, schema, value));
    if (IsAdditionalProperties(schema))
      constraints.push(BuildAdditionalProperties(stack, context, schema, value));
    if (IsDependencies(schema))
      constraints.push(BuildDependencies(stack, context, schema, value));
    if (IsDependentRequired(schema))
      constraints.push(BuildDependentRequired(stack, context, schema, value));
    if (IsDependentSchemas(schema))
      constraints.push(BuildDependentSchemas(stack, context, schema, value));
    if (IsPatternProperties(schema))
      constraints.push(BuildPatternProperties(stack, context, schema, value));
    if (IsProperties(schema))
      constraints.push(BuildProperties(stack, context, schema, value));
    if (IsPropertyNames(schema))
      constraints.push(BuildPropertyNames(stack, context, schema, value));
    if (IsMinProperties(schema))
      constraints.push(BuildMinProperties(stack, context, schema, value));
    if (IsMaxProperties(schema))
      constraints.push(BuildMaxProperties(stack, context, schema, value));
    const reduced = emit_exports.ReduceAnd(constraints);
    const guarded = emit_exports.Or(emit_exports.Not(emit_exports.IsObjectNotArray(value)), reduced);
    conditions.push(HasObjectType(schema) ? reduced : guarded);
  }
  if (HasArrayKeywords(schema)) {
    const constraints = [];
    if (IsAdditionalItems(schema))
      constraints.push(BuildAdditionalItems(stack, context, schema, value));
    if (IsContains(schema))
      constraints.push(BuildContains(stack, context, schema, value));
    if (IsItems(schema))
      constraints.push(BuildItems(stack, context, schema, value));
    if (IsMaxContains(schema))
      constraints.push(BuildMaxContains(stack, context, schema, value));
    if (IsMaxItems(schema))
      constraints.push(BuildMaxItems(stack, context, schema, value));
    if (IsMinContains(schema))
      constraints.push(BuildMinContains(stack, context, schema, value));
    if (IsMinItems(schema))
      constraints.push(BuildMinItems(stack, context, schema, value));
    if (IsPrefixItems(schema))
      constraints.push(BuildPrefixItems(stack, context, schema, value));
    if (IsUniqueItems(schema))
      constraints.push(BuildUniqueItems(stack, context, schema, value));
    const reduced = emit_exports.ReduceAnd(constraints);
    const guarded = emit_exports.Or(emit_exports.Not(emit_exports.IsArray(value)), reduced);
    conditions.push(HasArrayType(schema) ? reduced : guarded);
  }
  if (HasStringKeywords(schema)) {
    const constraints = [];
    if (IsMaxLength(schema))
      constraints.push(BuildMaxLength(stack, context, schema, value));
    if (IsMinLength(schema))
      constraints.push(BuildMinLength(stack, context, schema, value));
    if (IsFormat(schema))
      constraints.push(BuildFormat(stack, context, schema, value));
    if (IsPattern(schema))
      constraints.push(BuildPattern(stack, context, schema, value));
    const reduced = emit_exports.ReduceAnd(constraints);
    const guarded = emit_exports.Or(emit_exports.Not(emit_exports.IsString(value)), reduced);
    conditions.push(HasStringType(schema) ? reduced : guarded);
  }
  if (HasNumberKeywords(schema)) {
    const constraints = [];
    if (IsExclusiveMaximum(schema))
      constraints.push(BuildExclusiveMaximum(stack, context, schema, value));
    if (IsExclusiveMinimum(schema))
      constraints.push(BuildExclusiveMinimum(stack, context, schema, value));
    if (IsMaximum(schema))
      constraints.push(BuildMaximum(stack, context, schema, value));
    if (IsMinimum(schema))
      constraints.push(BuildMinimum(stack, context, schema, value));
    if (IsMultipleOf(schema))
      constraints.push(BuildMultipleOf(stack, context, schema, value));
    const reduced = emit_exports.ReduceAnd(constraints);
    const guarded = emit_exports.Or(emit_exports.Not(emit_exports.Or(emit_exports.IsNumber(value), emit_exports.IsBigInt(value))), reduced);
    conditions.push(HasNumberType(schema) ? reduced : guarded);
  }
  if (IsRef2(schema))
    conditions.push(BuildRef(stack, context, schema, value));
  if (IsRecursiveRef(schema))
    conditions.push(BuildRecursiveRef(stack, context, schema, value));
  if (IsDynamicRef(schema))
    conditions.push(BuildDynamicRef(stack, context, schema, value));
  if (IsConst(schema))
    conditions.push(BuildConst(stack, context, schema, value));
  if (IsEnum2(schema))
    conditions.push(BuildEnum(stack, context, schema, value));
  if (IsIf(schema))
    conditions.push(BuildIf(stack, context, schema, value));
  if (IsNot(schema))
    conditions.push(BuildNot(stack, context, schema, value));
  if (IsAllOf(schema))
    conditions.push(BuildAllOf(stack, context, schema, value));
  if (IsAnyOf(schema))
    conditions.push(BuildAnyOf(stack, context, schema, value));
  if (IsOneOf(schema))
    conditions.push(BuildOneOf(stack, context, schema, value));
  if (IsUnevaluatedItems(schema))
    conditions.push(emit_exports.Or(emit_exports.Not(emit_exports.IsArray(value)), BuildUnevaluatedItems(stack, context, schema, value)));
  if (IsUnevaluatedProperties(schema))
    conditions.push(emit_exports.Or(emit_exports.Not(emit_exports.IsObject(value)), BuildUnevaluatedProperties(stack, context, schema, value)));
  if (IsRefine2(schema))
    conditions.push(BuildRefine(stack, context, schema, value));
  const result = emit_exports.ReduceAnd(conditions);
  stack.Pop(schema);
  return result;
}
function CheckSchemaPushStack(stack, context, schema, value) {
  return context.Push() && CheckSchema(stack, context, schema, value) && context.Pop();
}
function CheckSchema(stack, context, schema, value) {
  stack.Push(schema);
  const result = IsSchemaBoolean(schema) ? CheckSchemaBoolean(stack, context, schema, value) : (!IsType(schema) || CheckType(stack, context, schema, value)) && (!(guard_exports.IsObject(value) && !guard_exports.IsArray(value)) || (!IsRequired(schema) || CheckRequired(stack, context, schema, value)) && (!IsAdditionalProperties(schema) || CheckAdditionalProperties(stack, context, schema, value)) && (!IsDependencies(schema) || CheckDependencies(stack, context, schema, value)) && (!IsDependentRequired(schema) || CheckDependentRequired(stack, context, schema, value)) && (!IsDependentSchemas(schema) || CheckDependentSchemas(stack, context, schema, value)) && (!IsPatternProperties(schema) || CheckPatternProperties(stack, context, schema, value)) && (!IsProperties(schema) || CheckProperties(stack, context, schema, value)) && (!IsPropertyNames(schema) || CheckPropertyNames(stack, context, schema, value)) && (!IsMinProperties(schema) || CheckMinProperties(stack, context, schema, value)) && (!IsMaxProperties(schema) || CheckMaxProperties(stack, context, schema, value))) && (!guard_exports.IsArray(value) || (!IsAdditionalItems(schema) || CheckAdditionalItems(stack, context, schema, value)) && (!IsContains(schema) || CheckContains(stack, context, schema, value)) && (!IsItems(schema) || CheckItems(stack, context, schema, value)) && (!IsMaxContains(schema) || CheckMaxContains(stack, context, schema, value)) && (!IsMaxItems(schema) || CheckMaxItems(stack, context, schema, value)) && (!IsMinContains(schema) || CheckMinContains(stack, context, schema, value)) && (!IsMinItems(schema) || CheckMinItems(stack, context, schema, value)) && (!IsPrefixItems(schema) || CheckPrefixItems(stack, context, schema, value)) && (!IsUniqueItems(schema) || CheckUniqueItems(stack, context, schema, value))) && (!guard_exports.IsString(value) || (!IsMaxLength(schema) || CheckMaxLength(stack, context, schema, value)) && (!IsMinLength(schema) || CheckMinLength(stack, context, schema, value)) && (!IsFormat(schema) || CheckFormat(stack, context, schema, value)) && (!IsPattern(schema) || CheckPattern(stack, context, schema, value))) && (!(guard_exports.IsNumber(value) || guard_exports.IsBigInt(value)) || (!IsExclusiveMaximum(schema) || CheckExclusiveMaximum(stack, context, schema, value)) && (!IsExclusiveMinimum(schema) || CheckExclusiveMinimum(stack, context, schema, value)) && (!IsMaximum(schema) || CheckMaximum(stack, context, schema, value)) && (!IsMinimum(schema) || CheckMinimum(stack, context, schema, value)) && (!IsMultipleOf(schema) || CheckMultipleOf(stack, context, schema, value))) && (!IsRef2(schema) || CheckRef(stack, context, schema, value)) && (!IsRecursiveRef(schema) || CheckRecursiveRef(stack, context, schema, value)) && (!IsDynamicRef(schema) || CheckDynamicRef(stack, context, schema, value)) && (!IsConst(schema) || CheckConst(stack, context, schema, value)) && (!IsEnum2(schema) || CheckEnum(stack, context, schema, value)) && (!IsIf(schema) || CheckIf(stack, context, schema, value)) && (!IsNot(schema) || CheckNot(stack, context, schema, value)) && (!IsAllOf(schema) || CheckAllOf(stack, context, schema, value)) && (!IsAnyOf(schema) || CheckAnyOf(stack, context, schema, value)) && (!IsOneOf(schema) || CheckOneOf(stack, context, schema, value)) && (!IsUnevaluatedItems(schema) || (!guard_exports.IsArray(value) || CheckUnevaluatedItems(stack, context, schema, value))) && (!IsUnevaluatedProperties(schema) || (!guard_exports.IsObject(value) || CheckUnevaluatedProperties(stack, context, schema, value))) && (!IsRefine2(schema) || CheckRefine(stack, context, schema, value));
  stack.Pop(schema);
  return result;
}
function ErrorSchemaPushStack(stack, context, schemaPath, instancePath, schema, value) {
  return context.Push() && ErrorSchema(stack, context, schemaPath, instancePath, schema, value) && context.Pop();
}
function ErrorSchema(stack, context, schemaPath, instancePath, schema, value) {
  if (context.AtCapacity())
    return false;
  stack.Push(schema);
  const result = IsSchemaBoolean(schema) ? ErrorSchemaBoolean(stack, context, schemaPath, instancePath, schema, value) : !!(+(!IsType(schema) || ErrorType(stack, context, schemaPath, instancePath, schema, value)) & +(!(guard_exports.IsObject(value) && !guard_exports.IsArray(value)) || !!(+(!IsRequired(schema) || ErrorRequired(stack, context, schemaPath, instancePath, schema, value)) & +(!IsAdditionalProperties(schema) || ErrorAdditionalProperties(stack, context, schemaPath, instancePath, schema, value)) & +(!IsDependencies(schema) || ErrorDependencies(stack, context, schemaPath, instancePath, schema, value)) & +(!IsDependentRequired(schema) || ErrorDependentRequired(stack, context, schemaPath, instancePath, schema, value)) & +(!IsDependentSchemas(schema) || ErrorDependentSchemas(stack, context, schemaPath, instancePath, schema, value)) & +(!IsPatternProperties(schema) || ErrorPatternProperties(stack, context, schemaPath, instancePath, schema, value)) & +(!IsProperties(schema) || ErrorProperties(stack, context, schemaPath, instancePath, schema, value)) & +(!IsPropertyNames(schema) || ErrorPropertyNames(stack, context, schemaPath, instancePath, schema, value)) & +(!IsMinProperties(schema) || ErrorMinProperties(stack, context, schemaPath, instancePath, schema, value)) & +(!IsMaxProperties(schema) || ErrorMaxProperties(stack, context, schemaPath, instancePath, schema, value)))) & +(!guard_exports.IsArray(value) || !!(+(!IsAdditionalItems(schema) || ErrorAdditionalItems(stack, context, schemaPath, instancePath, schema, value)) & +(!IsContains(schema) || ErrorContains(stack, context, schemaPath, instancePath, schema, value)) & +(!IsItems(schema) || ErrorItems(stack, context, schemaPath, instancePath, schema, value)) & +(!IsMaxContains(schema) || ErrorMaxContains(stack, context, schemaPath, instancePath, schema, value)) & +(!IsMaxItems(schema) || ErrorMaxItems(stack, context, schemaPath, instancePath, schema, value)) & +(!IsMinContains(schema) || ErrorMinContains(stack, context, schemaPath, instancePath, schema, value)) & +(!IsMinItems(schema) || ErrorMinItems(stack, context, schemaPath, instancePath, schema, value)) & +(!IsPrefixItems(schema) || ErrorPrefixItems(stack, context, schemaPath, instancePath, schema, value)) & +(!IsUniqueItems(schema) || ErrorUniqueItems(stack, context, schemaPath, instancePath, schema, value)))) & +(!guard_exports.IsString(value) || !!(+(!IsMaxLength(schema) || ErrorMaxLength(stack, context, schemaPath, instancePath, schema, value)) & +(!IsMinLength(schema) || ErrorMinLength(stack, context, schemaPath, instancePath, schema, value)) & +(!IsFormat(schema) || ErrorFormat(stack, context, schemaPath, instancePath, schema, value)) & +(!IsPattern(schema) || ErrorPattern(stack, context, schemaPath, instancePath, schema, value)))) & +(!(guard_exports.IsNumber(value) || guard_exports.IsBigInt(value)) || !!(+(!IsExclusiveMaximum(schema) || ErrorExclusiveMaximum(stack, context, schemaPath, instancePath, schema, value)) & +(!IsExclusiveMinimum(schema) || ErrorExclusiveMinimum(stack, context, schemaPath, instancePath, schema, value)) & +(!IsMaximum(schema) || ErrorMaximum(stack, context, schemaPath, instancePath, schema, value)) & +(!IsMinimum(schema) || ErrorMinimum(stack, context, schemaPath, instancePath, schema, value)) & +(!IsMultipleOf(schema) || ErrorMultipleOf(stack, context, schemaPath, instancePath, schema, value)))) & +(!IsRef2(schema) || ErrorRef(stack, context, schemaPath, instancePath, schema, value)) & +(!IsRecursiveRef(schema) || ErrorRecursiveRef(stack, context, schemaPath, instancePath, schema, value)) & +(!IsDynamicRef(schema) || ErrorDynamicRef(stack, context, schemaPath, instancePath, schema, value)) & +(!IsConst(schema) || ErrorConst(stack, context, schemaPath, instancePath, schema, value)) & +(!IsEnum2(schema) || ErrorEnum(stack, context, schemaPath, instancePath, schema, value)) & +(!IsIf(schema) || ErrorIf(stack, context, schemaPath, instancePath, schema, value)) & +(!IsNot(schema) || ErrorNot(stack, context, schemaPath, instancePath, schema, value)) & +(!IsAllOf(schema) || ErrorAllOf(stack, context, schemaPath, instancePath, schema, value)) & +(!IsAnyOf(schema) || ErrorAnyOf(stack, context, schemaPath, instancePath, schema, value)) & +(!IsOneOf(schema) || ErrorOneOf(stack, context, schemaPath, instancePath, schema, value)) & +(!IsUnevaluatedItems(schema) || (!guard_exports.IsArray(value) || ErrorUnevaluatedItems(stack, context, schemaPath, instancePath, schema, value))) & +(!IsUnevaluatedProperties(schema) || (!guard_exports.IsObject(value) || ErrorUnevaluatedProperties(stack, context, schemaPath, instancePath, schema, value)))) && (!IsRefine2(schema) || ErrorRefine(stack, context, schemaPath, instancePath, schema, value));
  stack.Pop(schema);
  return result;
}

// node_modules/typebox/build/schema/engine/_functions.mjs
var index2 = [0];
var names = /* @__PURE__ */ new Map();
var funcs = /* @__PURE__ */ new Map();
function NextName() {
  return `${index2[0]++}`;
}
function CreateName(schema, href) {
  if (!names.has(schema))
    names.set(schema, /* @__PURE__ */ new Map());
  const hrefs = names.get(schema);
  if (hrefs.has(href))
    return hrefs.get(href);
  const name = NextName();
  hrefs.set(href, name);
  return name;
}
function CreateCallExpression(context, _schema, name, value) {
  return context.UseUnevaluated() ? emit_exports.Call(`check_${name}`, ["context", value]) : emit_exports.Call(`check_${name}`, [value]);
}
function CreateFunctionExpression(stack, context, schema, name) {
  const expression = BuildSchema(stack, context, schema, "value");
  return context.UseUnevaluated() ? emit_exports.ConstDeclaration(`check_${name}`, emit_exports.ArrowFunction(["context", "value"], expression)) : emit_exports.ConstDeclaration(`check_${name}`, emit_exports.ArrowFunction(["value"], expression));
}
function ResetFunctions() {
  index2[0] = 0;
  names.clear();
  funcs.clear();
}
function GetFunctions() {
  return [...funcs.values()];
}
function CreateFunction(stack, context, schema, value) {
  const name = CreateName(schema, stack.LexicalBaseURL());
  const call = CreateCallExpression(context, schema, name, value);
  if (funcs.has(name))
    return call;
  funcs.set(name, "");
  funcs.set(name, CreateFunctionExpression(stack, context, schema, name));
  return call;
}

// node_modules/typebox/build/schema/resolve/resolve.mjs
var resolve_exports = {};
__export(resolve_exports, {
  Base: () => Base,
  DefaultBase: () => DefaultBase,
  DynamicRef: () => DynamicRef,
  Ref: () => Ref2,
  ResolveDynamicRef: () => ResolveDynamicRef,
  ResolveRecursiveRef: () => ResolveRecursiveRef,
  ResolveRef: () => ResolveRef,
  Resource: () => Resource
});
var DefaultBase = "https://json-schema.org";
function FindDynamicAnchor(schema, name) {
  if (guard_exports.IsObject(schema) && IsDynamicAnchor(schema) && guard_exports.IsEqual(schema.$dynamicAnchor, name)) {
    return schema;
  }
  if (guard_exports.IsObject(schema)) {
    for (const key of guard_exports.Keys(schema)) {
      const result = FindDynamicAnchor(schema[key], name);
      if (result)
        return result;
    }
  }
  return void 0;
}
function FindBase(schema, base, target) {
  if (guard_exports.IsEqual(schema, target))
    return base.href;
  const nextBase = IsSchemaObject(schema) && IsId(schema) ? new URL(schema.$id, base.href) : base;
  if (guard_exports.IsArray(schema)) {
    for (const item of schema) {
      const result = FindBase(item, nextBase, target);
      if (!guard_exports.IsUndefined(result))
        return result;
    }
  } else if (guard_exports.IsObject(schema)) {
    for (const key of guard_exports.Keys(schema)) {
      const result = FindBase(schema[key], nextBase, target);
      if (!guard_exports.IsUndefined(result))
        return result;
    }
  }
  return void 0;
}
function MatchId(schema, base, ref) {
  if (guard_exports.IsEqual(schema.$id, ref.hash))
    return schema;
  const absoluteRef = new URL(ref.href, base.href);
  if (guard_exports.IsEqual(base.pathname, absoluteRef.pathname))
    return ref.hash.startsWith("#") ? MatchHash(schema, ref) : schema;
  return void 0;
}
function MatchAnchor(schema, base, ref) {
  const absoluteAnchor = new URL(`#${schema.$anchor}`, base.href);
  const absoluteRef = new URL(ref.href, base.href);
  return guard_exports.IsEqual(absoluteAnchor.href, absoluteRef.href) ? schema : void 0;
}
function MatchDynamicAnchor(schema, base, ref) {
  const absoluteAnchor = new URL(`#${schema.$dynamicAnchor}`, base.href);
  const absoluteRef = new URL(ref.href, base.href);
  const isMatch = guard_exports.IsEqual(absoluteAnchor.href, absoluteRef.href);
  return isMatch ? schema : void 0;
}
function MatchHash(schema, ref) {
  if (ref.href.endsWith("#"))
    return schema;
  if (!ref.hash.startsWith("#"))
    return void 0;
  const fragment = decodeURIComponent(ref.hash.slice(1));
  if (!fragment.startsWith("/"))
    return void 0;
  const result = pointer_exports.Get(schema, fragment);
  return result;
}
function Match(schema, base, ref) {
  if (IsId(schema)) {
    const result = MatchId(schema, base, ref);
    if (!guard_exports.IsUndefined(result))
      return result;
  }
  if (IsAnchor(schema)) {
    const result = MatchAnchor(schema, base, ref);
    if (!guard_exports.IsUndefined(result))
      return result;
  }
  if (IsDynamicAnchor(schema)) {
    const result = MatchDynamicAnchor(schema, base, ref);
    if (!guard_exports.IsUndefined(result))
      return result;
  }
  return MatchHash(schema, ref);
}
function FromArray(schema, base, ref) {
  return schema.reduce((result, item) => {
    const match = FromValue(item, base, ref);
    return !guard_exports.IsUndefined(match) ? match : result;
  }, void 0);
}
function SkipProperty(key) {
  return guard_exports.IsEqual(key, "const") || guard_exports.IsEqual(key, "enum");
}
function FromObject(schema, base, ref) {
  return guard_exports.Keys(schema).reduce((result, key) => {
    if (SkipProperty(key))
      return result;
    const match = FromValue(schema[key], base, ref);
    return !guard_exports.IsUndefined(match) ? match : result;
  }, void 0);
}
function FromValue(schema, base, ref) {
  const nextBase = IsSchemaObject(schema) && IsId(schema) ? new URL(schema.$id, base.href) : base;
  if (IsSchemaObject(schema)) {
    const result = Match(schema, nextBase, ref);
    if (!guard_exports.IsUndefined(result))
      return result;
  }
  if (guard_exports.IsArray(schema))
    return FromArray(schema, nextBase, ref);
  if (guard_exports.IsObject(schema))
    return FromObject(schema, nextBase, ref);
  return void 0;
}
function Base(schema, base, target) {
  return FindBase(schema, new URL(base || ".", DefaultBase), target);
}
function Resource(context, schema, base, ref) {
  const result = Ref2(context, schema, base, ref, false);
  return IsSchemaObject(result) && IsId(result) ? result : void 0;
}
function CanonicalHref(url) {
  return url.href.split("#")[0];
}
function RefContext(context, ref) {
  return guard_exports.HasPropertyKey(context, ref) ? context[ref] : void 0;
}
function RefLocal(schema, base, ref) {
  return FromValue(schema, base, ref);
}
function RefRemote(context, base, ref) {
  const canonicalHref = CanonicalHref(ref);
  if (guard_exports.IsEqual(canonicalHref, CanonicalHref(base)))
    return void 0;
  if (!guard_exports.HasPropertyKey(context, canonicalHref))
    return void 0;
  const remoteSchema = context[canonicalHref];
  const remoteBase = IsSchemaObject(remoteSchema) && IsId(remoteSchema) ? new URL(remoteSchema.$id, canonicalHref) : new URL(canonicalHref);
  const result = guard_exports.IsEqual(ref.hash, "") ? remoteSchema : FromValue(remoteSchema, remoteBase, ref);
  return result;
}
function LegacyRetrievedResource(root, lexicalSchema, referenceBase, ref, schema) {
  if (!ref.$ref.startsWith("#"))
    return void 0;
  if (guard_exports.HasPropertyKey(root, "$schema"))
    return void 0;
  const targetBase = Base(lexicalSchema, referenceBase, schema);
  if (guard_exports.IsUndefined(targetBase) || guard_exports.IsEqual(targetBase, referenceBase))
    return void 0;
  return { target: schema, base: targetBase, root: lexicalSchema };
}
function RemoteRetrievedResource(context, canonical, schema) {
  const remoteRoot = context[canonical];
  if (!IsSchemaObject(remoteRoot))
    return void 0;
  return { target: schema, base: canonical, root: remoteRoot };
}
function FindRetrievedResource(stackframe, ref, schema, canonical, isRemote) {
  const remote = isRemote ? RemoteRetrievedResource(stackframe.context, canonical, schema) : void 0;
  if (!guard_exports.IsUndefined(remote))
    return remote;
  return LegacyRetrievedResource(stackframe.root, stackframe.lexicalSchema, stackframe.referenceBase, ref, schema);
}
function FindResolvedResource(context, root, referenceBase, canonical, schema, ids) {
  if (IsId(schema))
    return void 0;
  const resource = Resource(context, root, referenceBase, canonical);
  if (!resource || ids.includes(resource))
    return void 0;
  return { target: schema, resource };
}
function Ref2(remotes, schema, base, ref, applySchemaId = true) {
  const initialBase = new URL(base || ".", DefaultBase);
  const resolvedBase = applySchemaId && IsId(schema) ? new URL(schema.$id, initialBase) : initialBase;
  const initialRef = new URL(ref, resolvedBase.href);
  return RefContext(remotes, ref) ?? RefLocal(schema, resolvedBase, initialRef) ?? RefRemote(remotes, resolvedBase, initialRef);
}
function DynamicRef(context, root, base, schema, dynamicRef, dynamicAnchors) {
  const initialBase = new URL(base || ".", DefaultBase);
  const fragmentRoot = dynamicRef.$dynamicRef.startsWith("#") ? schema : root;
  const fragmentTarget = Ref2(context, fragmentRoot, base, dynamicRef.$dynamicRef, false);
  if (guard_exports.IsUndefined(fragmentTarget)) {
    const fragment2 = new URL(dynamicRef.$dynamicRef, initialBase).hash;
    if (!fragment2.startsWith("#/") && fragment2.startsWith("#")) {
      const name = decodeURIComponent(fragment2.slice(1));
      const anchorTarget2 = dynamicAnchors.find((anchor) => guard_exports.IsEqual(anchor.$dynamicAnchor, name)) ?? FindDynamicAnchor(root, name);
      return anchorTarget2;
    }
    return void 0;
  }
  if (!IsSchemaObject(fragmentTarget) || !IsDynamicAnchor(fragmentTarget))
    return fragmentTarget;
  const fragment = new URL(dynamicRef.$dynamicRef, initialBase).hash;
  if (fragment.startsWith("#/"))
    return fragmentTarget;
  const anchorTarget = dynamicAnchors.find((anchor) => guard_exports.IsEqual(anchor.$dynamicAnchor, fragmentTarget.$dynamicAnchor));
  return anchorTarget ?? fragmentTarget;
}
function ResolveRef(stackframe, ref) {
  const source = stackframe.inRetrievedFrame ? stackframe.lexicalSchema : stackframe.root;
  const refRoot = ref.$ref.startsWith("#") ? stackframe.lexicalSchema : source;
  const schema = Ref2(stackframe.context, refRoot, stackframe.referenceBase, ref.$ref, false);
  if (!schema || !IsSchemaObject(schema))
    return { schema };
  const canonical = new URL(ref.$ref, stackframe.referenceBase).href.split("#")[0];
  const isRemote = !guard_exports.IsEqual(canonical, stackframe.resourceBase);
  const retrievedResource = FindRetrievedResource(stackframe, ref, schema, canonical, isRemote);
  const resolvedResource = isRemote ? FindResolvedResource(stackframe.context, stackframe.root, stackframe.referenceBase, canonical, schema, stackframe.ids) : void 0;
  return { schema, retrievedResource, resolvedResource };
}
function ResolveRecursiveRef(stackframe, recursiveRef) {
  const refRoot = IsRecursiveAnchorTrue(stackframe.lexicalSchema) ? stackframe.recursiveAnchors[0] : stackframe.lexicalSchema;
  return Ref2(stackframe.context, refRoot, stackframe.lexicalBase, recursiveRef.$recursiveRef, false);
}
function ResolveDynamicRef(stackframe, dynamicRef) {
  return DynamicRef(stackframe.context, stackframe.root, stackframe.lexicalBase, stackframe.lexicalSchema, dynamicRef, stackframe.dynamicAnchors);
}

// node_modules/typebox/build/schema/engine/_stack.mjs
var __classPrivateFieldGet = function(receiver, state2, kind, f) {
  if (kind === "a" && !f) throw new TypeError("Private accessor was defined without a getter");
  if (typeof state2 === "function" ? receiver !== state2 || !f : !state2.has(receiver)) throw new TypeError("Cannot read private member from an object whose class did not declare it");
  return kind === "m" ? f : kind === "a" ? f.call(receiver) : f ? f.value : state2.get(receiver);
};
var _Stack_instances;
var _Stack_StackFrame;
var _Stack_ApplyRefResult;
var _Stack_BuildBase;
var _Stack_ResourceBaseURL;
var _Stack_ReferenceBaseURL;
var _Stack_LexicalSchema;
var _Stack_RegisterResourceAnchorArray;
var _Stack_UnregisterResourceAnchorArray;
var _Stack_RegisterResourceAnchors;
var _Stack_UnregisterResourceAnchors;
var _Stack_RegisterResource;
var _Stack_UnregisterResource;
var _Stack_EnterResolvedResource;
var _Stack_ExitResolvedResource;
var Stack = class {
  constructor(context, schema) {
    _Stack_instances.add(this);
    this.context = context;
    this.schema = schema;
    this.ids = [];
    this.resourceIds = [];
    this.anchors = [];
    this.recursiveAnchors = [];
    this.dynamicAnchors = [];
    this.retrievedResources = /* @__PURE__ */ new Map();
    this.retrievedFrames = [];
    this.resolvedResources = /* @__PURE__ */ new Map();
    this.pendingResource = true;
  }
  // ----------------------------------------------------------------
  // LexicalBaseURL
  // ----------------------------------------------------------------
  LexicalBaseURL() {
    return __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_BuildBase).call(this, this.ids);
  }
  // ----------------------------------------------------------------
  // Push
  // ----------------------------------------------------------------
  Push(schema) {
    if (!IsSchemaObject(schema))
      return;
    if (IsId(schema))
      __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_RegisterResource).call(this, schema);
    if (IsAnchor(schema))
      this.anchors.push(schema);
    if (IsRecursiveAnchorTrue(schema))
      this.recursiveAnchors.push(schema);
    if (IsDynamicAnchor(schema))
      this.dynamicAnchors.push(schema);
    const retrievedResource = this.retrievedResources.get(schema);
    if (retrievedResource) {
      this.retrievedFrames.push({
        schema: retrievedResource.root,
        base: retrievedResource.base,
        idDepth: this.ids.length,
        resourceDepth: this.resourceIds.length
      });
    }
  }
  // ----------------------------------------------------------------
  // Pop
  // ----------------------------------------------------------------
  Pop(schema) {
    if (!IsSchemaObject(schema))
      return;
    if (IsId(schema))
      __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_UnregisterResource).call(this, schema);
    if (IsAnchor(schema))
      this.anchors.pop();
    if (IsRecursiveAnchorTrue(schema))
      this.recursiveAnchors.pop();
    if (IsDynamicAnchor(schema))
      this.dynamicAnchors.pop();
    if (this.retrievedResources.has(schema))
      this.retrievedFrames.pop();
    __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_ExitResolvedResource).call(this, schema);
  }
  // ----------------------------------------------------------------
  // Ref
  // ----------------------------------------------------------------
  Ref(ref) {
    const result = resolve_exports.ResolveRef(__classPrivateFieldGet(this, _Stack_instances, "m", _Stack_StackFrame).call(this), ref);
    __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_ApplyRefResult).call(this, result);
    return result.schema;
  }
  // ----------------------------------------------------------------
  // RecursiveRef
  // ----------------------------------------------------------------
  RecursiveRef(recursiveRef) {
    const result = resolve_exports.ResolveRecursiveRef(__classPrivateFieldGet(this, _Stack_instances, "m", _Stack_StackFrame).call(this), recursiveRef);
    if (result)
      this.pendingResource = true;
    return result;
  }
  // ----------------------------------------------------------------
  // DynamicRef
  // ----------------------------------------------------------------
  DynamicRef(dynamicRef) {
    const result = resolve_exports.ResolveDynamicRef(__classPrivateFieldGet(this, _Stack_instances, "m", _Stack_StackFrame).call(this), dynamicRef);
    if (result)
      this.pendingResource = true;
    return result;
  }
};
_Stack_instances = /* @__PURE__ */ new WeakSet(), _Stack_StackFrame = function _Stack_StackFrame2() {
  return {
    context: this.context,
    root: this.schema,
    ids: this.ids,
    lexicalSchema: __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_LexicalSchema).call(this),
    lexicalBase: this.LexicalBaseURL(),
    referenceBase: __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_ReferenceBaseURL).call(this),
    resourceBase: __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_ResourceBaseURL).call(this),
    recursiveAnchors: this.recursiveAnchors,
    dynamicAnchors: this.dynamicAnchors,
    inRetrievedFrame: this.retrievedFrames.length > 0
  };
}, _Stack_ApplyRefResult = function _Stack_ApplyRefResult2(result) {
  if (!guard_exports.IsUndefined(result.schema))
    this.pendingResource = true;
  if (!guard_exports.IsUndefined(result.resolvedResource)) {
    __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_EnterResolvedResource).call(this, result.resolvedResource.target, result.resolvedResource.resource);
  }
  if (!guard_exports.IsUndefined(result.retrievedResource)) {
    this.retrievedResources.set(result.retrievedResource.target, {
      base: result.retrievedResource.base,
      root: result.retrievedResource.root
    });
  }
}, _Stack_BuildBase = function _Stack_BuildBase2(stack) {
  const frame = this.retrievedFrames[this.retrievedFrames.length - 1];
  const base = frame ? new URL(frame.base) : new URL(resolve_exports.DefaultBase);
  const scoped = frame ? stack.slice(frame.idDepth) : stack;
  return scoped.reduce((result, schema) => new URL(schema.$id, result), base).href;
}, _Stack_ResourceBaseURL = function _Stack_ResourceBaseURL2() {
  const frame = this.retrievedFrames[this.retrievedFrames.length - 1];
  if (!frame)
    return __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_BuildBase).call(this, this.resourceIds);
  return this.resourceIds.slice(frame.resourceDepth).reduce((result, schema) => new URL(schema.$id, result), new URL(frame.base)).href;
}, _Stack_ReferenceBaseURL = function _Stack_ReferenceBaseURL2() {
  if (this.retrievedFrames.length > 0)
    return __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_ResourceBaseURL).call(this);
  const lexical = this.ids[this.ids.length - 1];
  if (lexical && !/^[A-Za-z][A-Za-z0-9+.-]*:/.test(lexical.$id))
    return this.LexicalBaseURL();
  return __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_ResourceBaseURL).call(this);
}, _Stack_LexicalSchema = function _Stack_LexicalSchema2() {
  const frame = this.retrievedFrames[this.retrievedFrames.length - 1];
  if (frame)
    return this.ids.length > frame.idDepth ? this.ids[this.ids.length - 1] : frame.schema;
  return this.ids.length > 0 ? this.ids[this.ids.length - 1] : this.schema;
}, _Stack_RegisterResourceAnchorArray = function _Stack_RegisterResourceAnchorArray2(schema) {
  schema.forEach((schema2) => __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_RegisterResourceAnchors).call(this, schema2, false));
}, _Stack_UnregisterResourceAnchorArray = function _Stack_UnregisterResourceAnchorArray2(schema) {
  schema.forEach((schema2) => __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_UnregisterResourceAnchors).call(this, schema2, false));
}, _Stack_RegisterResourceAnchors = function _Stack_RegisterResourceAnchors2(schema, isRoot = true) {
  if (IsSchemaBoolean(schema))
    return;
  if (guard_exports.IsArray(schema))
    return __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_RegisterResourceAnchorArray).call(this, schema);
  if (!IsSchemaObject(schema))
    return;
  const current = schema;
  if (!isRoot && IsId(current))
    return;
  if (!isRoot && IsDynamicAnchor(current))
    this.dynamicAnchors.push(current);
  for (const key of guard_exports.Keys(current))
    __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_RegisterResourceAnchors2).call(this, current[key], false);
}, _Stack_UnregisterResourceAnchors = function _Stack_UnregisterResourceAnchors2(schema, isRoot = true) {
  if (IsSchemaBoolean(schema))
    return;
  if (guard_exports.IsArray(schema))
    return __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_UnregisterResourceAnchorArray).call(this, schema);
  if (!IsSchemaObject(schema))
    return;
  const current = schema;
  if (!isRoot && IsId(current))
    return;
  if (!isRoot && IsDynamicAnchor(current))
    this.dynamicAnchors.pop();
  for (const key of guard_exports.Keys(current))
    __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_UnregisterResourceAnchors2).call(this, current[key], false);
}, _Stack_RegisterResource = function _Stack_RegisterResource2(schema) {
  this.ids.push(schema);
  const isResource = this.pendingResource;
  this.pendingResource = false;
  if (isResource)
    this.resourceIds.push(schema);
  __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_RegisterResourceAnchors).call(this, schema, true);
}, _Stack_UnregisterResource = function _Stack_UnregisterResource2(schema) {
  this.ids.pop();
  const isResource = this.resourceIds.length > 0 && guard_exports.IsEqual(this.resourceIds[this.resourceIds.length - 1], schema);
  if (isResource)
    this.resourceIds.pop();
  __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_UnregisterResourceAnchors).call(this, schema, true);
}, _Stack_EnterResolvedResource = function _Stack_EnterResolvedResource2(target, resource) {
  __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_RegisterResource).call(this, resource);
  this.resolvedResources.set(target, resource);
}, _Stack_ExitResolvedResource = function _Stack_ExitResolvedResource2(target) {
  if (!this.resolvedResources.has(target))
    return;
  const resource = this.resolvedResources.get(target);
  __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_UnregisterResource).call(this, resource);
  this.resolvedResources.delete(target);
};

// node_modules/typebox/build/schema/build.mjs
function CreateCode(build) {
  const functions = build.Functions().join(";\n");
  const statements = build.UseUnevaluated() ? ["const context = new CheckContext({}, {})", `return ${build.Entry()}`] : [`return ${build.Entry()}`];
  return `${functions}; return (value) => { ${statements.join("; ")} }`;
}
function CreateEvaluatedCheck(build, code) {
  const factory = environment_exports.Evaluate("CheckContext", "Guard", "Format", "Hashing", build.External().identifier, code);
  return factory(CheckContext, guard_exports, format_exports, hash_exports, build.External().variables);
}
function CreateDynamicCheck(build) {
  const stack = new Stack(build.Context(), build.Schema());
  const context = new CheckContext();
  return (value) => CheckSchema(stack, context, build.Schema(), value);
}
function CreateCheck(build, code) {
  return environment_exports.CanEvaluate() ? CreateEvaluatedCheck(build, code) : CreateDynamicCheck(build);
}
var EvaluateResult = class {
  constructor(isAccelerated, code, check) {
    this.isAccelerated = isAccelerated;
    this.code = code;
    this.check = check;
  }
  IsAccelerated() {
    return this.isAccelerated;
  }
  Code() {
    return this.code;
  }
  Check(value) {
    return this.check(value);
  }
};
var BuildResult = class {
  constructor(context, schema, external, functions, entry, useUnevaluated) {
    this.context = context;
    this.schema = schema;
    this.external = external;
    this.functions = functions;
    this.entry = entry;
    this.useUnevaluated = useUnevaluated;
  }
  /** Returns the Context used for this build */
  Context() {
    return this.context;
  }
  /** Returns the Schema used for this build */
  Schema() {
    return this.schema;
  }
  /** Returns true if this build requires a Unevaluated context */
  UseUnevaluated() {
    return this.useUnevaluated;
  }
  /** Returns external variables */
  External() {
    return this.external;
  }
  /** Returns check functions */
  Functions() {
    return this.functions;
  }
  /** Return entry function call. */
  Entry() {
    return this.entry;
  }
  /** Evaluates the build into a validation function */
  Evaluate() {
    const code = CreateCode(this);
    const check = CreateCheck(this, code);
    return new EvaluateResult(environment_exports.CanEvaluate(), code, check);
  }
};
function Build(...args) {
  const [context, schema] = arguments_exports.Match(args, {
    2: (context2, schema2) => [context2, schema2],
    1: (schema2) => [{}, schema2]
  });
  ResetExternal();
  ResetFunctions();
  const stack = new Stack(context, schema);
  const build = new BuildContext(HasUnevaluated(context, schema));
  const call = CreateFunction(stack, build, schema, "value");
  const functions = GetFunctions();
  const externals = GetExternal();
  return new BuildResult(context, schema, externals, functions, call, build.UseUnevaluated());
}

// node_modules/typebox/build/schema/errors.mjs
function Errors(...args) {
  const [context, schema, value] = arguments_exports.Match(args, {
    3: (context2, schema2, value2) => [context2, schema2, value2],
    2: (schema2, value2) => [{}, schema2, value2]
  });
  const stack = new Stack(context, schema);
  const errorContext = new ErrorContext();
  const result = ErrorSchema(stack, errorContext, "#", "", schema, value);
  const errors = errorContext.GetErrors();
  const locale = Get();
  const localized = errors.map((error) => ({ ...error, message: locale(error) }));
  return [result, localized];
}

// node_modules/typebox/build/schema/check.mjs
function Check(...args) {
  const [context, schema, value] = arguments_exports.Match(args, {
    3: (context2, schema2, value2) => [context2, schema2, value2],
    2: (schema2, value2) => [{}, schema2, value2]
  });
  const stack = new Stack(context, schema);
  const checkContext = new CheckContext();
  return CheckSchema(stack, checkContext, schema, value);
}

// node_modules/typebox/build/value/check/check.mjs
function Check2(...args) {
  const [context, type, value] = arguments_exports.Match(args, {
    3: (context2, type2, value2) => [context2, type2, value2],
    2: (type2, value2) => [{}, type2, value2]
  });
  return Check(context, type, value);
}

// node_modules/typebox/build/value/errors/errors.mjs
function Errors2(...args) {
  const [context, type, value] = arguments_exports.Match(args, {
    3: (context2, type2, value2) => [context2, type2, value2],
    2: (type2, value2) => [{}, type2, value2]
  });
  const [_, errors] = Errors(context, type, value);
  return errors;
}

// node_modules/typebox/build/value/assert/assert.mjs
var AssertError = class extends Error {
  constructor(source, value, errors) {
    super(source);
    Object.defineProperty(this, "cause", {
      value: { source, errors, value },
      writable: false,
      configurable: false,
      enumerable: false
    });
  }
};
function Assert(...args) {
  const [context, type, value] = arguments_exports.Match(args, {
    3: (context2, type2, value2) => [context2, type2, value2],
    2: (type2, value2) => [{}, type2, value2]
  });
  const check = Check2(context, type, value);
  if (!check)
    throw new AssertError("Assert", value, Errors2(context, type, value));
}

// node_modules/typebox/build/value/clone/clone.mjs
function Clone2(value) {
  return Clone(value);
}

// node_modules/typebox/build/value/shared/union_priority_sort.mjs
function Modifiers(type, next) {
  for (const key of guard_default.Keys(type)) {
    if (guard_default.HasPropertyKey(next, key))
      continue;
    next[key] = type[key];
  }
  return next;
}
function FromProperties(properties) {
  const result = {};
  for (const key of guard_default.Keys(properties))
    result[key] = FromType(properties[key]);
  return result;
}
function FromPriorityTypes(types) {
  return FromTypes(Priority(types));
}
function FromTypes(types) {
  return types.map((type) => FromType(type));
}
function FromType(type) {
  const next = IsArray(type) ? _Array_(FromType(type.items), ArrayOptions(type)) : IsIntersect(type) ? Intersect(FromTypes(type.allOf)) : IsUnion(type) ? Union(FromPriorityTypes(type.anyOf)) : IsObject(type) ? _Object_(FromProperties(type.properties)) : IsRecord(type) ? Record(RecordKey(type), FromType(RecordValue(type))) : IsTuple(type) ? Tuple(FromTypes(type.items)) : type;
  return Modifiers(type, next);
}
function UnionPrioritySort(type) {
  const result = FromType(type);
  return result;
}

// node_modules/typebox/build/value/clean/from_array.mjs
function FromArray2(context, type, value) {
  if (!guard_exports.IsArray(value))
    return value;
  return value.map((value2) => FromType2(context, type.items, value2));
}

// node_modules/typebox/build/value/clean/from_cyclic.mjs
function FromCyclic(context, type, value) {
  return FromType2({ ...context, ...type.$defs }, Ref(type.$ref), value);
}

// node_modules/typebox/build/value/clean/from_intersect.mjs
function EvaluateIntersection(context, type) {
  const additionalProperties = guard_exports.HasPropertyKey(type, "unevaluatedProperties") ? { additionalProperties: type.unevaluatedProperties } : {};
  const instantiated = Instantiate(context, type);
  const evaluated = Evaluate(instantiated);
  return IsObject(evaluated) ? With(evaluated, additionalProperties) : evaluated;
}
function FromIntersect(context, type, value) {
  const evaluated = EvaluateIntersection(context, type);
  return FromType2(context, evaluated, value);
}

// node_modules/typebox/build/value/clean/additional.mjs
function GetAdditionalProperties(type) {
  const additionalProperties = guard_exports.HasPropertyKey(type, "additionalProperties") ? type.additionalProperties : void 0;
  return additionalProperties;
}

// node_modules/typebox/build/value/clean/from_object.mjs
function FromObject2(context, type, value) {
  if (!guard_exports.IsObject(value) || guard_exports.IsArray(value))
    return value;
  const additionalProperties = GetAdditionalProperties(type);
  for (const key of guard_exports.Keys(value)) {
    if (guard_exports.HasPropertyKey(type.properties, key)) {
      value[key] = FromType2(context, type.properties[key], value[key]);
      continue;
    }
    const unknownCheck = (
      // 1. additionalProperties: true
      guard_exports.IsBoolean(additionalProperties) && guard_exports.IsEqual(additionalProperties, true) || IsSchema(additionalProperties) && Check2(context, additionalProperties, value[key])
    );
    if (unknownCheck) {
      value[key] = FromType2(context, additionalProperties, value[key]);
      continue;
    }
    delete value[key];
  }
  return value;
}

// node_modules/typebox/build/value/clean/from_record.mjs
function FromRecord(context, type, value) {
  if (!guard_exports.IsObject(value))
    return value;
  const additionalProperties = GetAdditionalProperties(type);
  const [recordPattern, recordValue] = [new RegExp(RecordPattern(type)), RecordValue(type)];
  for (const key of guard_exports.Keys(value)) {
    if (recordPattern.test(key)) {
      value[key] = FromType2(context, recordValue, value[key]);
      continue;
    }
    const unknownCheck = (
      // 1. additionalProperties: true
      guard_exports.IsBoolean(additionalProperties) && guard_exports.IsEqual(additionalProperties, true) || IsSchema(additionalProperties) && Check2(context, additionalProperties, value[key])
    );
    if (unknownCheck) {
      value[key] = FromType2(context, additionalProperties, value[key]);
      continue;
    }
    delete value[key];
  }
  return value;
}

// node_modules/typebox/build/value/clean/from_ref.mjs
function FromRef(context, type, value) {
  return guard_exports.HasPropertyKey(context, type.$ref) ? FromType2(context, context[type.$ref], value) : value;
}

// node_modules/typebox/build/value/clean/from_tuple.mjs
function FromTuple(context, schema, value) {
  if (!guard_exports.IsArray(value))
    return value;
  const length = Math.min(value.length, schema.items.length);
  for (let index3 = 0; index3 < length; index3++) {
    value[index3] = FromType2(context, schema.items[index3], value[index3]);
  }
  return guard_exports.IsGreaterThan(value.length, length) ? value.slice(0, length) : value;
}

// node_modules/typebox/build/value/clean/from_union.mjs
function FromUnion(context, type, value) {
  for (const schema of type.anyOf) {
    const clean = FromType2(context, schema, Clone2(value));
    if (Check2(context, schema, clean))
      return clean;
  }
  return value;
}

// node_modules/typebox/build/value/clean/from_type.mjs
function FromType2(context, type, value) {
  return IsArray(type) ? FromArray2(context, type, value) : IsCyclic(type) ? FromCyclic(context, type, value) : IsIntersect(type) ? FromIntersect(context, type, value) : IsObject(type) ? FromObject2(context, type, value) : IsRecord(type) ? FromRecord(context, type, value) : IsRef(type) ? FromRef(context, type, value) : IsTuple(type) ? FromTuple(context, type, value) : IsUnion(type) ? FromUnion(context, type, value) : value;
}

// node_modules/typebox/build/value/clean/clean.mjs
function Clean(...args) {
  const [context, type, value] = arguments_exports.Match(args, {
    3: (context2, type2, value2) => [context2, type2, value2],
    2: (type2, value2) => [{}, type2, value2]
  });
  const sorted = settings_exports.Get().unionPrioritySort ? UnionPrioritySort(type) : type;
  return FromType2(context, sorted, value);
}

// node_modules/typebox/build/value/shared/optional_undefined.mjs
function IsOptionalUndefined(property, key, value) {
  return IsOptional(property) && guard_exports.IsUndefined(value[key]);
}

// node_modules/typebox/build/value/convert/try/try.mjs
var try_exports = {};
__export(try_exports, {
  Fail: () => Fail,
  IsOk: () => IsOk,
  Ok: () => Ok,
  TryArray: () => TryArray,
  TryBigInt: () => TryBigInt,
  TryBoolean: () => TryBoolean,
  TryNull: () => TryNull,
  TryNumber: () => TryNumber,
  TryString: () => TryString,
  TryUndefined: () => TryUndefined
});

// node_modules/typebox/build/value/convert/try/try_result.mjs
function IsOk(value) {
  return guard_exports.IsObject(value) && guard_exports.HasPropertyKey(value, "value");
}
function Ok(value) {
  return { value };
}
function Fail() {
  return void 0;
}

// node_modules/typebox/build/value/convert/try/try_array.mjs
function TryArray(value) {
  return guard_exports.IsArray(value) ? Ok(value) : Ok([value]);
}

// node_modules/typebox/build/value/convert/try/try_bigint.mjs
function FromBoolean(value) {
  return guard_exports.IsEqual(value, true) ? Ok(BigInt(1)) : Ok(BigInt(0));
}
var bigintPattern = /^-?(0|[1-9]\d*)n$/;
var decimalPattern = /^-?(0|[1-9]\d*)\.\d+$/;
var integerPattern = /^-?(0|[1-9]\d*)$/;
function IsStringBigIntLike(value) {
  return bigintPattern.test(value);
}
function IsStringDecimalLike(value) {
  return decimalPattern.test(value);
}
function IsStringIntegerLike(value) {
  return integerPattern.test(value);
}
function FromString(value) {
  const lowercase = value.toLowerCase();
  return IsStringBigIntLike(value) ? Ok(BigInt(value.slice(0, value.length - 1))) : IsStringDecimalLike(value) ? Ok(BigInt(value.split(".")[0])) : IsStringIntegerLike(value) ? Ok(BigInt(value)) : guard_exports.IsEqual(lowercase, "false") ? Ok(BigInt(0)) : guard_exports.IsEqual(lowercase, "true") ? Ok(BigInt(1)) : Fail();
}
function TryBigInt(value) {
  return guard_exports.IsBigInt(value) ? Ok(value) : guard_exports.IsBoolean(value) ? FromBoolean(value) : guard_exports.IsNumber(value) ? Ok(BigInt(Math.trunc(value))) : guard_exports.IsNull(value) ? Ok(BigInt(0)) : guard_exports.IsString(value) ? FromString(value) : guard_exports.IsUndefined(value) ? Ok(BigInt(0)) : Fail();
}

// node_modules/typebox/build/value/convert/try/try_boolean.mjs
function FromBigInt(value) {
  return guard_exports.IsEqual(value, BigInt(0)) ? Ok(false) : guard_exports.IsEqual(value, BigInt(1)) ? Ok(true) : Fail();
}
function FromNumber(value) {
  return guard_exports.IsEqual(value, 0) ? Ok(false) : guard_exports.IsEqual(value, 1) ? Ok(true) : Fail();
}
function FromString2(value) {
  return guard_exports.IsEqual(value.toLowerCase(), "false") ? Ok(false) : guard_exports.IsEqual(value.toLowerCase(), "true") ? Ok(true) : guard_exports.IsEqual(value, "0") ? Ok(false) : guard_exports.IsEqual(value, "1") ? Ok(true) : Fail();
}
function TryBoolean(value) {
  return guard_exports.IsBigInt(value) ? FromBigInt(value) : guard_exports.IsBoolean(value) ? Ok(value) : guard_exports.IsNumber(value) ? FromNumber(value) : guard_exports.IsNull(value) ? Ok(false) : guard_exports.IsString(value) ? FromString2(value) : guard_exports.IsUndefined(value) ? Ok(false) : Fail();
}

// node_modules/typebox/build/value/convert/try/try_null.mjs
function FromBigInt2(value) {
  return guard_exports.IsEqual(value, BigInt(0)) ? Ok(null) : Fail();
}
function FromBoolean2(value) {
  return guard_exports.IsEqual(value, false) ? Ok(null) : Fail();
}
function FromNumber2(value) {
  return guard_exports.IsEqual(value, 0) ? Ok(null) : Fail();
}
function FromString3(value) {
  const lowercase = value.toLowerCase();
  const predicate = guard_exports.IsEqual(lowercase, "undefined") || guard_exports.IsEqual(lowercase, "null") || guard_exports.IsEqual(value, "") || guard_exports.IsEqual(value, "0");
  return predicate ? Ok(null) : Fail();
}
function TryNull(value) {
  return guard_exports.IsBigInt(value) ? FromBigInt2(value) : guard_exports.IsBoolean(value) ? FromBoolean2(value) : guard_exports.IsNumber(value) ? FromNumber2(value) : guard_exports.IsNull(value) ? Ok(null) : guard_exports.IsString(value) ? FromString3(value) : guard_exports.IsUndefined(value) ? Ok(null) : Fail();
}

// node_modules/typebox/build/value/convert/try/try_number.mjs
var maxBigInt = BigInt(Number.MAX_SAFE_INTEGER);
var minBigInt = BigInt(Number.MIN_SAFE_INTEGER);
function FromBigInt3(value) {
  return value <= maxBigInt && value >= minBigInt ? Ok(Number(value)) : Fail();
}
function FromBoolean3(value) {
  return Ok(value ? 1 : 0);
}
function FromString4(value) {
  const coerced = +value;
  if (guard_exports.IsNumber(coerced))
    return Ok(coerced);
  const lowercase = value.toLowerCase();
  if (guard_exports.IsEqual(lowercase, "false"))
    return Ok(0);
  if (guard_exports.IsEqual(lowercase, "true"))
    return Ok(1);
  const result = TryBigInt(value);
  if (IsOk(result))
    return result.value <= maxBigInt && result.value >= minBigInt ? Ok(Number(result.value)) : Fail();
  return Fail();
}
function TryNumber(value) {
  return guard_exports.IsBigInt(value) ? FromBigInt3(value) : guard_exports.IsBoolean(value) ? FromBoolean3(value) : guard_exports.IsNumber(value) ? Ok(value) : guard_exports.IsNull(value) ? Ok(0) : guard_exports.IsString(value) ? FromString4(value) : guard_exports.IsUndefined(value) ? Ok(0) : Fail();
}

// node_modules/typebox/build/value/convert/try/try_string.mjs
function TryString(value) {
  return guard_exports.IsBigInt(value) ? Ok(value.toString()) : guard_exports.IsBoolean(value) ? Ok(value.toString()) : guard_exports.IsNumber(value) ? Ok(value.toString()) : guard_exports.IsNull(value) ? Ok("null") : guard_exports.IsString(value) ? Ok(value) : guard_exports.IsUndefined(value) ? Ok("") : Fail();
}

// node_modules/typebox/build/value/convert/try/try_undefined.mjs
function FromBigInt4(value) {
  return guard_exports.IsEqual(value, BigInt(0)) ? Ok(void 0) : Fail();
}
function FromBoolean4(value) {
  return guard_exports.IsEqual(value, false) ? Ok(void 0) : Fail();
}
function FromNumber3(value) {
  return guard_exports.IsEqual(value, 0) ? Ok(void 0) : Fail();
}
function FromString5(value) {
  const lowercase = value.toLowerCase();
  const predicate = guard_exports.IsEqual(lowercase, "undefined") || guard_exports.IsEqual(lowercase, "null") || guard_exports.IsEqual(value, "") || guard_exports.IsEqual(value, "0");
  return predicate ? Ok(void 0) : Fail();
}
function TryUndefined(value) {
  return guard_exports.IsBigInt(value) ? FromBigInt4(value) : guard_exports.IsBoolean(value) ? FromBoolean4(value) : guard_exports.IsNumber(value) ? FromNumber3(value) : guard_exports.IsNull(value) ? Ok(void 0) : guard_exports.IsString(value) ? FromString5(value) : guard_exports.IsUndefined(value) ? Ok(value) : Fail();
}

// node_modules/typebox/build/value/convert/from_array.mjs
function FromArray3(context, type, value) {
  const result = try_exports.TryArray(value);
  return result.value.map((value2) => FromType3(context, type.items, value2));
}

// node_modules/typebox/build/value/convert/from_bigint.mjs
function FromBigInt5(_context, _type, value) {
  const result = try_exports.TryBigInt(value);
  return try_exports.IsOk(result) ? result.value : value;
}

// node_modules/typebox/build/value/convert/from_boolean.mjs
function FromBoolean5(_context, _type, value) {
  const result = try_exports.TryBoolean(value);
  return try_exports.IsOk(result) ? result.value : value;
}

// node_modules/typebox/build/value/convert/from_cyclic.mjs
function FromCyclic2(context, type, value) {
  return FromType3({ ...context, ...type.$defs }, Ref(type.$ref), value);
}

// node_modules/typebox/build/value/convert/from_enum.mjs
function FromEnum(context, type, value) {
  return FromType3(context, Evaluate(type), value);
}

// node_modules/typebox/build/value/convert/from_integer.mjs
function FromInteger(_context, _type, value) {
  const result = try_exports.TryNumber(value);
  return try_exports.IsOk(result) ? Math.trunc(result.value) : value;
}

// node_modules/typebox/build/value/convert/from_intersect.mjs
function FromIntersect2(context, type, value) {
  const instantiated = Instantiate(context, type);
  const evaluated = Evaluate(instantiated);
  return FromType3(context, evaluated, value);
}

// node_modules/typebox/build/value/convert/from_literal.mjs
function FromLiteralBigInt(_context, type, value) {
  const result = try_exports.TryBigInt(value);
  return try_exports.IsOk(result) && guard_exports.IsEqual(type.const, result.value) ? result.value : value;
}
function FromLiteralBoolean(_context, type, value) {
  const result = try_exports.TryBoolean(value);
  return try_exports.IsOk(result) && guard_exports.IsEqual(type.const, result.value) ? result.value : value;
}
function FromLiteralNumber(_context, type, value) {
  const result = try_exports.TryNumber(value);
  return try_exports.IsOk(result) && guard_exports.IsEqual(type.const, result.value) ? result.value : value;
}
function FromLiteralString(_context, type, value) {
  const result = try_exports.TryString(value);
  return try_exports.IsOk(result) && guard_exports.IsEqual(type.const, result.value) ? result.value : value;
}
function FromLiteral(context, type, value) {
  if (guard_exports.IsEqual(type.const, value))
    return value;
  return IsLiteralBigInt(type) ? FromLiteralBigInt(context, type, value) : IsLiteralBoolean(type) ? FromLiteralBoolean(context, type, value) : IsLiteralNumber(type) ? FromLiteralNumber(context, type, value) : IsLiteralString(type) ? FromLiteralString(context, type, value) : Unreachable();
}

// node_modules/typebox/build/value/convert/from_null.mjs
function FromNull(_context, _type, value) {
  const result = try_exports.TryNull(value);
  return try_exports.IsOk(result) ? result.value : value;
}

// node_modules/typebox/build/value/convert/from_number.mjs
function FromNumber4(_context, _type, value) {
  const result = try_exports.TryNumber(value);
  return try_exports.IsOk(result) ? result.value : value;
}

// node_modules/typebox/build/value/convert/from_additional.mjs
function FromAdditionalProperties(context, entries, additionalProperties, value) {
  const keys = guard_exports.Keys(value);
  for (const [regexp, _] of entries) {
    for (const key of keys) {
      if (!regexp.test(key)) {
        value[key] = FromType3(context, additionalProperties, value[key]);
      }
    }
  }
  return value;
}

// node_modules/typebox/build/value/convert/from_object.mjs
function FromProperties2(context, type, value) {
  const entries = guard_exports.EntriesRegExp(type.properties);
  const keys = guard_exports.Keys(value);
  for (const [regexp, property] of entries) {
    for (const key of keys) {
      if (!regexp.test(key) || IsOptionalUndefined(property, key, value))
        continue;
      value[key] = FromType3(context, property, value[key]);
    }
  }
  return guard_exports.HasPropertyKey(type, "additionalProperties") && guard_exports.IsObject(type.additionalProperties) ? FromAdditionalProperties(context, entries, type.additionalProperties, value) : value;
}
function FromObject3(context, type, value) {
  return guard_exports.IsObjectNotArray(value) ? FromProperties2(context, type, value) : value;
}

// node_modules/typebox/build/value/convert/from_record.mjs
function FromPatternProperties(context, type, value) {
  const entries = guard_exports.EntriesRegExp(type.patternProperties);
  const keys = guard_exports.Keys(value);
  for (const [regexp, schema] of entries) {
    for (const key of keys) {
      if (regexp.test(key)) {
        value[key] = FromType3(context, schema, value[key]);
      }
    }
  }
  return guard_exports.HasPropertyKey(type, "additionalProperties") && guard_exports.IsObject(type.additionalProperties) ? FromAdditionalProperties(context, entries, type.additionalProperties, value) : value;
}
function FromRecord2(context, type, value) {
  return guard_exports.IsObjectNotArray(value) ? FromPatternProperties(context, type, value) : value;
}

// node_modules/typebox/build/value/convert/from_ref.mjs
function FromRef2(context, type, value) {
  return guard_exports.HasPropertyKey(context, type.$ref) ? FromType3(context, context[type.$ref], value) : value;
}

// node_modules/typebox/build/value/convert/from_string.mjs
function FromString6(_context, _type, value) {
  const result = try_exports.TryString(value);
  return try_exports.IsOk(result) ? result.value : value;
}

// node_modules/typebox/build/value/convert/from_template_literal.mjs
function FromTemplateLiteral(context, type, value) {
  return FromType3(context, Evaluate(type), value);
}

// node_modules/typebox/build/value/convert/from_tuple.mjs
function FromTuple2(context, type, value) {
  if (!guard_exports.IsArray(value))
    return value;
  for (let index3 = 0; index3 < Math.min(type.items.length, value.length); index3++) {
    value[index3] = FromType3(context, type.items[index3], value[index3]);
  }
  return value;
}

// node_modules/typebox/build/value/convert/from_undefined.mjs
function FromUndefined(_context, _type, value) {
  const result = try_exports.TryUndefined(value);
  return try_exports.IsOk(result) ? result.value : value;
}

// node_modules/typebox/build/value/convert/from_union.mjs
function FromUnion2(context, type, value) {
  const matched = type.anyOf.some((type2) => Check2(context, type2, value));
  if (matched)
    return value;
  const candidates = type.anyOf.map((type2) => FromType3(context, type2, Clone2(value)));
  const selected = candidates.find((value2) => Check2(context, type, value2));
  return guard_exports.IsUndefined(selected) ? value : selected;
}

// node_modules/typebox/build/value/convert/from_void.mjs
function FromVoid(_context, _type, value) {
  const result = try_exports.TryUndefined(value);
  return try_exports.IsOk(result) ? void 0 : value;
}

// node_modules/typebox/build/value/convert/from_type.mjs
function FromType3(context, type, value) {
  return IsArray(type) ? FromArray3(context, type, value) : IsBigInt(type) ? FromBigInt5(context, type, value) : IsBoolean(type) ? FromBoolean5(context, type, value) : IsCyclic(type) ? FromCyclic2(context, type, value) : IsEnum(type) ? FromEnum(context, type, value) : IsInteger(type) ? FromInteger(context, type, value) : IsIntersect(type) ? FromIntersect2(context, type, value) : IsLiteral(type) ? FromLiteral(context, type, value) : IsNull(type) ? FromNull(context, type, value) : IsNumber(type) ? FromNumber4(context, type, value) : IsObject(type) ? FromObject3(context, type, value) : IsRecord(type) ? FromRecord2(context, type, value) : IsRef(type) ? FromRef2(context, type, value) : IsString(type) ? FromString6(context, type, value) : IsTemplateLiteral(type) ? FromTemplateLiteral(context, type, value) : IsTuple(type) ? FromTuple2(context, type, value) : IsUndefined(type) ? FromUndefined(context, type, value) : IsUnion(type) ? FromUnion2(context, type, value) : IsVoid(type) ? FromVoid(context, type, value) : value;
}

// node_modules/typebox/build/value/convert/convert.mjs
function Convert(...args) {
  const [context, type, value] = arguments_exports.Match(args, {
    3: (context2, type2, value2) => [context2, type2, value2],
    2: (type2, value2) => [{}, type2, value2]
  });
  return FromType3(context, type, value);
}

// node_modules/typebox/build/value/default/from_array.mjs
function FromArray4(context, type, value) {
  if (!guard_exports.IsArray(value))
    return value;
  for (let i = 0; i < value.length; i++) {
    value[i] = FromType4(context, type.items, value[i]);
  }
  return value;
}

// node_modules/typebox/build/value/default/from_cyclic.mjs
function FromCyclic3(context, type, value) {
  return FromType4({ ...context, ...type.$defs }, Ref(type.$ref), value);
}

// node_modules/typebox/build/value/default/from_default.mjs
function FromDefault(type, value) {
  if (!guard_exports.IsUndefined(value))
    return value;
  return guard_exports.IsFunction(type.default) ? type.default() : Clone2(type.default);
}

// node_modules/typebox/build/value/default/from_intersect.mjs
function FromIntersect3(context, type, value) {
  const instantiated = Instantiate(context, type);
  const evaluated = Evaluate(instantiated);
  return FromType4(context, evaluated, value);
}

// node_modules/typebox/build/value/default/from_object.mjs
function FromObject4(context, type, value) {
  if (!guard_exports.IsObject(value))
    return value;
  const knownPropertyKeys = guard_exports.Keys(type.properties);
  for (const key of knownPropertyKeys) {
    const propertyValue = FromType4(context, type.properties[key], value[key]);
    const isUnassignableUndefined = guard_exports.IsUndefined(propertyValue) && (IsOptional(type.properties[key]) || !guard_exports.HasPropertyKey(type.properties[key], "default"));
    if (isUnassignableUndefined)
      continue;
    value[key] = propertyValue;
  }
  if (!IsAdditionalProperties(type) || guard_exports.IsBoolean(type.additionalProperties))
    return value;
  for (const key of guard_exports.Keys(value)) {
    if (knownPropertyKeys.includes(key))
      continue;
    value[key] = FromType4(context, type.additionalProperties, value[key]);
  }
  return value;
}

// node_modules/typebox/build/value/default/from_record.mjs
function FromRecord3(context, type, value) {
  if (!guard_exports.IsObject(value))
    return value;
  const [recordKey, recordValue] = [new RegExp(RecordPattern(type)), RecordValue(type)];
  for (const key of guard_exports.Keys(value)) {
    if (!(recordKey.test(key) && IsDefault(recordValue)))
      continue;
    value[key] = FromType4(context, recordValue, value[key]);
  }
  if (!IsAdditionalProperties(type))
    return value;
  for (const key of guard_exports.Keys(value)) {
    if (recordKey.test(key))
      continue;
    value[key] = FromType4(context, type.additionalProperties, value[key]);
  }
  return value;
}

// node_modules/typebox/build/value/default/from_ref.mjs
function FromRef3(context, type, value) {
  return guard_exports.HasPropertyKey(context, type.$ref) ? FromType4(context, context[type.$ref], value) : value;
}

// node_modules/typebox/build/value/default/from_tuple.mjs
function FromTuple3(context, schema, value) {
  if (!guard_exports.IsArray(value))
    return value;
  const [items, max] = [schema.items, Math.max(schema.items.length, value.length)];
  for (let i = 0; i < max; i++) {
    if (i < items.length)
      value[i] = FromType4(context, items[i], value[i]);
  }
  return value;
}

// node_modules/typebox/build/value/default/from_union.mjs
function FromUnion3(context, schema, value) {
  for (const inner of schema.anyOf) {
    const result = FromType4(context, inner, Clone2(value));
    if (Check2(context, inner, result)) {
      return result;
    }
  }
  return value;
}

// node_modules/typebox/build/value/default/from_type.mjs
function FromType4(context, type, value) {
  const defaulted = IsDefault(type) ? FromDefault(type, value) : value;
  return IsArray(type) ? FromArray4(context, type, defaulted) : IsCyclic(type) ? FromCyclic3(context, type, defaulted) : IsIntersect(type) ? FromIntersect3(context, type, defaulted) : IsObject(type) ? FromObject4(context, type, defaulted) : IsRecord(type) ? FromRecord3(context, type, defaulted) : IsRef(type) ? FromRef3(context, type, defaulted) : IsTuple(type) ? FromTuple3(context, type, defaulted) : IsUnion(type) ? FromUnion3(context, type, defaulted) : defaulted;
}

// node_modules/typebox/build/value/default/default.mjs
function Default(...args) {
  const [context, type, value] = arguments_exports.Match(args, {
    3: (context2, type2, value2) => [context2, type2, value2],
    2: (type2, value2) => [{}, type2, value2]
  });
  return FromType4(context, type, value);
}

// node_modules/typebox/build/value/pipeline/pipeline.mjs
function Pipeline(pipeline) {
  return (...args) => {
    const [context, type, value] = arguments_exports.Match(args, {
      3: (context2, type2, value2) => [context2, type2, value2],
      2: (type2, value2) => [{}, type2, value2]
    });
    return pipeline.reduce((result, func) => func(context, type, result), value);
  };
}

// node_modules/typebox/build/value/codec/callback.mjs
function Decode2(_context, type, value) {
  return type["~codec"].decode(value);
}
function Encode2(_context, type, value) {
  return type["~codec"].encode(value);
}
function Callback(direction, context, type, value) {
  if (!IsCodec(type))
    return value;
  return guard_exports.IsEqual(direction, "Decode") ? Decode2(context, type, value) : Encode2(context, type, value);
}

// node_modules/typebox/build/value/codec/from_array.mjs
function Decode3(direction, context, type, value) {
  if (!guard_exports.IsArray(value))
    return value;
  for (let i = 0; i < value.length; i++) {
    value[i] = FromType5(direction, context, type.items, value[i]);
  }
  return Callback(direction, context, type, value);
}
function Encode3(direction, context, type, value) {
  const exterior = Callback(direction, context, type, value);
  if (!guard_exports.IsArray(exterior))
    return exterior;
  for (let i = 0; i < exterior.length; i++) {
    exterior[i] = FromType5(direction, context, type.items, exterior[i]);
  }
  return exterior;
}
function FromArray5(direction, context, type, value) {
  return guard_exports.IsEqual(direction, "Decode") ? Decode3(direction, context, type, value) : Encode3(direction, context, type, value);
}

// node_modules/typebox/build/value/codec/from_cyclic.mjs
function FromCyclic4(direction, context, type, value) {
  value = FromType5(direction, { ...context, ...type.$defs }, Ref(type.$ref), value);
  return Callback(direction, context, type, value);
}

// node_modules/typebox/build/value/codec/from_intersect.mjs
function MergeInteriors(interiors) {
  return interiors.reduce((results, interior) => ({ ...results, ...interior }), {});
}
function NonMatchingInterior(value, interiors) {
  for (const interior of interiors)
    if (!guard_exports.IsDeepEqual(value, interior))
      return interior;
  return value;
}
function Decode4(direction, context, type, value) {
  if (guard_exports.IsEqual(type.allOf.length, 0))
    return Callback(direction, context, type, value);
  const interiors = type.allOf.map((schema) => FromType5(direction, context, schema, Clean(schema, Clone2(value))));
  const structural = interiors.every((result) => guard_exports.IsObject(result));
  const exterior = structural ? MergeInteriors(interiors) : NonMatchingInterior(value, interiors);
  return Callback(direction, context, type, exterior);
}
function Encode4(direction, context, type, value) {
  if (guard_exports.IsEqual(type.allOf.length, 0))
    return Callback(direction, context, type, value);
  const exterior = Callback(direction, context, type, value);
  const interiors = type.allOf.map((schema) => FromType5(direction, context, schema, Clean(schema, Clone2(exterior))));
  const structural = interiors.every((result) => guard_exports.IsObject(result));
  if (structural)
    return MergeInteriors(interiors);
  return NonMatchingInterior(exterior, interiors);
}
function FromIntersect4(direction, context, type, value) {
  return guard_exports.IsEqual(direction, "Decode") ? Decode4(direction, context, type, value) : Encode4(direction, context, type, value);
}

// node_modules/typebox/build/value/codec/from_object.mjs
function Decode5(direction, context, type, value) {
  if (!guard_exports.IsObjectNotArray(value))
    return value;
  for (const key of guard_exports.Keys(type.properties)) {
    if (!guard_exports.HasPropertyKey(value, key) || IsOptionalUndefined(type.properties[key], key, value))
      continue;
    value[key] = FromType5(direction, context, type.properties[key], value[key]);
  }
  return Callback(direction, context, type, value);
}
function Encode5(direction, context, type, value) {
  const exterior = Callback(direction, context, type, value);
  if (!guard_exports.IsObjectNotArray(exterior))
    return exterior;
  for (const key of guard_exports.Keys(type.properties)) {
    if (!guard_exports.HasPropertyKey(exterior, key) || IsOptionalUndefined(type.properties[key], key, exterior))
      continue;
    exterior[key] = FromType5(direction, context, type.properties[key], exterior[key]);
  }
  return exterior;
}
function FromObject5(direction, context, type, value) {
  return guard_exports.IsEqual(direction, "Decode") ? Decode5(direction, context, type, value) : Encode5(direction, context, type, value);
}

// node_modules/typebox/build/value/codec/from_record.mjs
function Decode6(direction, context, type, value) {
  if (!guard_exports.IsObjectNotArray(value))
    return value;
  const regexp = new RegExp(RecordPattern(type));
  for (const key of guard_exports.Keys(value)) {
    if (!regexp.test(key))
      continue;
    value[key] = FromType5(direction, context, RecordValue(type), value[key]);
  }
  return Callback(direction, context, type, value);
}
function Encode6(direction, context, type, value) {
  const exterior = Callback(direction, context, type, value);
  if (!guard_exports.IsObjectNotArray(exterior))
    return exterior;
  const regexp = new RegExp(RecordPattern(type));
  for (const key of guard_exports.Keys(exterior)) {
    if (!regexp.test(key))
      continue;
    exterior[key] = FromType5(direction, context, RecordValue(type), exterior[key]);
  }
  return exterior;
}
function FromRecord4(direction, context, type, value) {
  return guard_exports.IsEqual(direction, "Decode") ? Decode6(direction, context, type, value) : Encode6(direction, context, type, value);
}

// node_modules/typebox/build/value/codec/from_ref.mjs
function ResolveRef2(direction, context, type, value) {
  return guard_exports.HasPropertyKey(context, type.$ref) ? FromType5(direction, context, context[type.$ref], value) : value;
}
function FromRef4(direction, context, type, value) {
  return guard_exports.IsEqual(direction, "Decode") ? Callback(direction, context, type, ResolveRef2(direction, context, type, value)) : ResolveRef2(direction, context, type, Callback(direction, context, type, value));
}

// node_modules/typebox/build/value/codec/from_tuple.mjs
function Decode7(direction, context, type, value) {
  if (!guard_exports.IsArray(value))
    return value;
  for (let i = 0; i < Math.min(type.items.length, value.length); i++) {
    value[i] = FromType5(direction, context, type.items[i], value[i]);
  }
  return Callback(direction, context, type, value);
}
function Encode7(direction, context, type, value) {
  const exterior = Callback(direction, context, type, value);
  if (!guard_exports.IsArray(exterior))
    return value;
  for (let i = 0; i < Math.min(type.items.length, exterior.length); i++) {
    exterior[i] = FromType5(direction, context, type.items[i], exterior[i]);
  }
  return exterior;
}
function FromTuple4(direction, context, type, value) {
  return guard_exports.IsEqual(direction, "Decode") ? Decode7(direction, context, type, value) : Encode7(direction, context, type, value);
}

// node_modules/typebox/build/value/codec/from_union.mjs
function Decode8(direction, context, type, value) {
  for (const schema of type.anyOf) {
    if (!Check2(context, schema, value))
      continue;
    const variant = FromType5(direction, context, schema, value);
    return Callback(direction, context, type, variant);
  }
  return value;
}
function Encode8(direction, context, type, value) {
  const exterior = Callback(direction, context, type, value);
  for (const schema of type.anyOf) {
    const variant = FromType5(direction, context, schema, Clone2(exterior));
    if (!Check2(context, schema, variant))
      continue;
    return variant;
  }
  return exterior;
}
function FromUnion4(direction, context, type, value) {
  return guard_exports.IsEqual(direction, "Decode") ? Decode8(direction, context, type, value) : Encode8(direction, context, type, value);
}

// node_modules/typebox/build/value/codec/from_type.mjs
function FromType5(direction, context, type, value) {
  return IsArray(type) ? FromArray5(direction, context, type, value) : IsCyclic(type) ? FromCyclic4(direction, context, type, value) : IsIntersect(type) ? FromIntersect4(direction, context, type, value) : IsObject(type) ? FromObject5(direction, context, type, value) : IsRecord(type) ? FromRecord4(direction, context, type, value) : IsRef(type) ? FromRef4(direction, context, type, value) : IsTuple(type) ? FromTuple4(direction, context, type, value) : IsUnion(type) ? FromUnion4(direction, context, type, value) : Callback(direction, context, type, value);
}

// node_modules/typebox/build/value/codec/decode.mjs
var DecodeError = class extends AssertError {
  constructor(value, errors) {
    super("Decode", value, errors);
  }
};
function Assert2(context, type, value) {
  if (!Check2(context, type, value))
    throw new DecodeError(value, Errors2(context, type, value));
  return value;
}
function DecodeUnsafe(context, type, value) {
  const sorted = settings_exports.Get().unionPrioritySort ? UnionPrioritySort(type) : type;
  return FromType5("Decode", context, sorted, value);
}
var Decoder = Pipeline([
  (_context, _type, value) => Clone2(value),
  (context, type, value) => Default(context, type, value),
  (context, type, value) => Convert(context, type, value),
  (context, type, value) => Clean(context, type, value),
  (context, type, value) => Assert2(context, type, value),
  (context, type, value) => DecodeUnsafe(context, type, value)
]);
function Decode9(...args) {
  const [context, type, value] = arguments_exports.Match(args, {
    3: (context2, type2, value2) => [context2, type2, value2],
    2: (type2, value2) => [{}, type2, value2]
  });
  return Decoder(context, type, value);
}

// node_modules/typebox/build/value/codec/encode.mjs
var EncodeError = class extends AssertError {
  constructor(value, errors) {
    super("Encode", value, errors);
  }
};
function Assert3(context, type, value) {
  if (!Check2(context, type, value))
    throw new EncodeError(value, Errors2(context, type, value));
  return value;
}
function EncodeUnsafe(context, type, value) {
  const sorted = settings_exports.Get().unionPrioritySort ? UnionPrioritySort(type) : type;
  return FromType5("Encode", context, sorted, value);
}
var Encoder = Pipeline([
  (_context, _type, value) => Clone2(value),
  (context, type, value) => EncodeUnsafe(context, type, value),
  (context, type, value) => Default(context, type, value),
  (context, type, value) => Convert(context, type, value),
  (context, type, value) => Clean(context, type, value),
  (context, type, value) => Assert3(context, type, value)
]);
function Encode9(...args) {
  const [context, type, value] = arguments_exports.Match(args, {
    3: (context2, type2, value2) => [context2, type2, value2],
    2: (type2, value2) => [{}, type2, value2]
  });
  return Encoder(context, type, value);
}

// node_modules/typebox/build/value/codec/has.mjs
function FromArray6(context, type) {
  return IsCodec(type) || FromType6(context, type.items);
}
function FromCyclic5(context, type) {
  return IsCodec(type) || FromRef5({ ...context, ...type.$defs }, Ref(type.$ref));
}
function FromIntersect5(context, type) {
  return IsCodec(type) || type.allOf.some((type2) => FromType6(context, type2));
}
function FromObject6(context, type) {
  return IsCodec(type) || guard_exports.Keys(type.properties).some((key) => {
    return FromType6(context, type.properties[key]);
  });
}
function FromRecord5(context, type) {
  return IsCodec(type) || FromType6(context, RecordValue(type));
}
function FromRef5(context, type) {
  if (visited.has(type.$ref))
    return false;
  visited.add(type.$ref);
  return IsCodec(type) || guard_exports.HasPropertyKey(context, type.$ref) && FromType6(context, context[type.$ref]);
}
function FromTuple5(context, type) {
  return IsCodec(type) || type.items.some((type2) => FromType6(context, type2));
}
function FromUnion5(context, type) {
  return IsCodec(type) || type.anyOf.some((type2) => FromType6(context, type2));
}
function FromType6(context, type) {
  return IsArray(type) ? FromArray6(context, type) : IsCyclic(type) ? FromCyclic5(context, type) : IsIntersect(type) ? FromIntersect5(context, type) : IsObject(type) ? FromObject6(context, type) : IsRecord(type) ? FromRecord5(context, type) : IsRef(type) ? FromRef5(context, type) : IsTuple(type) ? FromTuple5(context, type) : IsUnion(type) ? FromUnion5(context, type) : IsCodec(type);
}
var visited = /* @__PURE__ */ new Set();
function HasCodec(...args) {
  const [context, type] = arguments_exports.Match(args, {
    2: (context2, type2) => [context2, type2],
    1: (type2) => [{}, type2]
  });
  visited.clear();
  return FromType6(context, type);
}

// node_modules/typebox/build/value/create/error.mjs
var CreateError = class extends Error {
  constructor(type, message) {
    super(message);
    this.type = type;
  }
};

// node_modules/typebox/build/value/create/from_default.mjs
function FromDefault2(_context, schema) {
  return guard_exports.IsFunction(schema.default) ? schema.default(schema) : guard_exports.IsObject(schema.default) ? Clone2(schema.default) : schema.default;
}

// node_modules/typebox/build/value/create/from_array.mjs
function FromArray7(context, type) {
  if (IsUniqueItems(type) && !IsDefault(type))
    throw new CreateError(type, "Arrays with uniqueItems constraints must specify a default annotation");
  const length = IsMinItems(type) ? type.minItems : 0;
  return Array.from({ length }, () => FromType7(context, type.items));
}

// node_modules/typebox/build/value/create/from_bigint.mjs
function FromBigInt6(_context, type) {
  return IsExclusiveMinimum(type) ? BigInt(type.exclusiveMinimum) + BigInt(1) : IsMinimum(type) ? BigInt(type.minimum) : BigInt(0);
}

// node_modules/typebox/build/value/create/from_boolean.mjs
function FromBoolean6(_context, _type) {
  return false;
}

// node_modules/typebox/build/value/create/from_constructor.mjs
function FromConstructor(context, type) {
  const instanceType = FromType7(context, type.instanceType);
  return class {
    constructor() {
      Object.assign(this, instanceType);
    }
  };
}

// node_modules/typebox/build/value/create/from_cyclic.mjs
function FromCyclic6(context, type) {
  return FromType7({ ...context, ...type.$defs }, Ref(type.$ref));
}

// node_modules/typebox/build/value/create/from_enum.mjs
function FromEnum2(context, type) {
  return FromType7(context, Evaluate(type));
}

// node_modules/typebox/build/value/create/from_function.mjs
function FromFunction(context, type) {
  const returnType = FromType7(context, type.returnType);
  return () => returnType;
}

// node_modules/typebox/build/value/create/from_integer.mjs
function FromInteger2(_context, type) {
  return IsExclusiveMinimum(type) && guard_exports.IsNumber(type.exclusiveMinimum) ? type.exclusiveMinimum + 1 : IsMinimum(type) ? type.minimum : 0;
}

// node_modules/typebox/build/value/create/from_intersect.mjs
function FromIntersect6(context, type) {
  const instantiated = Instantiate(context, type);
  const evaluated = Evaluate(instantiated);
  return FromType7(context, evaluated);
}

// node_modules/typebox/build/value/create/from_literal.mjs
function FromLiteral2(_context, type) {
  return type.const;
}

// node_modules/typebox/build/value/create/from_never.mjs
function FromNever(_context, type) {
  throw new CreateError(type, "Cannot create TNever types");
}

// node_modules/typebox/build/value/create/from_null.mjs
function FromNull2(_context, _type) {
  return null;
}

// node_modules/typebox/build/value/create/from_number.mjs
function FromNumber5(_context, type) {
  return IsExclusiveMinimum(type) && guard_exports.IsNumber(type.exclusiveMinimum) ? type.exclusiveMinimum + 1 : IsMinimum(type) ? type.minimum : 0;
}

// node_modules/typebox/build/value/create/from_object.mjs
function FromObject7(context, type) {
  const required = guard_exports.IsUndefined(type.required) ? [] : type.required;
  return required.reduce((result, key) => {
    return { ...result, [key]: FromType7(context, type.properties[key]) };
  }, {});
}

// node_modules/typebox/build/value/create/from_record.mjs
function FromRecord6(_context, type) {
  if (IsMinProperties(type) && !IsDefault(type))
    throw new CreateError(type, "Record with the minProperties constraint must have a default annotation");
  return {};
}

// node_modules/typebox/build/value/create/from_ref.mjs
function FromRef6(context, type) {
  return guard_exports.HasPropertyKey(context, type.$ref) ? FromType7(context, context[type.$ref]) : (() => {
    throw new CreateError(type, "Unable to deref Ref");
  })();
}

// node_modules/typebox/build/value/create/from_string.mjs
function FromString7(_context, type) {
  const needsDefault = (IsPattern(type) || IsFormat(type)) && !IsDefault(type);
  if (needsDefault)
    throw Error("Strings with format or pattern constraints must specify default");
  const minLength = IsMinLength(type) ? type.minLength : 0;
  return "".padEnd(minLength);
}

// node_modules/typebox/build/value/create/from_symbol.mjs
function FromSymbol(_context, _type) {
  return Symbol();
}

// node_modules/typebox/build/value/create/from_template_literal.mjs
function FromTemplateLiteral2(context, type) {
  const decoded = TemplateLiteralDecode(type.pattern);
  if (IsString(decoded))
    throw new CreateError(type, "Unable to create TemplateLiteral due to infinite type expansion");
  return FromType7(context, decoded);
}

// node_modules/typebox/build/value/create/from_tuple.mjs
function FromTuple6(context, type) {
  return Array.from({ length: type.minItems }, (_, i) => FromType7(context, type.items[i]));
}

// node_modules/typebox/build/value/create/from_undefined.mjs
function FromUndefined2(_context, _type) {
  return void 0;
}

// node_modules/typebox/build/value/create/from_union.mjs
function FromUnion6(context, type) {
  if (guard_exports.IsEqual(type.anyOf.length, 0)) {
    throw Error("Unable to create Union with no variants");
  }
  return FromType7(context, type.anyOf[0]);
}

// node_modules/typebox/build/value/create/from_void.mjs
function FromVoid2(_context, _type) {
  return void 0;
}

// node_modules/typebox/build/value/create/from_type.mjs
function FromType7(context, type) {
  return (
    // -----------------------------------------------------
    // Default
    // -----------------------------------------------------
    IsDefault(type) ? FromDefault2(context, type) : (
      // -----------------------------------------------------
      // Types
      // -----------------------------------------------------
      IsArray(type) ? FromArray7(context, type) : IsBigInt(type) ? FromBigInt6(context, type) : IsBoolean(type) ? FromBoolean6(context, type) : IsConstructor(type) ? FromConstructor(context, type) : IsCyclic(type) ? FromCyclic6(context, type) : IsEnum(type) ? FromEnum2(context, type) : IsFunction(type) ? FromFunction(context, type) : IsInteger(type) ? FromInteger2(context, type) : IsIntersect(type) ? FromIntersect6(context, type) : IsLiteral(type) ? FromLiteral2(context, type) : IsNever(type) ? FromNever(context, type) : IsNull(type) ? FromNull2(context, type) : IsNumber(type) ? FromNumber5(context, type) : IsObject(type) ? FromObject7(context, type) : IsRecord(type) ? FromRecord6(context, type) : IsRef(type) ? FromRef6(context, type) : IsString(type) ? FromString7(context, type) : IsSymbol(type) ? FromSymbol(context, type) : IsTemplateLiteral(type) ? FromTemplateLiteral2(context, type) : IsTuple(type) ? FromTuple6(context, type) : IsUndefined(type) ? FromUndefined2(context, type) : IsUnion(type) ? FromUnion6(context, type) : IsVoid(type) ? FromVoid2(context, type) : void 0
    )
  );
}

// node_modules/typebox/build/value/create/create.mjs
function Create(...args) {
  const [context, type] = arguments_exports.Match(args, {
    2: (context2, type2) => [context2, type2],
    1: (type2) => [{}, type2]
  });
  return FromType7(context, type);
}

// node_modules/typebox/build/value/equal/equal.mjs
function Equal(left, right) {
  return guard_exports.IsDeepEqual(left, right);
}

// node_modules/typebox/build/value/hash/hash.mjs
function Hash(value) {
  return hash_exports.Hash(value);
}

// node_modules/typebox/build/value/parse/parse.mjs
var ParseError = class extends AssertError {
  constructor(value, errors) {
    super("Parse", value, errors);
  }
};
function Assert4(context, type, value) {
  if (!Check2(context, type, value))
    throw new ParseError(value, Errors2(context, type, value));
  return value;
}
var Parser = Pipeline([
  (_context, _type, value) => Clone2(value),
  (context, type, value) => Default(context, type, value),
  (context, type, value) => Convert(context, type, value),
  (context, type, value) => Clean(context, type, value),
  (context, type, value) => Assert4(context, type, value)
]);
function Parse(...args) {
  const [context, type, value] = arguments_exports.Match(args, {
    3: (context2, type2, value2) => [context2, type2, value2],
    2: (type2, value2) => [{}, type2, value2]
  });
  const checked = Check2(context, type, value);
  if (checked)
    return value;
  if (settings_exports.Get().correctiveParse)
    return Parser(context, type, value);
  throw new ParseError(value, Errors2(context, type, value));
}

// node_modules/typebox/build/value/delta/diff.mjs
function CreateUpdate(path, value) {
  return { type: "update", path, value };
}
function CreateInsert(path, value) {
  return { type: "insert", path, value };
}
function CreateDelete(path) {
  return { type: "delete", path };
}
function AssertCanDiffObject(value) {
  if (guard_exports.IsObject(value) && guard_exports.IsEqual(guard_exports.Symbols(value).length, 0))
    return;
  throw new Error("Cannot create diffs for objects with symbols keys");
}
function* FromObject8(path, left, right) {
  if (!guard_exports.IsObject(right) || guard_exports.IsArray(right))
    return yield CreateUpdate(path, right);
  AssertCanDiffObject(left);
  AssertCanDiffObject(right);
  const leftKeys = guard_exports.Keys(left);
  const rightKeys = guard_exports.Keys(right);
  for (const key of rightKeys) {
    if (guard_exports.HasPropertyKey(left, key))
      continue;
    if (guard_exports.IsUnsafePropertyKey(key))
      continue;
    yield CreateInsert(`${path}/${key}`, right[key]);
  }
  for (const key of leftKeys) {
    if (!guard_exports.HasPropertyKey(right, key))
      continue;
    if (guard_exports.IsUnsafePropertyKey(key))
      continue;
    if (Equal(left, right))
      continue;
    yield* FromValue2(`${path}/${key}`, left[key], right[key]);
  }
  for (const key of leftKeys) {
    if (guard_exports.HasPropertyKey(right, key))
      continue;
    if (guard_exports.IsUnsafePropertyKey(key))
      continue;
    yield CreateDelete(`${path}/${key}`);
  }
}
function* FromArray8(path, left, right) {
  if (!guard_exports.IsArray(right))
    return yield CreateUpdate(path, right);
  for (let i = 0; i < Math.min(left.length, right.length); i++) {
    yield* FromValue2(`${path}/${i}`, left[i], right[i]);
  }
  for (let i = 0; i < right.length; i++) {
    if (i < left.length)
      continue;
    yield CreateInsert(`${path}/${i}`, right[i]);
  }
  for (let i = left.length - 1; i >= 0; i--) {
    if (i < right.length)
      continue;
    yield CreateDelete(`${path}/${i}`);
  }
}
function* FromTypedArray(path, left, right) {
  const typeLeft = globalThis.Object.getPrototypeOf(left).constructor.name;
  const typeRight = globalThis.Object.getPrototypeOf(right).constructor.name;
  const predicate = globals_exports.IsTypeArray(right) && guard_exports.IsEqual(left.length, right.length) && guard_exports.IsEqual(typeLeft, typeRight);
  if (predicate) {
    for (let index3 = 0; index3 < Math.min(left.length, right.length); index3++) {
      yield* FromValue2(`${path}/${index3}`, left[index3], right[index3]);
    }
  } else {
    return yield CreateUpdate(path, right);
  }
}
function* FromUnknown(path, left, right) {
  if (left === right)
    return;
  yield CreateUpdate(path, right);
}
function* FromValue2(path, left, right) {
  return globals_exports.IsTypeArray(left) ? yield* FromTypedArray(path, left, right) : guard_exports.IsArray(left) ? yield* FromArray8(path, left, right) : guard_exports.IsObject(left) ? yield* FromObject8(path, left, right) : yield* FromUnknown(path, left, right);
}
function Diff(current, next) {
  return [...FromValue2("", current, next)];
}

// node_modules/typebox/build/value/delta/edit.mjs
var Insert = _Object_({
  type: Literal("insert"),
  path: String2(),
  value: Unknown()
});
var Update = Object({
  type: Literal("update"),
  path: String2(),
  value: Unknown()
});
var Delete2 = _Object_({
  type: Literal("delete"),
  path: String2()
});
var Edit = Union([Insert, Update, Delete2]);

// node_modules/typebox/build/value/delta/patch.mjs
function IsRoot(edits) {
  return edits.length > 0 && edits[0].path === "" && edits[0].type === "update";
}
function IsEmpty(edits) {
  return edits.length === 0;
}
function Patch(current, edits) {
  if (IsRoot(edits))
    return Clone2(edits[0].value);
  if (IsEmpty(edits))
    return Clone2(current);
  const clone = Clone2(current);
  for (const edit of edits) {
    switch (edit.type) {
      case "insert": {
        pointer_exports.Set(clone, edit.path, edit.value);
        break;
      }
      case "update": {
        pointer_exports.Set(clone, edit.path, edit.value);
        break;
      }
      case "delete": {
        pointer_exports.Delete(clone, edit.path);
        break;
      }
    }
  }
  return clone;
}

// node_modules/typebox/build/value/shared/union_score_select.mjs
function Deref(context, type, value) {
  return IsRef(type) ? guard_exports.HasPropertyKey(context, type.$ref) ? Deref(context, context[type.$ref], value) : (() => {
    throw new Error("Unable to Deref target");
  })() : type;
}
function ScoreVariant(context, type, value) {
  if (!(IsObject(type) && guard_exports.IsObject(value)))
    return 0;
  const keys = guard_exports.Keys(value);
  const entries = guard_exports.Entries(type.properties);
  return entries.reduce((result, [key, schema]) => {
    const literal = IsLiteral(schema) && guard_exports.IsEqual(schema.const, value[key]) ? 100 : 0;
    const checks = Check2(context, schema, value[key]) ? 10 : 0;
    const exists = keys.includes(key) ? 1 : 0;
    return result + (literal + checks + exists);
  }, 0);
}
function UnionScoreSelect(context, type, value) {
  const schemas = type.anyOf.map((schema) => Deref(context, schema, value));
  let [select, best] = [schemas[0], 0];
  for (const schema of schemas) {
    const score = ScoreVariant(context, schema, value);
    if (score > best) {
      select = schema;
      best = score;
    }
  }
  return select;
}

// node_modules/typebox/build/value/repair/error.mjs
var RepairError = class extends Error {
  constructor(context, type, value, message) {
    super(message);
    this.context = context;
    this.type = type;
    this.value = value;
  }
};

// node_modules/typebox/build/value/repair/from_array.mjs
function MakeUnique(values) {
  const [hashes, result] = [/* @__PURE__ */ new Set(), []];
  for (const value of values) {
    const hash = Hash(value);
    if (hashes.has(hash))
      continue;
    hashes.add(hash);
    result.push(value);
  }
  return result;
}
function FromArray9(context, type, value) {
  if (Check2(context, type, value))
    return value;
  const created = guard_exports.IsArray(value) ? value : Create(context, type);
  const minimum = IsMinItems(type) && created.length < type.minItems ? [...created, ...Array.from({ length: type.minItems - created.length }, () => Create(context, type))] : created;
  const maximum = IsMaxItems(type) && minimum.length > type.maxItems ? minimum.slice(0, type.maxItems) : minimum;
  const repaired = maximum.map((value2) => FromType8(context, type.items, value2));
  if (!IsUniqueItems(type) || IsUniqueItems(type) && !guard_exports.IsEqual(type.uniqueItems, true))
    return repaired;
  const unique = MakeUnique(repaired);
  if (!Check2(context, type, unique))
    throw new RepairError(context, type, value, "Failed to repair Array due to uniqueItems constraint");
  return unique;
}

// node_modules/typebox/build/value/repair/from_enum.mjs
function FromEnum3(context, type, value) {
  return FromType8(context, Evaluate(type), value);
}

// node_modules/typebox/build/value/repair/from_intersect.mjs
function FromIntersect7(context, type, value) {
  const instantiated = Instantiate(context, type);
  const evaluated = Evaluate(instantiated);
  return FromType8(context, evaluated, value);
}

// node_modules/typebox/build/value/repair/from_object.mjs
function FromObject9(context, type, value) {
  if (Check2(context, type, value))
    return value;
  if (!guard_exports.IsObjectNotArray(value))
    return Create(context, type);
  const required = new Set(guard_exports.IsUndefined(type.required) ? [] : type.required);
  const result = {};
  for (const [key, schema] of guard_exports.Entries(type.properties)) {
    if (!required.has(key) && guard_exports.IsUndefined(value[key]))
      continue;
    result[key] = key in value ? FromType8(context, schema, value[key]) : Create(context, schema);
  }
  const evaluatedKeys = guard_exports.Keys(type.properties);
  if (IsAdditionalProperties(type) && guard_exports.IsObject(type.additionalProperties)) {
    for (const key of guard_exports.Keys(value)) {
      if (evaluatedKeys.includes(key))
        continue;
      result[key] = FromType8(context, type.additionalProperties, value[key]);
    }
  }
  return result;
}

// node_modules/typebox/build/value/repair/from_record.mjs
function FromRecord7(context, type, value) {
  if (Check2(context, type, value))
    return value;
  if (guard_exports.IsNull(value) || !guard_exports.IsObject(value) || guard_exports.IsArray(value))
    return Create(context, type);
  const recordKey = new RegExp(RecordPattern(type));
  const recordValue = RecordValue(type);
  const evaluatedKeys = /* @__PURE__ */ new Set();
  const result = {};
  for (const [key, value_] of guard_exports.Entries(value)) {
    if (!recordKey.test(key))
      continue;
    result[key] = FromType8(context, recordValue, value_);
    evaluatedKeys.add(key);
  }
  if (IsAdditionalProperties(type)) {
    for (const key of guard_exports.Keys(value)) {
      if (evaluatedKeys.has(key))
        continue;
      result[key] = FromType8(context, type.additionalProperties, value[key]);
    }
  }
  return result;
}

// node_modules/typebox/build/value/repair/from_ref.mjs
function FromRef7(context, type, value) {
  return guard_exports.HasPropertyKey(context, type.$ref) ? FromType8(context, context[type.$ref], value) : (() => {
    throw new RepairError(context, type, value, "Unable to de-reference target type");
  })();
}

// node_modules/typebox/build/value/repair/from_template_literal.mjs
function FromTemplateLiteral3(context, type, value) {
  const decoded = TemplateLiteralDecode(type.pattern);
  return FromType8(context, decoded, value);
}

// node_modules/typebox/build/value/repair/from_tuple.mjs
function FromTuple7(context, schema, value) {
  if (Check2(context, schema, value))
    return value;
  if (!guard_exports.IsArray(value))
    return Create(context, schema);
  return schema.items.map((schema2, index3) => FromType8(context, schema2, value[index3]));
}

// node_modules/typebox/build/value/repair/from_union.mjs
function RepairUnion(context, type, value) {
  const union = Union(Flatten(type.anyOf));
  const schema = UnionScoreSelect(context, union, value);
  return FromType8(context, schema, value);
}
function FromUnion7(context, type, value) {
  if (Check2(context, type, value))
    return Clone2(value);
  if (IsDefault(type))
    return Create(context, type);
  return RepairUnion(context, type, value);
}

// node_modules/typebox/build/value/repair/from_unknown.mjs
function FromUnknown2(context, type, value) {
  if (Check2(context, type, value))
    return value;
  const converted = Convert(context, type, value);
  if (Check2(context, type, converted))
    return converted;
  return Create(context, type);
}

// node_modules/typebox/build/value/repair/from_type.mjs
function AssertRepairableValue(context, type, value) {
  const unsupported = globals_exports.IsDate(value) || globals_exports.IsMap(value) || globals_exports.IsSet(value) || globals_exports.IsTypeArray(value) || guard_exports.IsConstructor(value) || guard_exports.IsFunction(value);
  if (unsupported) {
    throw new RepairError(context, type, value, "Value is not repairable");
  }
}
function AssertRepairableType(context, type, value) {
  const unsupported = IsConstructor(type) || IsFunction(type) || IsNever(type);
  if (unsupported) {
    throw new RepairError(context, type, value, "Type is not repairable");
  }
}
function CreateWhenUndefined(context, type, value) {
  return guard_exports.IsUndefined(value) && !IsUndefined(type) ? Create(context, type) : value;
}
function FinalizeRepair(context, type, repaired) {
  return IsRefine(type) ? Check2(context, type, repaired) ? repaired : Create(context, type) : repaired;
}
function FromType8(context, type, value) {
  AssertRepairableValue(context, type, value);
  AssertRepairableType(context, type, value);
  const candidate = CreateWhenUndefined(context, type, value);
  const repaired = IsArray(type) ? FromArray9(context, type, candidate) : IsEnum(type) ? FromEnum3(context, type, candidate) : IsIntersect(type) ? FromIntersect7(context, type, candidate) : IsObject(type) ? FromObject9(context, type, candidate) : IsRecord(type) ? FromRecord7(context, type, candidate) : IsRef(type) ? FromRef7(context, type, candidate) : IsTemplateLiteral(type) ? FromTemplateLiteral3(context, type, candidate) : IsTuple(type) ? FromTuple7(context, type, candidate) : IsUnion(type) ? FromUnion7(context, type, candidate) : FromUnknown2(context, type, candidate);
  return FinalizeRepair(context, type, repaired);
}

// node_modules/typebox/build/value/repair/repair.mjs
function Repair(...args) {
  const [context, type, value] = arguments_exports.Match(args, {
    3: (context2, type2, value2) => [context2, type2, value2],
    2: (type2, value2) => [{}, type2, value2]
  });
  const repaired = FromType8(context, type, value);
  Assert(context, type, repaired);
  return repaired;
}

// node_modules/typebox/build/value/value.mjs
var value_exports = {};
__export(value_exports, {
  Assert: () => Assert,
  Check: () => Check2,
  Clean: () => Clean,
  Clone: () => Clone2,
  Convert: () => Convert,
  Create: () => Create,
  Decode: () => Decode9,
  Default: () => Default,
  Diff: () => Diff,
  Encode: () => Encode9,
  Equal: () => Equal,
  Errors: () => Errors2,
  HasCodec: () => HasCodec,
  Hash: () => Hash,
  Parse: () => Parse,
  Patch: () => Patch,
  Pointer: () => pointer_exports,
  Repair: () => Repair
});

export {
  pointer_exports,
  Build,
  Check2 as Check,
  Errors2 as Errors,
  AssertError,
  Assert,
  Clone2 as Clone,
  UnionPrioritySort,
  Clean,
  IsOptionalUndefined,
  Convert,
  Default,
  Pipeline,
  DecodeError,
  DecodeUnsafe,
  Decode9 as Decode,
  EncodeError,
  EncodeUnsafe,
  Encode9 as Encode,
  HasCodec,
  CreateError,
  Create,
  Equal,
  Hash,
  ParseError,
  Parser,
  Parse,
  Diff,
  Insert,
  Update,
  Delete2 as Delete,
  Edit,
  Patch,
  UnionScoreSelect,
  Repair,
  value_exports
};
