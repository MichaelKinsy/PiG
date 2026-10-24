// SPDX-License-Identifier: MIT

//go:build !race

package codingagent

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

// reachableBytes is the memory the objects reachable from roots occupy, counted from the object graph rather than read from the heap: the
// bytes of every struct, array, slice backing array, string and interface-boxed value a pointer chain from a root reaches, each byte once.
// The runtime's heap statistics count what every goroutine of the process allocated and every one-time initialization of the process
// kept, so a few tens of kilobytes of them move with the scheduler and with the tests that ran before; the graph of two roots has one
// size however the process got there.
//
// A slice or string counts the bytes of its own range (a slice, up to its capacity), so a view of a larger buffer counts from where the
// view starts and two views of one buffer count their union. What the walk cannot see it counts in opaque, by type: a func's captured
// variables, a channel's buffer, an unsafe.Pointer's target, and the types opaqueGraphPackages names (the os.File of a snapshot holds
// a descriptor, not memory of the records).
type reachableBytes struct {
	spans  []reachSpan
	boxed  int64
	seen   map[reachKey]struct{}
	opaque map[string]int
}

type reachSpan struct{ lo, hi uintptr }

type reachKey struct {
	addr uintptr
	cap  int
	typ  reflect.Type
}

// opaqueGraphPackages are the packages whose structs the walk counts at their own size and does not enter: the runtime's and the
// operating system's state, which a Session reaches through a file or a lock and which holds no record of its history.
var opaqueGraphPackages = map[string]bool{
	"os": true, "sync": true, "runtime": true, "reflect": true, "time": true, "syscall": true, "internal/poll": true,
	"internal/syscall/unix": true, "internal/bytealg": true, "unique": true, "weak": true, "context": true,
}

var graphHasPointers sync.Map // reflect.Type to bool

// measureReachable walks the graph from each root, a pointer or a value (which it copies, so what the value refers to is counted and the
// copy is not).
func measureReachable(roots ...any) *reachableBytes {
	g := &reachableBytes{seen: map[reachKey]struct{}{}, opaque: map[string]int{}}
	for _, root := range roots {
		v := reflect.ValueOf(root)
		if v.Kind() != reflect.Pointer {
			boxed := reflect.New(v.Type())
			boxed.Elem().Set(v)
			v = boxed
		}
		g.walk(v)
	}
	return g
}

// Bytes is the size of the union of every span the walk reached, plus the interface boxes and map entries it counted by size.
func (g *reachableBytes) Bytes() int64 {
	spans := append([]reachSpan(nil), g.spans...)
	sort.Slice(spans, func(i, j int) bool { return spans[i].lo < spans[j].lo })
	var total int64
	var lo, hi uintptr
	for _, s := range spans {
		switch {
		case s.lo >= hi:
			total += int64(hi - lo)
			lo, hi = s.lo, s.hi
		case s.hi > hi:
			hi = s.hi
		}
	}
	return total + int64(hi-lo) + g.boxed
}

func (g *reachableBytes) addSpan(lo uintptr, n uintptr) {
	if n > 0 {
		g.spans = append(g.spans, reachSpan{lo, lo + n})
	}
}

func (g *reachableBytes) first(key reachKey) bool {
	if _, ok := g.seen[key]; ok {
		return false
	}
	g.seen[key] = struct{}{}
	return true
}

func hasPointers(t reflect.Type) bool {
	if known, ok := graphHasPointers.Load(t); ok {
		return known.(bool)
	}
	graphHasPointers.Store(t, false) // a recursive type holds a pointer by way of one, which the walk reaches
	var has bool
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.String, reflect.Map, reflect.Interface, reflect.Func, reflect.Chan, reflect.UnsafePointer:
		has = true
	case reflect.Array:
		has = t.Len() > 0 && hasPointers(t.Elem())
	case reflect.Struct:
		for field := range t.Fields() {
			if hasPointers(field.Type) {
				has = true
				break
			}
		}
	}
	graphHasPointers.Store(t, has)
	return has
}

// directInterface reports whether an interface holding a t keeps the value in its data word, as the runtime does for a pointer-shaped type.
func directInterface(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return true
	case reflect.Struct:
		return t.NumField() == 1 && directInterface(t.Field(0).Type)
	case reflect.Array:
		return t.Len() == 1 && directInterface(t.Elem())
	}
	return false
}

// unlock returns v, which must be addressable, without the flag that forbids reading an unexported field.
func unlock(v reflect.Value) reflect.Value {
	return reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem() //nolint:gosec // G103: the walk reads fields reflect hides, at the address reflect gave.
}

func (g *reachableBytes) walk(v reflect.Value) {
	if !hasPointers(v.Type()) {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		elem := v.Type().Elem()
		addr := v.Pointer()
		if !g.first(reachKey{addr, 0, elem}) {
			return
		}
		g.addSpan(addr, elem.Size())
		if elem.Kind() == reflect.Struct && opaqueGraphPackages[elem.PkgPath()] {
			g.opaque[elem.String()]++
			return
		}
		g.walk(unlock(v.Elem()))
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		dynamic := v.Elem().Type()
		data := (*[2]unsafe.Pointer)(unsafe.Pointer(v.UnsafeAddr()))[1] //nolint:gosec // G103: the data word of an interface is the second word of its (type, data) pair, which reflect does not expose.
		if directInterface(dynamic) {
			boxed := reflect.New(dynamic).Elem()
			*(*unsafe.Pointer)(unsafe.Pointer(boxed.UnsafeAddr())) = data //nolint:gosec // G103: a pointer-shaped dynamic type is its data word, which this puts in a value reflect can walk.
			g.walk(boxed)
			return
		}
		if !g.first(reachKey{uintptr(data), 0, dynamic}) {
			return
		}
		g.addSpan(uintptr(data), dynamic.Size())
		g.walk(reflect.NewAt(dynamic, data).Elem())
	case reflect.Struct:
		for _, field := range v.Fields() {
			if hasPointers(field.Type()) {
				g.walk(unlock(field))
			}
		}
	case reflect.Array:
		for i := range v.Len() {
			g.walk(v.Index(i))
		}
	case reflect.Slice:
		if v.Cap() == 0 {
			return
		}
		elem := v.Type().Elem()
		addr := v.Pointer()
		g.addSpan(addr, uintptr(v.Cap())*elem.Size())
		if !hasPointers(elem) || !g.first(reachKey{addr, v.Cap(), v.Type()}) {
			return
		}
		all := v.Slice(0, v.Cap())
		for i := range all.Len() {
			g.walk(all.Index(i))
		}
	case reflect.String:
		g.addSpan(uintptr(unsafe.Pointer(unsafe.StringData(v.String()))), uintptr(v.Len())) //nolint:gosec // G103: the address of a string's bytes, which is where its span starts.
	case reflect.Map:
		if v.IsNil() || !g.first(reachKey{v.Pointer(), 0, v.Type()}) {
			return
		}
		t := v.Type()
		g.boxed += int64(v.Len()) * int64(t.Key().Size()+t.Elem().Size())
		for it := v.MapRange(); it.Next(); {
			key := reflect.New(t.Key()).Elem()
			key.Set(it.Key())
			g.walk(key)
			elem := reflect.New(t.Elem()).Elem()
			elem.Set(it.Value())
			g.walk(elem)
		}
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		if !v.IsNil() {
			g.opaque[v.Type().String()]++
		}
	}
}

// The walk counts exactly what the graph holds: each object once however many pointers reach it, a view of a buffer from where it starts
// to the buffer's end, two views of one buffer as their union, and a field no export reaches as any other.
func TestReachableBytesCountsTheGraphOnce(t *testing.T) {
	type holder struct {
		a, b   *[1000]byte
		hidden []byte
		name   string
	}
	shared := new([1000]byte)
	root := &holder{a: shared, b: shared, hidden: make([]byte, 10, 64), name: strings.Repeat("n", 33)}
	if got, want := measureReachable(root).Bytes(), int64(unsafe.Sizeof(*root))+1000+64+33; got != want {
		t.Errorf("a holder with a shared array, a hidden slice and a string reaches %d B; want %d", got, want)
	}

	buf := make([]byte, 1000)
	views := &struct{ head, middle, tail []byte }{buf[:10], buf[400:410], buf[900:905]}
	if got, want := measureReachable(views).Bytes(), int64(unsafe.Sizeof(*views))+1000; got != want {
		t.Errorf("three views of a 1000 B buffer reach %d B; want %d", got, want)
	}
	late := &struct{ view []byte }{buf[500:510]}
	if got, want := measureReachable(late).Bytes(), int64(unsafe.Sizeof(*late))+500; got != want {
		t.Errorf("a view that starts at 500 of a 1000 B buffer reaches %d B; want %d", got, want)
	}

	type node struct {
		next *node
		_    [100]byte
	}
	loop := &node{}
	loop.next = loop
	if got, want := measureReachable(loop).Bytes(), int64(unsafe.Sizeof(*loop)); got != want {
		t.Errorf("a node that points to itself reaches %d B; want %d", got, want)
	}
}

// An interface holding a value that is not a pointer keeps it in a box of its own, which the walk counts and follows; a map's entries are
// counted by size and their keys and values followed.
func TestReachableBytesFollowsInterfacesAndMaps(t *testing.T) {
	box := [64]byte{1}
	holder := &struct {
		value any
		table map[string][]byte
	}{value: box, table: map[string][]byte{}}
	var keys, caps int
	for _, name := range []string{strings.Repeat("a", 20), strings.Repeat("b", 30), strings.Repeat("c", 40)} {
		holder.table[name] = make([]byte, 5, 128)
		keys, caps = keys+len(name), caps+128
	}
	want := int64(unsafe.Sizeof(*holder)) + 64 + int64(3*(unsafe.Sizeof("")+unsafe.Sizeof([]byte(nil)))) + int64(keys+caps)
	if got := measureReachable(holder).Bytes(); got != want {
		t.Errorf("an interface holding 64 B and a map of three entries reach %d B; want %d", got, want)
	}

	inner := make([]byte, 100)
	pointerShaped := &struct{ value any }{&struct{ p []byte }{inner}}
	if got, want := measureReachable(pointerShaped).Bytes(), int64(unsafe.Sizeof(*pointerShaped))+int64(unsafe.Sizeof([]byte(nil)))+100; got != want {
		t.Errorf("an interface holding a pointer to a slice reaches %d B; want %d", got, want)
	}
}

// What the walk cannot enter it counts by type, so a caller sees what its total leaves out: a file's descriptor state, a closure's captures.
func TestReachableBytesReportsWhatItCannotFollow(t *testing.T) {
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	captured := make([]byte, 1<<20)
	holder := &struct {
		file *os.File
		fn   func() int
		ch   chan int
	}{file, func() int { return len(captured) }, make(chan int, 4)}
	graph := measureReachable(holder)
	if got, want := graph.Bytes(), int64(unsafe.Sizeof(*holder))+int64(unsafe.Sizeof(os.File{})); got != want {
		t.Errorf("a holder of a file, a closure and a channel reaches %d B; want %d (the file's own struct only)", got, want)
	}
	if graph.opaque["os.File"] != 1 || graph.opaque["func() int"] != 1 || graph.opaque["chan int"] != 1 || len(graph.opaque) != 3 {
		t.Errorf("the walk reports %v unfollowed; want one os.File, one func() int and one chan int", graph.opaque)
	}
}
