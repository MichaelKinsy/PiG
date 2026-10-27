package sdk

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Refinement is one entry of a schema's hidden `~refine` list (TypeBox's `{check, error}`): Check decides whether a value passes and Error words the failure. Pi runs them while
// it validates a tool call's arguments; an extension tool's refinements run here, in this process, when the host asks for them (see [Extension.Tool]). A returned error is
// what a thrown JavaScript error is: it ends the validation, and the tool call fails with its message.
type Refinement struct {
	// Check reports whether value passes.
	Check func(value any) (bool, error)
	// Error is the message of a failure; nil is TypeBox's "Refine Error".
	Error func(value any) (string, error)
}

// Refine is `Type.Refine(type, check, error?)`: a copy of the schema whose hidden `~refine` list has the refinement appended. The schema's JSON does not change. A schema made of an
// array, or anything that is not an object, panics as Unsafe does.
func (t typeNamespace) Refine(schema any, check func(value any) (bool, error), errorFn ...func(value any) (string, error)) *TSchema {
	refinement := Refinement{Check: check}
	if len(errorFn) > 0 {
		refinement.Error = errorFn[0]
	}
	var existing []Refinement
	if current, ok := schema.(*TSchema); ok && current != nil {
		if list, isList := current.hidden["~refine"].([]Refinement); isList {
			existing = list
		}
	}
	return update(schema, map[string]any{"~refine": append(slices.Clone(existing), refinement)}, nil)
}

// IsRefine is TypeBox's IsRefine: the value carries a `~refine` list of refinements.
func (t typeNamespace) IsRefine(value any) bool {
	schema, ok := value.(*TSchema)
	if !ok || schema == nil {
		return false
	}
	list, isList := schema.hidden["~refine"].([]Refinement)
	return isList && len(list) > 0
}

// refinementSite is a schema node that carries refinements: the path of the node in the schema (JSON pointer segments of the keywords that lead to it: "/properties/a",
// "/anyOf/1", "/items") and the refinements, in the order they were added.
type refinementSite struct {
	path        string
	refinements []Refinement
}

func pointerSegment(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

// refinementSites lists the nodes of a schema that carry refinements, in the order the nodes are found. A node reached only through a keyword whose value is not a schema TypeBox
// writes (a plain JSON object) has no refinements. root is the schema itself or the map [TSchema.Schema] made.
func refinementSites(root any) []refinementSite {
	var sites []refinementSite
	var visit func(node any, path string)
	visit = func(node any, path string) {
		switch node := node.(type) {
		case *TSchema:
			if node == nil || node.items != nil {
				return
			}
			if list, ok := node.hidden["~refine"].([]Refinement); ok && len(list) > 0 {
				sites = append(sites, refinementSite{path: path, refinements: list})
			}
			visitKeywords(node.object.keys, node.object.values, path, visit)
		case Schema:
			if list, ok := node[schemaRefineKey].([]Refinement); ok && len(list) > 0 {
				sites = append(sites, refinementSite{path: path, refinements: list})
			}
			keys := sortedKeys(node)
			if declared, ok := node[schemaOrderKey].([]string); ok {
				keys = declared
			}
			visitKeywords(keys, node, path, visit)
		}
	}
	visit(root, "")
	return sites
}

var schemaKeywordsWithChild = []string{"additionalProperties", "additionalItems", "contains", "propertyNames", "not", "if", "then", "else", "items", "unevaluatedItems", "unevaluatedProperties"}
var schemaKeywordsWithMembers = []string{"properties", "patternProperties", "dependentSchemas", "dependencies", "$defs", "definitions"}
var schemaKeywordsWithList = []string{"allOf", "anyOf", "oneOf", "prefixItems", "items"}

func visitKeywords(keys []string, values map[string]any, path string, visit func(node any, path string)) {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		switch {
		case slices.Contains(schemaKeywordsWithMembers, key):
			switch members := value.(type) {
			case *OrderedObject:
				for _, name := range members.keys {
					visit(members.values[name], path+"/"+key+"/"+pointerSegment(name))
				}
			case map[string]any:
				for _, name := range sortedKeys(members) {
					visit(members[name], path+"/"+key+"/"+pointerSegment(name))
				}
			}
		case slices.Contains(schemaKeywordsWithList, key):
			if list, isList := value.([]any); isList {
				for i, item := range list {
					visit(item, fmt.Sprintf("%s/%s/%d", path, key, i))
				}
				continue
			}
			if slices.Contains(schemaKeywordsWithChild, key) {
				visit(value, path+"/"+key)
			}
		case slices.Contains(schemaKeywordsWithChild, key):
			visit(value, path+"/"+key)
		}
	}
}

// countRefinements counts every refinement in a schema, wherever it hangs: a count above what [refinementSites] finds means a refinement sits under a keyword whose refinements the host
// does not run.
func countRefinements(node any) int {
	switch node := node.(type) {
	case *TSchema:
		if node == nil {
			return 0
		}
		list, _ := node.hidden["~refine"].([]Refinement)
		total := len(list)
		for _, key := range node.object.keys {
			total += countRefinements(node.object.values[key])
		}
		for _, item := range node.items {
			total += countRefinements(item)
		}
		return total
	case *OrderedObject:
		total := 0
		if node != nil {
			for _, key := range node.keys {
				total += countRefinements(node.values[key])
			}
		}
		return total
	case map[string]any:
		total := 0
		for key, value := range node {
			if key == schemaOrderKey {
				continue
			}
			if key == schemaRefineKey {
				list, _ := value.([]Refinement)
				total += len(list)
				continue
			}
			total += countRefinements(value)
		}
		return total
	case []any:
		total := 0
		for _, item := range node {
			total += countRefinements(item)
		}
		return total
	}
	return 0
}

// refinementDecls is the wire form of the sites: a path and a count each.
func refinementDecls(sites []refinementSite) []refinementDecl {
	if len(sites) == 0 {
		return nil
	}
	decls := make([]refinementDecl, len(sites))
	for i, site := range sites {
		decls[i] = refinementDecl{Path: site.path, Count: len(site.refinements)}
	}
	return decls
}

// runRefinement answers tool_refine_arguments: refinement index of the node at path of the tool's schema, on value. A panic or an error from the predicate is what a thrown JavaScript
// error is, and the host fails the tool call with its message.
func (e *Extension) runRefinement(tool string, request *refineMsg) (result refineResult, err error) {
	e.toolMu.RLock()
	var sites []refinementSite
	for i := range e.tools {
		if e.tools[i].Name == tool {
			sites = e.tools[i].refineSites
			break
		}
	}
	e.toolMu.RUnlock()
	var refinement *Refinement
	for i := range sites {
		if sites[i].path == request.Path && request.Index >= 0 && request.Index < len(sites[i].refinements) {
			refinement = &sites[i].refinements[request.Index]
		}
	}
	if refinement == nil || refinement.Check == nil {
		return result, fmt.Errorf("tool %s has no refinement %d at %q", tool, request.Index, request.Path)
	}
	var value any
	if len(request.Value) > 0 {
		if err := json.Unmarshal(request.Value, &value); err != nil {
			return result, fmt.Errorf("decode refinement value: %w", err)
		}
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			result, err = refineResult{}, fmt.Errorf("%v", recovered)
		}
	}()
	passed, err := refinement.Check(value)
	if err != nil {
		return result, err
	}
	result.OK = passed
	if !passed && request.WithMessage {
		result.Message = "Refine Error"
		if refinement.Error != nil {
			if result.Message, err = refinement.Error(value); err != nil {
				return refineResult{}, err
			}
		}
	}
	return result, nil
}

func countSiteRefinements(sites []refinementSite) int {
	total := 0
	for _, site := range sites {
		total += len(site.refinements)
	}
	return total
}

// validationSchema is the schema as the host validates arguments with it: the JSON of the tool's parameters with TypeBox's `~kind` on every node that has one, which JSON.stringify drops
// but the host's port of Value.Convert reads (the Node runtime sends the same form as validation_parameters). A node's `~kind` follows its other members.
func validationSchema(node any) any {
	switch node := node.(type) {
	case *TSchema:
		if node == nil {
			return nil
		}
		if node.items != nil {
			items := make([]any, len(node.items))
			for i, item := range node.items {
				items[i] = validationSchema(item)
			}
			return items
		}
		out := &OrderedObject{values: map[string]any{}}
		for _, key := range node.object.keys {
			out.set(key, validationSchema(node.object.values[key]))
		}
		if kind, ok := node.hidden["~kind"].(string); ok {
			out.set("~kind", kind)
		}
		return out
	case *OrderedObject:
		if node == nil {
			return nil
		}
		out := &OrderedObject{values: map[string]any{}}
		for _, key := range node.keys {
			out.set(key, validationSchema(node.values[key]))
		}
		return out
	case map[string]any:
		out := &OrderedObject{values: map[string]any{}}
		declared, _ := node[schemaOrderKey].([]string)
		keys := declared
		if keys == nil {
			keys = sortedKeys(node)
		}
		for _, key := range keys {
			if value, ok := node[key]; ok && key != schemaOrderKey && key != schemaRefineKey && key != schemaValidationKey {
				out.set(key, validationSchema(value))
			}
		}
		return out
	case []any:
		items := make([]any, len(node))
		for i, item := range node {
			items[i] = validationSchema(item)
		}
		return items
	}
	return node
}
