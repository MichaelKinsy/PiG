package host

import (
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"slices"
	"testing"
)

// Ports packages/chord/test/next/remote/path.test.ts.

// recordingHolder records where it was held and what it was told, as `name@link` and `name dropped`: the path calls only
// attach and dropped (Pi's test fakes the holder the same way).
type recordingHolder struct {
	name  string
	log   *[]string
	names map[*hostLink]string
}

func (*recordingHolder) kind() string { return "recording" }
func (h *recordingHolder) attach(l *hostLink) {
	*h.log = append(*h.log, h.name+"@"+h.names[l])
}
func (h *recordingHolder) dropped(*data.ChordError) { *h.log = append(*h.log, h.name+" dropped") }

// pathRig names the links its holders are held on: the path only passes a link on.
type pathRig struct {
	log   []string
	names map[*hostLink]string
}

func newPathRig() *pathRig { return &pathRig{names: map[*hostLink]string{}} }

func (r *pathRig) holder(name string) *recordingHolder {
	return &recordingHolder{name: name, log: &r.log, names: r.names}
}

func (r *pathRig) linkOf(name string) *hostLink {
	l := &hostLink{}
	r.names[l] = name
	return l
}

func expectLog(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("log = %v, want %v", got, want)
	}
}

// path.test.ts: a path › asks for a link when it gets a holder without one; holds every holder on each link it gets
func TestPathAsksForALinkAndHoldsEveryHolderOnEachLink(t *testing.T) {
	rig := newPathRig()
	changes := 0
	p := newPath(func() { changes++ })
	p.attach(rig.holder("a"))
	p.attach(rig.holder("b"))
	if changes != 2 {
		t.Fatalf("changes = %d, want 2", changes)
	}
	expectLog(t, rig.log)
	p.connected(rig.linkOf("1"))
	expectLog(t, rig.log, "a@1", "b@1")
	p.attach(rig.holder("c"))
	if last := rig.log[len(rig.log)-1]; last != "c@1" {
		t.Fatalf("last = %q, want c@1", last)
	}
	if changes != 2 {
		t.Fatalf("changes = %d, want 2", changes)
	}
}

// path.test.ts: a path › tells every holder when its link drops, then asks for the next; a stale link's drop is ignored
func TestPathTellsHoldersWhenItsLinkDrops(t *testing.T) {
	rig := newPathRig()
	changes := 0
	p := newPath(func() { changes++ })
	first, second := rig.linkOf("1"), rig.linkOf("2")
	p.connected(first)
	p.attach(rig.holder("a"))
	p.disconnected(first)
	expectLog(t, rig.log, "a@1", "a dropped")
	if changes != 1 {
		t.Fatalf("changes = %d, want 1", changes)
	}
	if p.link() != nil {
		t.Fatal("link() is set after its drop")
	}
	p.connected(second)
	p.disconnected(first)
	expectLog(t, rig.log, "a@1", "a dropped", "a@2")
	if changes != 1 {
		t.Fatalf("changes = %d, want 1", changes)
	}
	if p.link() != second {
		t.Fatal("link() is not the second link")
	}
}

// path.test.ts: a path › reports its last holder's detach: no later link holds it
func TestPathReportsItsLastHoldersDetach(t *testing.T) {
	rig := newPathRig()
	changes := 0
	p := newPath(func() { changes++ })
	a, b := rig.holder("a"), rig.holder("b")
	p.connected(rig.linkOf("1"))
	p.attach(a)
	p.attach(b)
	p.detach(a)
	if changes != 0 {
		t.Fatalf("changes = %d, want 0", changes)
	}
	p.detach(b)
	if changes != 1 {
		t.Fatalf("changes = %d, want 1", changes)
	}
	if p.held() {
		t.Fatal("held() after the last detach")
	}
	p.connected(rig.linkOf("2"))
	expectLog(t, rig.log, "a@1", "b@1")
}
