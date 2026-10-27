package ai

// Tool argument validation with TypeBox refinements (Type.Refine). Pi's validateToolArguments (validation.ts:317) runs a tool's schema through TypeBox's Compile: a refinement
// is a `{check, error}` pair on a schema node (the hidden `~refine` list), which TypeBox calls while it validates. A native extension's tool keeps its refinements in the extension
// process, so the host runs TypeBox's own traversal and asks the extension for each predicate call, in the order TypeBox makes them:
//
//   - Check (Compile's generated code, typebox/build/schema/engine/schema.mjs BuildSchema): an && chain of the node's keywords in a fixed order, a node's refinements last
//     (BuildRefine: Every, so the first failing refinement stops the rest), properties and items visited with Every, anyOf an || chain (BuildAnyOfFast), allOf an && chain
//     (BuildAllOfFast), oneOf evaluates every branch (BuildOneOfFast).
//   - Errors (ErrorSchema, same file): every keyword of a node is evaluated, and the node's refinements run only when the node reported no error (ErrorRefine: EveryAll, so every
//     check runs, and error(value) words each failing one).
//
// A schema with no refinement runs none of this: ValidateToolArgumentsRefined without a refiner is ValidateToolArgumentsJSON.

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

// RefinementSite is a schema node that carries refinements: its path in the schema (the keywords that lead to it, as JSON pointer segments: "/properties/a", "/anyOf/1", "/items")
// and how many refinements it has.
type RefinementSite struct {
	Path  string
	Count int
}

// RefineOutcome is what a refinement answers: whether the value passed and, when the call asked for it and the value failed, the refinement's error message.
type RefineOutcome struct {
	OK      bool
	Message string
}

// Refiner runs the refinements of a tool whose schema carries them. Refine runs the check of refinement index of the node at path on value (the arguments after conversion, as Pi
// hands them to TypeBox) and, when withMessage is set and the check fails, the refinement's error function. An error is what a thrown JavaScript error is: it ends the validation and
// becomes the tool call's error. ctx ends with the tool call.
type Refiner interface {
	RefinementSites() []RefinementSite
	Refine(ctx context.Context, path string, index int, value any, withMessage bool) (RefineOutcome, error)
}

// refineAbort carries an error out of the traversal, as a thrown JavaScript error unwinds validateToolArguments.
type refineAbort struct{ err error }

// refineRun is one validation with refinements; the root of the parsed schema points at it for the duration (the caller holds the entry's lock).
type refineRun struct {
	ctx     context.Context
	refiner Refiner
	sites   map[string]int
	order   schemaObjectOrder
	hotMemo map[*argumentSchema]bool
	// errorCount is how many errors the error context in progress holds (TypeBox's ErrorContext capacity is typeboxMaxErrors).
	errorCount int
}

func newRefineRun(ctx context.Context, refiner Refiner, sites []RefinementSite) *refineRun {
	run := &refineRun{ctx: ctx, refiner: refiner, sites: map[string]int{}, hotMemo: map[*argumentSchema]bool{}}
	for _, site := range sites {
		if site.Count > 0 {
			run.sites[site.Path] += site.Count
		}
	}
	return run
}

func (r *refineRun) call(path string, index int, value any, withMessage bool) RefineOutcome {
	outcome, err := r.refiner.Refine(r.ctx, path, index, value, withMessage)
	if err != nil {
		panic(refineAbort{err})
	}
	return outcome
}

// hot reports whether the node, or a node below it, carries refinements: only those are traversed in TypeBox's order here, the rest are checked as a whole.
func (r *refineRun) hot(s *argumentSchema) bool {
	if hot, ok := r.hotMemo[s]; ok {
		return hot
	}
	r.hotMemo[s] = false // a cycle through $ref is not hot by itself
	hot := false
	for path := range r.sites {
		if s.path == "" || path == s.path || strings.HasPrefix(path, s.path+"/") {
			hot = true
			break
		}
	}
	if !hot {
		if ref := s.text("$ref"); ref != "" {
			if target := s.resolveArgumentRef(ref); target != nil && target != s {
				hot = r.hot(target)
			}
		}
	}
	r.hotMemo[s] = hot
	return hot
}

// structural is a check without refinements: the node as the compiled JSON Schema validator sees it.
func (s *argumentSchema) structural(value any) bool {
	validator, err := s.validator()
	return err == nil && validator.Validate(value) == nil
}

// leaf checks one keyword of the node on its own. The keyword's result does not depend on the node's other keywords.
func (s *argumentSchema) leaf(keyword string, value any) bool {
	if _, present := s.fields[keyword]; !present {
		return true
	}
	cacheKey := "\x00kw:" + keyword
	holder, ok := s.children[cacheKey]
	if !ok {
		holder = &argumentSchema{doc: map[string]any{keyword: s.value(keyword)}, children: map[string]*argumentSchema{}, root: s.root}
		s.children[cacheKey] = holder
	}
	return holder.structural(value)
}

// additionalPropertiesLeaf checks `additionalProperties` when it is not a schema (false): it needs the node's properties and patterns to know which members are additional.
func (s *argumentSchema) additionalPropertiesLeaf(value any) bool {
	cacheKey := "\x00kw:additionalProperties:keys"
	holder, ok := s.children[cacheKey]
	if !ok {
		doc := map[string]any{"additionalProperties": s.value("additionalProperties")}
		if properties, isMap := s.value("properties").(map[string]any); isMap {
			names := make(map[string]any, len(properties))
			for name := range properties {
				names[name] = true
			}
			doc["properties"] = names
		}
		if patterns, isMap := s.value("patternProperties").(map[string]any); isMap {
			names := make(map[string]any, len(patterns))
			for pattern := range patterns {
				names[pattern] = true
			}
			doc["patternProperties"] = names
		}
		holder = &argumentSchema{doc: doc, children: map[string]*argumentSchema{}, root: s.root}
		s.children[cacheKey] = holder
	}
	return holder.structural(value)
}

func (s *argumentSchema) has(keyword string) bool {
	_, ok := s.fields[keyword]
	return ok
}

// checkNode is TypeBox's Check of a node that is hot: its keywords in the order of BuildSchema, with every predicate call TypeBox makes on the way. pointer is the JSON pointer of the
// value in the arguments, for the member order of the object's keys.
func (r *refineRun) checkNode(s *argumentSchema, value any, pointer string) bool {
	if !r.hot(s) {
		return s.structural(value)
	}
	if _, isBool := s.doc.(bool); isBool {
		return s.structural(value)
	}
	if s.has("type") && !s.leaf("type", value) {
		return false
	}
	if object, isObject := value.(map[string]any); isObject {
		if !r.checkObjectKeywords(s, object, pointer) {
			return false
		}
	}
	if items, isArray := value.([]any); isArray {
		if !r.checkArrayKeywords(s, items, pointer) {
			return false
		}
	}
	if text, isString := value.(string); isString {
		for _, keyword := range []string{"maxLength", "minLength", "format", "pattern"} {
			if !s.leaf(keyword, text) {
				return false
			}
		}
	}
	if _, isNumber := value.(float64); isNumber {
		for _, keyword := range []string{"exclusiveMaximum", "exclusiveMinimum", "maximum", "minimum", "multipleOf"} {
			if !s.leaf(keyword, value) {
				return false
			}
		}
	}
	if ref := s.text("$ref"); ref != "" {
		target := s.resolveArgumentRef(ref)
		if target == nil || !r.checkNode(target, value, pointer) {
			return false
		}
	}
	for _, keyword := range []string{"const", "enum"} {
		if !s.leaf(keyword, value) {
			return false
		}
	}
	if condition := s.child("if"); condition != nil {
		branch := s.child("else")
		if r.checkNode(condition, value, pointer) {
			branch = s.child("then")
		}
		if branch != nil && !r.checkNode(branch, value, pointer) {
			return false
		}
	}
	if child := s.child("not"); child != nil && r.checkNode(child, value, pointer) {
		return false
	}
	for _, child := range s.list("allOf") {
		if !r.checkNode(child, value, pointer) {
			return false
		}
	}
	if children := s.list("anyOf"); children != nil {
		matched := false
		for _, child := range children {
			if r.checkNode(child, value, pointer) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if children := s.list("oneOf"); children != nil {
		count := 0
		for _, child := range children {
			if r.checkNode(child, value, pointer) {
				count++
			}
		}
		if count != 1 {
			return false
		}
	}
	for _, keyword := range []string{"unevaluatedItems", "unevaluatedProperties"} {
		if !s.leaf(keyword, value) {
			return false
		}
	}
	count := r.sites[s.path]
	for index := range count {
		if !r.call(s.path, index, value, false).OK {
			return false
		}
	}
	return true
}

func (r *refineRun) checkObjectKeywords(s *argumentSchema, object map[string]any, pointer string) bool {
	if !s.leaf("required", object) {
		return false
	}
	properties := s.child("properties")
	patterns := s.child("patternProperties")
	if s.has("additionalProperties") {
		if additional := s.child("additionalProperties"); additional != nil && !isBooleanDoc(additional.doc) {
			for _, key := range orderedSchemaKeys(object, r.order, pointer) {
				known := properties != nil && properties.child(key) != nil
				if patterns != nil {
					for _, pattern := range patterns.keys {
						if argumentPatternMatches(pattern, key) {
							known = true
						}
					}
				}
				if !known && !r.checkNode(additional, object[key], pointer+"/"+escapeArgumentPointer(key)) {
					return false
				}
			}
		} else if !s.additionalPropertiesLeaf(object) {
			return false
		}
	}
	for _, keyword := range []string{"dependencies", "dependentRequired", "dependentSchemas"} {
		if !s.leaf(keyword, object) {
			return false
		}
	}
	if patterns != nil {
		for _, pattern := range patterns.keys {
			for _, key := range orderedSchemaKeys(object, r.order, pointer) {
				if argumentPatternMatches(pattern, key) && !r.checkNode(patterns.child(pattern), object[key], pointer+"/"+escapeArgumentPointer(key)) {
					return false
				}
			}
		}
	}
	if properties != nil {
		for _, key := range properties.keys {
			if item, exists := object[key]; exists && !r.checkNode(properties.child(key), item, pointer+"/"+escapeArgumentPointer(key)) {
				return false
			}
		}
	}
	for _, keyword := range []string{"propertyNames", "minProperties", "maxProperties"} {
		if !s.leaf(keyword, object) {
			return false
		}
	}
	return true
}

func isBooleanDoc(doc any) bool {
	_, ok := doc.(bool)
	return ok
}

func (r *refineRun) checkArrayKeywords(s *argumentSchema, items []any, pointer string) bool {
	tuple := s.list("items")
	if tuple != nil {
		if additional := s.child("additionalItems"); additional != nil {
			for i := len(tuple); i < len(items); i++ {
				if !r.checkNode(additional, items[i], pointer+"/"+strconv.Itoa(i)) {
					return false
				}
			}
		}
	}
	if !s.leaf("contains", items) {
		return false
	}
	switch {
	case tuple != nil:
		for i := range min(len(tuple), len(items)) {
			if !r.checkNode(tuple[i], items[i], pointer+"/"+strconv.Itoa(i)) {
				return false
			}
		}
	case s.child("items") != nil:
		child := s.child("items")
		for i, item := range items {
			if !r.checkNode(child, item, pointer+"/"+strconv.Itoa(i)) {
				return false
			}
		}
	}
	for _, keyword := range []string{"maxContains", "maxItems", "minContains", "minItems"} {
		if !s.leaf(keyword, items) {
			return false
		}
	}
	if prefix := s.list("prefixItems"); prefix != nil {
		for i := range min(len(prefix), len(items)) {
			if !r.checkNode(prefix[i], items[i], pointer+"/"+strconv.Itoa(i)) {
				return false
			}
		}
	}
	return s.leaf("uniqueItems", items)
}

// errorLines is TypeBox's ErrorRefine for a node that reported no error: every check runs, and each failure is worded by its error function.
func (r *refineRun) errorLines(s *argumentSchema, value any, path string) []string {
	count := r.sites[s.path]
	var lines []string
	for index := range count {
		outcome := r.call(s.path, index, value, true)
		if outcome.OK {
			continue
		}
		location := path
		if location == "" {
			location = "root"
		}
		lines = append(lines, "  - "+location+": "+outcome.Message)
		r.errorCount++
	}
	return lines
}

// ValidateToolArgumentsRefined is ValidateToolArgumentsJSON for a tool whose schema carries refinements, which refiner runs. Without a refiner, or when it reports none, it is
// ValidateToolArgumentsJSON itself.
func ValidateToolArgumentsRefined(ctx context.Context, toolName string, schema, args json.RawMessage, refiner Refiner) (result json.RawMessage, err error) {
	if refiner == nil {
		return validateToolArgsSchema(toolName, schema, args)
	}
	sites := refiner.RefinementSites()
	if len(sites) == 0 {
		return validateToolArgsSchema(toolName, schema, args)
	}
	run := newRefineRun(ctx, refiner, sites)
	defer func() {
		if recovered := recover(); recovered != nil {
			abort, ok := recovered.(refineAbort)
			if !ok {
				panic(recovered)
			}
			result, err = nil, abort.err
		}
	}()
	return validateToolArgsSchemaRefined(toolName, schema, args, run)
}
