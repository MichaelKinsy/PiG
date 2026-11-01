// deno-fmt-ignore-file
var __classPrivateFieldGet = (this && this.__classPrivateFieldGet) || function (receiver, state, kind, f) {
    if (kind === "a" && !f) throw new TypeError("Private accessor was defined without a getter");
    if (typeof state === "function" ? receiver !== state || !f : !state.has(receiver)) throw new TypeError("Cannot read private member from an object whose class did not declare it");
    return kind === "m" ? f : kind === "a" ? f.call(receiver) : f ? f.value : state.get(receiver);
};
var _Stack_instances, _Stack_StackFrame, _Stack_ApplyRefResult, _Stack_BuildBase, _Stack_ResourceBaseURL, _Stack_ReferenceBaseURL, _Stack_LexicalSchema, _Stack_RegisterResourceAnchorArray, _Stack_UnregisterResourceAnchorArray, _Stack_RegisterResourceAnchors, _Stack_UnregisterResourceAnchors, _Stack_RegisterResource, _Stack_UnregisterResource, _Stack_EnterResolvedResource, _Stack_ExitResolvedResource;
import * as Schema from '../types/index.mjs';
import { Guard } from '../../guard/index.mjs';
import { Resolve } from '../resolve/index.mjs';
// --------------------------------------------------------------------------
// Stack
//
// Tracks traversal state ($ids, $anchors, stack frames) and applies scope
// updates. Reference resolution rules ($ref, $dynamicRef) are delegated to
// the Resolve functions using state snapshots (Resolve.Scope).
// --------------------------------------------------------------------------
export class Stack {
    constructor(context, schema) {
        _Stack_instances.add(this);
        this.context = context;
        this.schema = schema;
        this.ids = [];
        this.resourceIds = [];
        this.anchors = [];
        this.recursiveAnchors = [];
        this.dynamicAnchors = [];
        this.retrievedResources = new Map();
        this.retrievedFrames = [];
        this.resolvedResources = new Map();
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
        if (!Schema.IsSchemaObject(schema))
            return;
        if (Schema.IsId(schema))
            __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_RegisterResource).call(this, schema);
        if (Schema.IsAnchor(schema))
            this.anchors.push(schema);
        if (Schema.IsRecursiveAnchorTrue(schema))
            this.recursiveAnchors.push(schema);
        if (Schema.IsDynamicAnchor(schema))
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
        if (!Schema.IsSchemaObject(schema))
            return;
        if (Schema.IsId(schema))
            __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_UnregisterResource).call(this, schema);
        if (Schema.IsAnchor(schema))
            this.anchors.pop();
        if (Schema.IsRecursiveAnchorTrue(schema))
            this.recursiveAnchors.pop();
        if (Schema.IsDynamicAnchor(schema))
            this.dynamicAnchors.pop();
        if (this.retrievedResources.has(schema))
            this.retrievedFrames.pop();
        __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_ExitResolvedResource).call(this, schema);
    }
    // ----------------------------------------------------------------
    // Ref
    // ----------------------------------------------------------------
    Ref(ref) {
        const result = Resolve.ResolveRef(__classPrivateFieldGet(this, _Stack_instances, "m", _Stack_StackFrame).call(this), ref);
        __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_ApplyRefResult).call(this, result);
        return result.schema;
    }
    // ----------------------------------------------------------------
    // RecursiveRef
    // ----------------------------------------------------------------
    RecursiveRef(recursiveRef) {
        const result = Resolve.ResolveRecursiveRef(__classPrivateFieldGet(this, _Stack_instances, "m", _Stack_StackFrame).call(this), recursiveRef);
        if (result)
            this.pendingResource = true;
        return result;
    }
    // ----------------------------------------------------------------
    // DynamicRef
    // ----------------------------------------------------------------
    DynamicRef(dynamicRef) {
        const result = Resolve.ResolveDynamicRef(__classPrivateFieldGet(this, _Stack_instances, "m", _Stack_StackFrame).call(this), dynamicRef);
        if (result)
            this.pendingResource = true;
        return result;
    }
}
_Stack_instances = new WeakSet(), _Stack_StackFrame = function _Stack_StackFrame() {
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
}, _Stack_ApplyRefResult = function _Stack_ApplyRefResult(result) {
    if (!Guard.IsUndefined(result.schema))
        this.pendingResource = true;
    if (!Guard.IsUndefined(result.resolvedResource)) {
        __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_EnterResolvedResource).call(this, result.resolvedResource.target, result.resolvedResource.resource);
    }
    if (!Guard.IsUndefined(result.retrievedResource)) {
        this.retrievedResources.set(result.retrievedResource.target, {
            base: result.retrievedResource.base,
            root: result.retrievedResource.root
        });
    }
}, _Stack_BuildBase = function _Stack_BuildBase(stack) {
    const frame = this.retrievedFrames[this.retrievedFrames.length - 1];
    const base = frame ? new URL(frame.base) : new URL(Resolve.DefaultBase);
    const scoped = frame ? stack.slice(frame.idDepth) : stack;
    return scoped.reduce((result, schema) => new URL(schema.$id, result), base).href;
}, _Stack_ResourceBaseURL = function _Stack_ResourceBaseURL() {
    const frame = this.retrievedFrames[this.retrievedFrames.length - 1];
    if (!frame)
        return __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_BuildBase).call(this, this.resourceIds);
    return this.resourceIds.slice(frame.resourceDepth).reduce((result, schema) => new URL(schema.$id, result), new URL(frame.base)).href;
}, _Stack_ReferenceBaseURL = function _Stack_ReferenceBaseURL() {
    if (this.retrievedFrames.length > 0)
        return __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_ResourceBaseURL).call(this);
    const lexical = this.ids[this.ids.length - 1];
    if (lexical && !/^[A-Za-z][A-Za-z0-9+.-]*:/.test(lexical.$id))
        return this.LexicalBaseURL();
    return __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_ResourceBaseURL).call(this);
}, _Stack_LexicalSchema = function _Stack_LexicalSchema() {
    const frame = this.retrievedFrames[this.retrievedFrames.length - 1];
    if (frame)
        return this.ids.length > frame.idDepth ? this.ids[this.ids.length - 1] : frame.schema;
    return this.ids.length > 0 ? this.ids[this.ids.length - 1] : this.schema;
}, _Stack_RegisterResourceAnchorArray = function _Stack_RegisterResourceAnchorArray(schema) {
    schema.forEach((schema) => __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_RegisterResourceAnchors).call(this, schema, false));
}, _Stack_UnregisterResourceAnchorArray = function _Stack_UnregisterResourceAnchorArray(schema) {
    schema.forEach((schema) => __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_UnregisterResourceAnchors).call(this, schema, false));
}, _Stack_RegisterResourceAnchors = function _Stack_RegisterResourceAnchors(schema, isRoot = true) {
    if (Schema.IsSchemaBoolean(schema))
        return;
    if (Guard.IsArray(schema))
        return __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_RegisterResourceAnchorArray).call(this, schema);
    if (!Schema.IsSchemaObject(schema))
        return;
    const current = schema;
    if (!isRoot && Schema.IsId(current))
        return;
    if (!isRoot && Schema.IsDynamicAnchor(current))
        this.dynamicAnchors.push(current);
    for (const key of Guard.Keys(current))
        __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_RegisterResourceAnchors).call(this, current[key], false);
}, _Stack_UnregisterResourceAnchors = function _Stack_UnregisterResourceAnchors(schema, isRoot = true) {
    if (Schema.IsSchemaBoolean(schema))
        return;
    if (Guard.IsArray(schema))
        return __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_UnregisterResourceAnchorArray).call(this, schema);
    if (!Schema.IsSchemaObject(schema))
        return;
    const current = schema;
    if (!isRoot && Schema.IsId(current))
        return;
    if (!isRoot && Schema.IsDynamicAnchor(current))
        this.dynamicAnchors.pop();
    for (const key of Guard.Keys(current))
        __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_UnregisterResourceAnchors).call(this, current[key], false);
}, _Stack_RegisterResource = function _Stack_RegisterResource(schema) {
    this.ids.push(schema);
    const isResource = this.pendingResource;
    this.pendingResource = false;
    if (isResource)
        this.resourceIds.push(schema);
    __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_RegisterResourceAnchors).call(this, schema, true);
}, _Stack_UnregisterResource = function _Stack_UnregisterResource(schema) {
    this.ids.pop();
    const isResource = this.resourceIds.length > 0 && Guard.IsEqual(this.resourceIds[this.resourceIds.length - 1], schema);
    if (isResource)
        this.resourceIds.pop();
    __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_UnregisterResourceAnchors).call(this, schema, true);
}, _Stack_EnterResolvedResource = function _Stack_EnterResolvedResource(target, resource) {
    __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_RegisterResource).call(this, resource);
    this.resolvedResources.set(target, resource);
}, _Stack_ExitResolvedResource = function _Stack_ExitResolvedResource(target) {
    if (!this.resolvedResources.has(target))
        return;
    const resource = this.resolvedResources.get(target);
    __classPrivateFieldGet(this, _Stack_instances, "m", _Stack_UnregisterResource).call(this, resource);
    this.resolvedResources.delete(target);
};
