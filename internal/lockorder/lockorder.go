// Package lockorder is a lock-acquisition-order checker for instrumented test builds.
//
// The end-to-end journey harness (test/journeys) rewrites every sync.Mutex and sync.RWMutex of the PiG module into this package's types with a build overlay, so a binary built for a journey records which locks a goroutine holds when it takes another. A cycle in that order graph (lock A taken while holding B on one path, B while holding A on another) is a potential deadlock even when no run hit it. Go's race detector does not report lock-order cycles.
//
// Production binaries never import this package: the overlay exists only in the journey build.
//
// A lock's class is the declaration the overlay rewrote it from: every instance of one struct field (or variable) shares a class, like the classes of the Linux kernel's lockdep. Embedded locks and locks created through a composite literal have no declaration to name and use the call site of their first acquisition instead. A cycle fails the run when it is real for the objects involved: either the same lock instances were taken in opposite orders, or every class on the cycle has only ever had one instance (a singleton such as a session or mode lock). A class cycle among classes with several instances (nested containers of one type) is only a candidate, written to the report file with the suffix .potential, because two different parent and child objects of one type can interleave without ever contending.
package lockorder

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

type class uint32

type edge struct{ from, to class }

type held struct {
	cls  class
	inst uintptr
}

// maxInstanceEdges bounds the instance-level graph of a long journey; past it only class cycles of singletons are still found.
const maxInstanceEdges = 200_000

var (
	mu      sync.Mutex // guards everything below; a raw sync.Mutex because this package is excluded from rewriting
	names   = []string{""}
	byName  = map[string]class{}
	adj     = map[class]map[class]struct{}{}
	seen    = map[edge]string{} // first stack that created the edge
	holding = map[int64][]held{}
	reports = map[string]bool{}

	instAdj    = map[uintptr]map[uintptr]struct{}{}
	instRev    = map[uintptr]map[uintptr]struct{}{} // reverse edges, to purge a dead lock
	instEdge   = 0
	classMulti = map[class]bool{}    // class has had more than one instance
	classFirst = map[class]uintptr{} // the first instance seen
)

func classNamed(site string, skip int) class {
	if site != "" {
		mu.Lock()
		defer mu.Unlock()
		if c, ok := byName[site]; ok {
			return c
		}
		c := class(len(names))
		names = append(names, site)
		byName[site] = c
		return c
	}
	name := "unknown"
	if _, file, line, ok := runtime.Caller(skip); ok {
		name = file + ":" + strconv.Itoa(line)
	}
	mu.Lock()
	defer mu.Unlock()
	if c, ok := byName[name]; ok {
		return c
	}
	c := class(len(names))
	names = append(names, name)
	byName[name] = c
	return c
}

func goid() int64 { return int64(goroutineid.Current()) }

func shortStack(skip int) string {
	pcs := make([]uintptr, 12)
	n := runtime.Callers(skip, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var b strings.Builder
	for {
		f, more := frames.Next()
		fmt.Fprintf(&b, "    %s\n      %s:%d\n", f.Function, f.File, f.Line)
		if !more {
			break
		}
	}
	return b.String()
}

// before records the order edges for acquiring inst (class cls) on this goroutine, before it blocks.
func before(inst uintptr, cls class, shared bool) {
	g := goid()
	var stack string
	mu.Lock()
	if first, ok := classFirst[cls]; !ok {
		classFirst[cls] = inst
	} else if first != inst {
		classMulti[cls] = true
	}
	for _, h := range holding[g] {
		if h.inst == inst {
			// A second read lock on a goroutine that already holds one deadlocks once a writer queues between the two: sync.RWMutex blocks new readers behind a waiting writer, which is why its documentation prohibits recursive read locking.
			what := "locks"
			if shared {
				what = "read-locks"
			}
			report("relock "+names[cls], fmt.Sprintf("goroutine %d %s %s again while holding it\n%s", g, what, names[cls], shortStack(4)), false)
			continue
		}
		if stack == "" {
			stack = shortStack(4)
		}
		// Instance level: the same two objects taken in opposite orders.
		if instEdge < maxInstanceEdges {
			if instAdj[h.inst] == nil {
				instAdj[h.inst] = map[uintptr]struct{}{}
			}
			if _, ok := instAdj[h.inst][inst]; !ok {
				instAdj[h.inst][inst] = struct{}{}
				if instRev[inst] == nil {
					instRev[inst] = map[uintptr]struct{}{}
				}
				instRev[inst][h.inst] = struct{}{}
				instEdge++
				if path := findInstPath(inst, h.inst); path != nil {
					report(fmt.Sprintf("inst %x->%x", h.inst, inst), fmt.Sprintf("lock order cycle between the same lock objects: %s is taken while holding %s (%d locks on the cycle), and an earlier path took them in the opposite order\n  taken here:\n%s", names[cls], names[h.cls], len(path), stack), false)
				}
			}
		}
		if h.cls == cls {
			continue
		}
		e := edge{h.cls, cls}
		if _, ok := seen[e]; ok {
			continue
		}
		seen[e] = stack
		if adj[h.cls] == nil {
			adj[h.cls] = map[class]struct{}{}
		}
		adj[h.cls][cls] = struct{}{}
		if path := findPath(cls, h.cls); path != nil {
			single := !classMulti[cls] && !classMulti[h.cls]
			for _, c := range path {
				single = single && !classMulti[c]
			}
			var b strings.Builder
			fmt.Fprintf(&b, "lock order cycle: %s is taken while holding %s, but the reverse order exists\n", names[cls], names[h.cls])
			fmt.Fprintf(&b, "  new edge %s -> %s taken here:\n%s", names[h.cls], names[cls], stack)
			for i := 0; i+1 < len(path); i++ {
				fmt.Fprintf(&b, "  earlier edge %s -> %s taken here:\n%s", names[path[i]], names[path[i+1]], seen[edge{path[i], path[i+1]}])
			}
			report(names[h.cls]+"->"+names[cls], b.String(), !single)
		}
	}
	mu.Unlock()
}

// sentinel is the identity of one lock instance. A lock's address is not an identity: the allocator reuses it, so edges recorded for a dead lock would join the next lock built at the same address into a cycle that never existed. The sentinel is reachable only from its lock; when the lock dies its finalizer purges the sentinel's edges before the address can be reused.
type sentinel struct{ _ [32]byte }

func newSentinel() *sentinel {
	s := new(sentinel)
	addr := uintptr(unsafePointer(s))
	runtime.SetFinalizer(s, func(*sentinel) { purge(addr) })
	return s
}

func purge(addr uintptr) {
	mu.Lock()
	defer mu.Unlock()
	for next := range instAdj[addr] {
		delete(instRev[next], addr)
		instEdge--
	}
	for prev := range instRev[addr] {
		delete(instAdj[prev], addr)
		instEdge--
	}
	delete(instAdj, addr)
	delete(instRev, addr)
}

// findInstPath returns a path of lock instances from a to b, or nil. mu is held.
func findInstPath(a, b uintptr) []uintptr {
	prev := map[uintptr]uintptr{a: a}
	queue := []uintptr{a}
	for len(queue) > 0 && len(prev) < 20_000 {
		x := queue[0]
		queue = queue[1:]
		if x == b {
			var path []uintptr
			for c := b; ; c = prev[c] {
				path = append(path, c)
				if c == a {
					return path
				}
			}
		}
		for y := range instAdj[x] {
			if _, ok := prev[y]; !ok {
				prev[y] = x
				queue = append(queue, y)
			}
		}
	}
	return nil
}

// findPath returns a path of classes from a to b in the order graph, or nil. mu is held.
func findPath(a, b class) []class {
	prev := map[class]class{a: a}
	queue := []class{a}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		if x == b {
			var path []class
			for c := b; ; c = prev[c] {
				path = append([]class{c}, path...)
				if c == a {
					return path
				}
			}
		}
		for y := range adj[x] {
			if _, ok := prev[y]; !ok {
				prev[y] = x
				queue = append(queue, y)
			}
		}
	}
	return nil
}

func report(key, text string, potential bool) {
	if reports[key] {
		return
	}
	reports[key] = true
	if potential {
		if path := os.Getenv("PIG_LOCKORDER_REPORT"); path != "" {
			if f, err := os.OpenFile(path+".potential", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
				_, _ = f.WriteString(text + "\n")
				_ = f.Close()
			}
		}
		return
	}
	fmt.Fprintf(os.Stderr, "LOCKORDER %s", text)
	if path := os.Getenv("PIG_LOCKORDER_REPORT"); path != "" {
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_, _ = f.WriteString(text + "\n")
			_ = f.Close()
		}
	}
}

func acquired(inst uintptr, cls class) {
	g := goid()
	mu.Lock()
	holding[g] = append(holding[g], held{cls: cls, inst: inst})
	mu.Unlock()
}

func released(inst uintptr, g int64) {
	mu.Lock()
	hs := holding[g]
	for i, h := range slices.Backward(hs) {
		if h.inst == inst {
			hs = append(hs[:i], hs[i+1:]...)
			break
		}
	}
	if len(hs) == 0 {
		delete(holding, g)
	} else {
		holding[g] = hs
	}
	mu.Unlock()
}

// Mutex replaces sync.Mutex.
type Mutex struct {
	mu     sync.Mutex
	cls    atomic.Uint32
	holder atomic.Int64
	ident  atomic.Pointer[sentinel]
}

func (m *Mutex) class(site string) class {
	if c := m.cls.Load(); c != 0 {
		return class(c)
	}
	m.cls.CompareAndSwap(0, uint32(classNamed(site, 4)))
	return class(m.cls.Load())
}

func (m *Mutex) id() uintptr {
	if p := m.ident.Load(); p != nil {
		return uintptr(unsafePointer(p))
	}
	m.ident.CompareAndSwap(nil, newSentinel())
	return uintptr(unsafePointer(m.ident.Load()))
}

// Lock locks m.
func (m *Mutex) Lock() { m.LockAs("") }

// LockAs locks m as a member of the class declared at site; "" names the caller's call site.
func (m *Mutex) LockAs(site string) {
	c := m.class(site)
	before(m.id(), c, false)
	m.mu.Lock()
	m.holder.Store(goid())
	acquired(m.id(), c)
}

// TryLock tries to lock m.
func (m *Mutex) TryLock() bool { return m.TryLockAs("") }

// TryLockAs is TryLock with an explicit class site.
func (m *Mutex) TryLockAs(site string) bool {
	c := m.class(site)
	if !m.mu.TryLock() {
		return false
	}
	m.holder.Store(goid())
	acquired(m.id(), c)
	return true
}

// Unlock unlocks m.
func (m *Mutex) Unlock() {
	g := m.holder.Load()
	m.mu.Unlock()
	released(m.id(), g)
}

// RWMutex replaces sync.RWMutex.
type RWMutex struct {
	mu     sync.RWMutex
	cls    atomic.Uint32
	holder atomic.Int64 // writer goroutine
	ident  atomic.Pointer[sentinel]
}

func (m *RWMutex) class(site string) class {
	if c := m.cls.Load(); c != 0 {
		return class(c)
	}
	m.cls.CompareAndSwap(0, uint32(classNamed(site, 4)))
	return class(m.cls.Load())
}

func (m *RWMutex) id() uintptr {
	if p := m.ident.Load(); p != nil {
		return uintptr(unsafePointer(p))
	}
	m.ident.CompareAndSwap(nil, newSentinel())
	return uintptr(unsafePointer(m.ident.Load()))
}

// Lock write-locks m.
func (m *RWMutex) Lock() { m.LockAs("") }

// LockAs write-locks m as a member of the class declared at site.
func (m *RWMutex) LockAs(site string) {
	c := m.class(site)
	before(m.id(), c, false)
	m.mu.Lock()
	m.holder.Store(goid())
	acquired(m.id(), c)
}

// TryLock tries to write-lock m.
func (m *RWMutex) TryLock() bool { return m.TryLockAs("") }

// TryLockAs is TryLock with an explicit class site.
func (m *RWMutex) TryLockAs(site string) bool {
	c := m.class(site)
	if !m.mu.TryLock() {
		return false
	}
	m.holder.Store(goid())
	acquired(m.id(), c)
	return true
}

// Unlock write-unlocks m.
func (m *RWMutex) Unlock() {
	g := m.holder.Load()
	m.mu.Unlock()
	released(m.id(), g)
}

// RLock read-locks m. Recursive read locking on one goroutine is allowed and not reported.
func (m *RWMutex) RLock() { m.RLockAs("") }

// RLockAs read-locks m as a member of the class declared at site.
func (m *RWMutex) RLockAs(site string) {
	c := m.class(site)
	before(m.id(), c, true)
	m.mu.RLock()
	acquired(m.id(), c)
}

// TryRLock tries to read-lock m.
func (m *RWMutex) TryRLock() bool { return m.TryRLockAs("") }

// TryRLockAs is TryRLock with an explicit class site.
func (m *RWMutex) TryRLockAs(site string) bool {
	c := m.class(site)
	if !m.mu.TryRLock() {
		return false
	}
	acquired(m.id(), c)
	return true
}

// RUnlock read-unlocks m.
func (m *RWMutex) RUnlock() {
	m.mu.RUnlock()
	released(m.id(), goid())
}

// RLocker returns a Locker for the read lock.
func (m *RWMutex) RLocker() sync.Locker { return (*rlocker)(m) }

type rlocker RWMutex

func (r *rlocker) Lock()   { (*RWMutex)(r).RLock() }
func (r *rlocker) Unlock() { (*RWMutex)(r).RUnlock() }
