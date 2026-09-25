package pico3

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
)

// Membrane is a transaction-scoped revocable view over tracked documents.
// Every Node reached through a membrane shares one liveness flag; after
// Revoke every Node, root or nested, panics with a *TypeError on any
// operation. Assigning a Node into a document is rejected, and plain inputs
// are copied through JSON so later caller mutations cannot reach the document.
type Membrane struct {
	alive atomic.Bool
	what  string
	nodes map[string]*Node
}

// NewMembrane creates a live membrane; what names it in error messages.
func NewMembrane(what string) *Membrane {
	membrane := &Membrane{what: what, nodes: map[string]*Node{}}
	membrane.alive.Store(true)
	return membrane
}

// Revoke makes every Node of this membrane unusable.
func (membrane *Membrane) Revoke() { membrane.alive.Store(false) }

func (membrane *Membrane) assertAlive() {
	if !membrane.alive.Load() {
		panic(&TypeError{Message: fmt.Sprintf("document proxy (%s) used outside its transaction", membrane.what)})
	}
}

// Wrap returns the root Node over a JSON object.
func (membrane *Membrane) Wrap(target JsonObject) *Node {
	root := fmt.Sprintf("%x", reflect.ValueOf(target).Pointer())
	return membrane.node(root, func() any { return target }, nil, nil)
}

// view returns a restricted object Node whose keys route to holders.
func (membrane *Membrane) view(fields map[string]JsonObject) *Node {
	return membrane.node(fmt.Sprintf("view:%x", reflect.ValueOf(fields).Pointer()), nil, nil, fields)
}

func (membrane *Membrane) node(path string, get func() any, set func(any), fields map[string]JsonObject) *Node {
	if cached, ok := membrane.nodes[path]; ok && fields == nil && sameContainer(cached.get(), get()) {
		return cached
	}
	node := &Node{membrane: membrane, path: path, get: get, set: set, fields: fields}
	if fields == nil {
		membrane.nodes[path] = node
	}
	return node
}

// Node is a membrane wrapper over one JSON object or array inside a document.
// Object methods (Get, Set, Delete, Has, Keys) and array methods (Len, Index,
// SetIndex, Push, Splice, RemoveWhere) panic with a *TypeError after the
// membrane is revoked or when used on the wrong container kind.
type Node struct {
	membrane *Membrane
	path     string
	get      func() any
	set      func(any)
	// fields routes a restricted view's keys to the objects that hold them;
	// the view cannot gain keys.
	fields map[string]JsonObject
}

func (node *Node) object() JsonObject {
	node.membrane.assertAlive()
	object, ok := node.get().(map[string]any)
	if !ok {
		panic(&TypeError{Message: fmt.Sprintf("document proxy (%s) is not an object", node.membrane.what)})
	}
	return object
}

func (node *Node) array() []any {
	node.membrane.assertAlive()
	items, ok := node.get().([]any)
	if !ok {
		panic(&TypeError{Message: fmt.Sprintf("document proxy (%s) is not an array", node.membrane.what)})
	}
	return items
}

// holder returns the object that stores key.
func (node *Node) holder(key string) (JsonObject, bool) {
	if node.fields != nil {
		node.membrane.assertAlive()
		holder, ok := node.fields[key]
		return holder, ok
	}
	return node.object(), true
}

func sameContainer(left, right any) bool {
	leftValue, rightValue := reflect.ValueOf(left), reflect.ValueOf(right)
	if !leftValue.IsValid() || !rightValue.IsValid() || leftValue.Type() != rightValue.Type() {
		return false
	}
	return leftValue.Pointer() == rightValue.Pointer()
}

func (node *Node) wrapChild(segment string, value any, get func() any, set func(any)) any {
	switch value.(type) {
	case map[string]any:
		return node.membrane.node(fmt.Sprintf("object:%x", reflect.ValueOf(value).Pointer()), func() any { return value }, nil, nil)
	case []any:
		captured := value
		return node.membrane.node(node.path+"\x00"+segment, func() any { return captured }, func(next any) {
			if sameContainer(get(), captured) {
				set(next)
			}
			captured = next
		}, nil)
	default:
		return value
	}
}

// Get returns a member: a *Node for objects and arrays, the value otherwise,
// and nil when absent.
func (node *Node) Get(key string) any {
	holder, ok := node.holder(key)
	if !ok {
		return nil
	}
	value, exists := holder[key]
	if !exists {
		return nil
	}
	if node.fields != nil {
		return node.wrapChild(key, value,
			func() any { return holder[key] },
			func(next any) { holder[key] = next })
	}
	return node.wrapChild(key, value,
		func() any {
			parent, _ := node.get().(map[string]any)
			return parent[key]
		},
		func(next any) {
			if parent, ok := node.get().(map[string]any); ok {
				parent[key] = next
			}
		})
}

// Has reports whether key is present.
func (node *Node) Has(key string) bool {
	holder, ok := node.holder(key)
	if !ok {
		return false
	}
	_, exists := holder[key]
	return exists
}

// Keys returns the present keys in ascending order.
func (node *Node) Keys() []string {
	if node.fields != nil {
		node.membrane.assertAlive()
		var keys []string
		for key, holder := range node.fields {
			if _, ok := holder[key]; ok {
				keys = append(keys, key)
			}
		}
		return sortedKeys(toSet(keys))
	}
	return sortedKeys(node.object())
}

func toSet(keys []string) JsonObject {
	out := JsonObject{}
	for _, key := range keys {
		out[key] = true
	}
	return out
}

// Set assigns a plain value; a Node anywhere inside value is rejected.
func (node *Node) Set(key string, value any) {
	node.membrane.assertAlive()
	if key == "__proto__" {
		panic(&TypeError{Message: fmt.Sprintf("document proxy (%s) cannot change prototypes", node.membrane.what)})
	}
	holder, ok := node.holder(key)
	if !ok {
		panic(&TypeError{Message: fmt.Sprintf("Cannot add property %s, object is not extensible", key)})
	}
	holder[key] = node.membrane.input(value)
}

// Delete removes key.
func (node *Node) Delete(key string) {
	holder, ok := node.holder(key)
	if !ok {
		return
	}
	delete(holder, key)
}

// Len returns an array's length.
func (node *Node) Len() int { return len(node.array()) }

// Index returns an array element: a *Node for containers.
func (node *Node) Index(index int) any {
	items := node.array()
	if index < 0 || index >= len(items) {
		return nil
	}
	return node.wrapChild(fmt.Sprint(index), items[index],
		func() any {
			current, _ := node.get().([]any)
			if index < len(current) {
				return current[index]
			}
			return nil
		},
		func(next any) {
			current, _ := node.get().([]any)
			if index < len(current) {
				current[index] = next
			}
		})
}

// SetIndex replaces an existing element or appends one past the end.
func (node *Node) SetIndex(index int, value any) {
	items := node.array()
	stored := node.membrane.input(value)
	if index == len(items) {
		node.replace(append(items, stored))
		return
	}
	if index < 0 || index > len(items) {
		panic(&TypeError{Message: fmt.Sprintf("array index %d is out of range", index)})
	}
	items[index] = stored
}

// Push appends plain values.
func (node *Node) Push(values ...any) {
	items := node.array()
	for _, value := range values {
		items = append(items, node.membrane.input(value))
	}
	node.replace(items)
}

// Splice removes count elements at start and inserts values there.
func (node *Node) Splice(start, count int, values ...any) {
	items := node.array()
	start = max(0, min(start, len(items)))
	end := min(len(items), start+max(0, count))
	next := append([]any{}, items[:start]...)
	for _, value := range values {
		next = append(next, node.membrane.input(value))
	}
	node.replace(append(next, items[end:]...))
}

// RemoveWhere removes matching elements in place.
func (node *Node) RemoveWhere(match func(item any) bool) {
	items := node.array()
	kept := make([]any, 0, len(items))
	for index, item := range items {
		if !match(node.Index(index)) {
			kept = append(kept, item)
		}
	}
	node.replace(kept)
}

func (node *Node) replace(items []any) {
	if node.set == nil {
		panic(&TypeError{Message: fmt.Sprintf("document proxy (%s) array has no parent", node.membrane.what)})
	}
	node.set(items)
}

// Plain returns a deep plain copy of the wrapped value.
func (node *Node) Plain() JsonValue {
	node.membrane.assertAlive()
	if node.fields != nil {
		out := JsonObject{}
		for key, holder := range node.fields {
			if value, ok := holder[key]; ok {
				out[key] = cloneJSON(value)
			}
		}
		return out
	}
	return cloneJSON(node.get())
}

// input validates and copies a value crossing into a document.
func (membrane *Membrane) input(value any) JsonValue {
	membrane.assertAlive()
	stored, err := ToStored(value)
	if err != nil {
		panic(&TypeError{Message: fmt.Sprintf("assigning a non-JSON value into a document (%s): %v", membrane.what, err)})
	}
	if containsNode(value) {
		panic(&TypeError{Message: fmt.Sprintf("assigning a document proxy into a document (%s); assign a plain value", membrane.what)})
	}
	return stored
}

func containsNode(value any) bool {
	switch typed := value.(type) {
	case *Node:
		return true
	case map[string]any:
		for _, item := range typed {
			if containsNode(item) {
				return true
			}
		}
	case []any:
		if slices.ContainsFunc(typed, containsNode) {
			return true
		}
	}
	return false
}

// String renders the node's path for diagnostics.
func (node *Node) String() string {
	return "document proxy " + strings.ReplaceAll(node.path, "\x00", ".")
}
