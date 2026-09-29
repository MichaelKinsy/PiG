package subprocess

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

type retentionGroupKey struct{}
type reloadingKey struct{}

// reloadPasses numbers Host.Reload calls across every Host in the process, so a retained process shared by successive Hosts never sees one pass number twice.
var reloadPasses atomic.Uint64

// reloadPass is the number of the Host.Reload that ctx belongs to, or zero outside a reload.
func reloadPass(ctx context.Context) uint64 {
	value, _ := ctx.Value(reloadingKey{}).(uint64)
	return value
}

// RuntimeRetention keeps extension processes alive across the Hosts of one Session runtime. Pi re-invokes extension factories inside the process that already holds their modules, on /reload and on every Session replacement, so module state and globals persist. A Host claims a retained process from its RuntimeRetention when a cell it starts matches one, and admits the new factory generation to it instead of spawning another process.
type RuntimeRetention struct {
	mu    sync.Mutex
	procs []*processShare
	// epochs numbers the Hosts that share this retention in creation order. loaded is the newest Host whose load has finished: a predecessor that retires after that has nothing left to hand over.
	epochs int
	loaded int
}

// NewRuntimeRetention returns an empty retention for the Hosts of one Session runtime.
func NewRuntimeRetention() *RuntimeRetention { return &RuntimeRetention{} }

// register records the process a spawner started.
func (r *RuntimeRetention) register(share *processShare) {
	if share == nil || share.key == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	live := r.procs[:0]
	for _, known := range r.procs {
		if known.alive() {
			live = append(live, known)
		}
	}
	live = append(live, share)
	r.procs = live
}

// claim returns a live process retained under key, holding one reference for the caller. A process parked by a retiring Host transfers the parking reference instead.
func (r *RuntimeRetention) claim(key string) *processShare {
	if key == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	live := r.procs[:0]
	var found *processShare
	for _, share := range r.procs {
		if !share.alive() || share.exited() {
			continue
		}
		if share.dead.Load() {
			live = append(live, share)
			continue
		}
		// A process that has died but not yet been reaped still looks alive; its closed stdin proves it is gone.
		if share.send(factoryAdmission{Op: "noop"}) != nil {
			continue
		}
		live = append(live, share)
		if found == nil && share.key == key {
			if share.parked.CompareAndSwap(true, false) || share.acquire() {
				found = share
			}
		}
	}
	r.procs = live
	return found
}

// enter numbers a Host that starts sharing the retention.
func (r *RuntimeRetention) enter() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.epochs++
	return r.epochs
}

// reserve parks share's process for the successor of the Host numbered epoch, unless a successor has already loaded and claimed what it needs. It reports whether this call took the parking reference.
func (r *RuntimeRetention) reserve(epoch int, share *processShare) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loaded > epoch {
		return false
	}
	return share.reserve(epoch)
}

// loadDone ends the load of the Host numbered epoch: the processes parked for it that its cells did not claim end.
func (r *RuntimeRetention) loadDone(epoch int) {
	r.mu.Lock()
	r.loaded = max(r.loaded, epoch)
	procs := append([]*processShare(nil), r.procs...)
	r.mu.Unlock()
	for _, share := range procs {
		share.releaseParkedBefore(epoch)
	}
}

// Close kills every parked process no Host claimed. Call it when the Session runtime that owns the retention ends.
func (r *RuntimeRetention) Close() {
	r.mu.Lock()
	procs := append([]*processShare(nil), r.procs...)
	r.mu.Unlock()
	for _, share := range procs {
		share.releaseParkedBefore(int(^uint(0) >> 1))
	}
}

// adopt makes p another state of the process from's spawner started.
func (p *packedProcessState) adopt(from *packedProcessState) {
	origin := from.spawner()
	p.origin = origin
	p.share = origin.share
	p.cmd = origin.cmd
	p.processTree = origin.processTree
	p.cancel = origin.cancel
	p.stderrLog = origin.stderrLog
}

// stopAndReap stops p and waits for its process to exit when p's stop ended it. A process another state still holds keeps running.
func (p *packedProcessState) stopAndReap() {
	p.stop()
	if p.processReleased() {
		_ = p.wait()
	}
	p.releaseUsageLease()
}

// retention returns the retention this Host claims processes from.
func (h *Host) retention() *RuntimeRetention {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.retained == nil {
		h.retained = NewRuntimeRetention()
	}
	return h.retained
}

// SetRuntimeRetention shares r with the Hosts that succeed this one in a Session runtime, so a replacement Session's extensions re-invoke their factories in the processes this Host started. Configure it before loading extensions.
func (h *Host) SetRuntimeRetention(r *RuntimeRetention) {
	h.mu.Lock()
	h.retained = r
	h.retainedShared = r != nil
	if r != nil {
		h.epoch = r.enter()
	}
	h.mu.Unlock()
}

// Retain parks this Host's live extension processes in its RuntimeRetention, so Shutdown ends the Host's own connections and leaves the processes running for the replacement Host to claim. It is a no-op for a Host without a shared RuntimeRetention, and parks nothing once a successor Host has loaded. Call it before Shutdown, after session_shutdown has been delivered. It returns once every process has taken the hold, so the generations Shutdown retires next are not the last thing keeping a process open.
func (h *Host) Retain() {
	type held struct {
		share *processShare
		conns []*Conn
	}
	h.mu.Lock()
	retention, epoch := h.retained, h.epoch
	if !h.retainedShared {
		h.mu.Unlock()
		return
	}
	byShare := map[*processShare]*held{}
	var order []*held
	add := func(share *processShare, conn *Conn) {
		if share == nil {
			return
		}
		entry := byShare[share]
		if entry == nil {
			entry = &held{share: share}
			byShare[share] = entry
			order = append(order, entry)
		}
		if conn != nil {
			entry.conns = append(entry.conns, conn)
		}
	}
	for _, me := range h.exts {
		switch {
		case me.packedProcess != nil && !me.packedProcess.stopping.Load():
			add(me.packedProcess.share, me.connection())
		case me.packedProcess == nil && me.share != nil:
			add(me.share, me.connection())
		}
	}
	h.mu.Unlock()
	var parked sync.WaitGroup
	for _, entry := range order {
		if !retention.reserve(epoch, entry.share) {
			continue
		}
		parked.Go(func() {
			if err := h.awaitParkAck(entry.share, entry.conns); err != nil {
				// A process that cannot hold itself open ends with this Host.
				entry.share.unpark()
			}
		})
	}
	parked.Wait()
}

// awaitParkAck tells the process to hold itself open and waits until it does. The process connects to a socket this Host listens on once the hold is set. A process that exits, or whose generations stop answering their heartbeat, ends the wait.
func (h *Host) awaitParkAck(share *processShare, conns []*Conn) error {
	name := fmt.Sprintf("park-%d", h.parkSeq.Add(1))
	sockPath, err := h.sockPathFor(name)
	if err != nil {
		return err
	}
	_ = os.Remove(sockPath)
	listener, address, err := ListenExtension(sockPath, share.node)
	if err != nil {
		return err
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(sockPath)
	}()
	accepted := make(chan struct{})
	go func() {
		if conn, err := listener.Accept(); err == nil {
			_ = conn.Close()
			close(accepted)
		}
	}()
	if err := share.send(factoryAdmission{Op: "park", Socket: address}); err != nil {
		return err
	}
	stalled := make(chan struct{})
	stop := make(chan struct{})
	defer close(stop)
	var once sync.Once
	for _, conn := range conns {
		go func() {
			select {
			case <-conn.done:
				once.Do(func() { close(stalled) })
			case <-stop:
			}
		}()
	}
	select {
	case <-accepted:
		return nil
	case <-share.exitedChan():
		return errors.New("process exited before it held itself open")
	case <-stalled:
		return errors.New("extension connection failed before the process held itself open")
	}
}

// retentionKey names the process a cell's factories run in: a Node cell shares one runtime process per plan group, and a compiled cell runs in its artifact.
func (h *Host) retentionKey(ctx context.Context, artifact string, node bool) string {
	if node {
		group, _ := ctx.Value(retentionGroupKey{}).(string)
		if group == "" {
			return ""
		}
		return "node:" + group
	}
	return "artifact:" + artifact
}

// withdrawProcessFromRetention keeps every later claim off the process hosting me, packed or isolated. The host calls it when a connection closes without the host closing it.
func (me *managedExt) withdrawProcessFromRetention() {
	share := me.share
	if me.packedProcess != nil {
		share = me.packedProcess.share
	}
	if share != nil {
		share.dead.Store(true)
	}
}

// claimRetainedProcess finds the live process the cell's factories can be re-invoked in.
func (h *Host) claimRetainedProcess(ctx context.Context, artifact string, node bool) *processShare {
	return h.retention().claim(h.retentionKey(ctx, artifact, node))
}
