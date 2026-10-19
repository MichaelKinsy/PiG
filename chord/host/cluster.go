package host

// Ports packages/chord/src/cluster.ts. Cross-host reload, coordinated by the server (the host without a parent):
//   - server: prepare itself and send prepare to every synced child host; collect votes;
//   - all ok: the server installs its new code version, sends commit to every child, and only then starts what may make
//     new-code version calls (tasks, listener and registry callbacks). Links are FIFO, so a child commits before it can
//     receive any frame of a root started in the new code version;
//   - any failure or missed vote deadline: abort everywhere; each host rolls back to where it was.
//
// Clients (browsers) are not participants: they see shape/reset pushes, or upgrade_required. A child that connects
// behind the server's code version is fenced and reconciled with the same protocol.

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// reloadSeq numbers prepare/vote exchanges, process-wide as upstream's module counter.
var reloadSeq atomic.Int64

func nextReloadID() int { return int(reloadSeq.Add(1)) }

// modulesFor are the modules of the longest prefix of hostName in remote ([] when none matches). An empty hostName is
// an unnamed peer (upstream undefined), which matches no prefix.
func modulesFor(remote map[string][]string, hostName string) []string {
	best, found := "", false
	for prefix := range remote {
		if hostName != "" && strings.HasPrefix(hostName, prefix) && (!found || len(prefix) > len(best)) {
			best, found = prefix, true
		}
	}
	return append([]string{}, remote[best]...)
}

// milliseconds formats a duration as upstream's millisecond number.
func milliseconds(d time.Duration) string {
	return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', -1, 64)
}

// awaitVote is connection's vote on reload id, or its rejection after deadline. It runs on the loop.
func awaitVote(host *HostImpl, connection *Connection, id int, deadline time.Duration) *Promise[*WireReloadReport] {
	vote, resolve, reject := newPromise[*WireReloadReport](&host.loop)
	connection.votes[id] = votePending{resolve: resolve, reject: reject}
	stop := host.after(deadline, func() {
		delete(connection.votes, id)
		reject(NewChordError(CodeAborted, "votes missing after "+milliseconds(deadline)+" ms", ChordErrorOptions{Reason: "vote_deadline"}))
	})
	vote.then(func(*WireReloadReport, error) { stop() })
	return vote
}

func wireDeadline(value *time.Duration) *float64 {
	if value == nil {
		return nil
	}
	ms := float64(*value) / float64(time.Millisecond)
	return &ms
}

func deadlineFromWire(value *float64) *time.Duration {
	if value == nil {
		return nil
	}
	//portlint:allow numbers a wire deadline is a JSON number of ms, so finite, and any deadline below 292 years fits a Duration
	d := time.Duration(*value * float64(time.Millisecond))
	return &d
}

func reportFromWire(report *WireReloadReport) ReloadReport {
	return ReloadReport{CodeVersion: report.CodeVersion, Previous: report.Previous, Changed: report.Changed, Removed: report.Removed, Prepared: report.Prepared, Gated: report.Gated}
}

func reportToWire(report ReloadReport) *WireReloadReport {
	return &WireReloadReport{CodeVersion: report.CodeVersion, Previous: report.Previous, Changed: report.Changed, Removed: report.Removed, Prepared: report.Prepared, Gated: report.Gated}
}

type childVote struct {
	name   string
	report *WireReloadReport
}

// runClusterReload reloads this host and every synced child host as one transaction. It runs off the loop.
func runClusterReload(host *HostImpl, plugins []PluginRef, options ReloadOptions) (ReloadReport, error) {
	var synced []*Connection
	var codeVersion int
	host.run(func() {
		// Participants are the children in step now. Fenced children (behind, being reconciled) reconcile to the new
		// code version afterwards; a child whose hello arrives while the reload runs is synced or reconciled once it ends
		// (HostImpl.peerHello).
		for _, connection := range host.connections.Keys() {
			if connection.role == "downstream" && connection.peer.kind == KindHost && connection.open() && connection.synced {
				synced = append(synced, connection)
			}
		}
		codeVersion = host.peekNextCodeVersion()
	})
	reload, err := beginReload(host, plugins, options, &codeVersion)
	if err != nil {
		return ReloadReport{}, err
	}
	defer host.run(func() { endReload(host) })
	// Retire first: its retire frames reach every child before prepare (FIFO), so a child never retires an code version this
	// host still runs roots of.
	reload.waitForRetirement()
	var participants []*Connection
	host.run(func() {
		for _, connection := range synced {
			if connection.open() {
				participants = append(participants, connection)
			}
		}
	})
	id := nextReloadID()
	deadline := durationOr(options.VoteDeadline, durationOr(options.GateDeadline, 5*time.Second)+durationOr(options.RetireDeadline, 5*time.Second)+5*time.Second)
	votes := make([]*Promise[childVote], len(participants))
	host.run(func() {
		for index, connection := range participants {
			name := connection.name()
			if name == "" {
				name = "child"
			}
			vote := awaitVote(host, connection, id, deadline)
			connection.send(PrepareFrame{
				Reload:      id,
				CodeVersion: codeVersion,
				Plugins:     modulesFor(options.Remote, name),
				Options: WireReloadOptions{
					Remove:         modulesFor(options.RemoteRemove, name),
					GateDeadline:   wireDeadline(options.GateDeadline),
					RetireDeadline: wireDeadline(options.RetireDeadline),
				},
			})
			named, resolve, reject := newPromise[childVote](&host.loop)
			vote.then(func(report *WireReloadReport, err error) {
				if err != nil {
					cause := reloadError(err)
					reject(NewChordError(cause.Code, name+": "+cause.Message, ChordErrorOptions{Cause: cause, Reason: cause.Reason}))
					return
				}
				resolve(childVote{name: name, report: report})
			})
			votes[index] = named
		}
	})
	local := false
	prepared, resolvePrepared, rejectPrepared := newPromise[childVote](&host.loop)
	host.goTask(func() {
		if err := reload.prepare(); err != nil {
			rejectPrepared(err)
			return
		}
		host.run(func() { local = true })
		resolvePrepared(childVote{})
	})
	//portlint:allow ctxbackground cluster.ts reload awaits Promise.allSettled with no abort; a missed vote settles its promise through awaitVote's deadline
	results, _ := promiseAllSettled(&host.loop, append([]*Promise[childVote]{prepared}, votes...)).Await(context.Background())
	for _, result := range results {
		if result.err == nil {
			continue
		}
		var rollback bool
		host.run(func() {
			for _, connection := range participants {
				delete(connection.votes, id)
				connection.send(AbortFrame{Reload: id})
			}
			rollback = local
		})
		if rollback {
			reload.rollback()
		}
		return ReloadReport{}, result.err
	}
	var report ReloadReport
	host.run(func() {
		if report, err = reload.commit(func() {
			for _, connection := range participants {
				connection.send(CommitFrame{Reload: id})
				connection.peerCodeVersion = codeVersion
			}
		}); err != nil {
			return
		}
		host.rememberRemoteModules(options.Remote, options.RemoteRemove)
	})
	if err != nil {
		return ReloadReport{}, err
	}
	report.Children = map[string]ReloadReport{}
	for _, result := range results[1:] {
		if result.value.report != nil {
			report.Children[result.value.name] = reportFromWire(result.value.report)
		}
	}
	return report, nil
}

// reconcileChild brings a child that connected behind this host's code version up to it: a reload of the child's modules (the
// ones cluster reloads gave its name) to this host's current code version, prepared, voted and committed like a cluster
// reload. Until then the child is fenced: nothing is routed to it. The child's old code is never relabeled as a newer
// code version. It runs off the loop.
func reconcileChild(host *HostImpl, connection *Connection) error {
	for {
		var codeVersion, id int
		var vote *Promise[*WireReloadReport]
		var reloadEnded <-chan struct{}
		done := false
		host.run(func() {
			if !connection.open() {
				done = true
				return
			}
			// Not during a reload of this host: the child is not one of its participants, so whatever it would be synced
			// to now may be superseded by that reload's commit. Recheck once it ended.
			if host.reloading {
				reloadEnded = host.whenReloadEnded()
				return
			}
			codeVersion = host.current.id
			if connection.peerCodeVersion == codeVersion {
				connection.markSynced(codeVersion)
				host.announceRetired(connection)
				done = true
				return
			}
			// Before prepare: the child's prepare first retires its previous code version, which waits for this permission.
			host.announceRetired(connection)
			id = nextReloadID()
			vote = awaitVote(host, connection, id, 30*time.Second)
			connection.send(PrepareFrame{
				Reload:      id,
				CodeVersion: codeVersion,
				Plugins:     modulesFor(host.remoteModules, connection.name()),
				Options:     WireReloadOptions{Remove: modulesFor(host.remoteRemoved, connection.name())},
			})
		})
		if done {
			return nil
		}
		if reloadEnded != nil {
			<-reloadEnded
			continue
		}
		//portlint:allow ctxbackground cluster.ts awaits the vote with no abort; awaitVote's deadline rejects it
		if _, err := vote.Await(context.Background()); err != nil {
			return err
		}
		host.run(func() {
			if host.current.id != codeVersion {
				// This host moved on meanwhile: abort and try again with the newer code version.
				connection.send(AbortFrame{Reload: id})
				return
			}
			connection.send(CommitFrame{Reload: id})
			connection.peerCodeVersion = codeVersion
		})
	}
}

// Child operation states.
const (
	childLoading   = "loading"
	childPreparing = "preparing"
	childPrepared  = "prepared"
)

// childOperation is one prepare the child received, registered before its first await so an abort or disconnect is
// never lost.
type childOperation struct {
	id        int
	reload    *Reload
	state     string
	cancelled bool
}

// ChildReloads is the child side: prepare and vote, then wait for the server's commit or abort. An abort or a lost
// parent link can arrive at any point: while loading modules, while preparing (pending opens close themselves when
// they complete), or after the vote. The operation is unwound once its pending work settles, and the reload slot is
// freed. Its methods run on the loop.
type ChildReloads struct {
	host      *HostImpl
	operation *childOperation
}

func newChildReloads(host *HostImpl) *ChildReloads { return &ChildReloads{host: host} }

func (reloads *ChildReloads) ref(connection *Connection, frame Frame) error {
	if prepare, ok := frame.(PrepareFrame); ok {
		if reloads.operation != nil {
			wire := ToWireError(NewChordError(CodeUnavailable, "a reload is already running", ChordErrorOptions{Reason: "reload_running"}))
			connection.send(VoteFrame{Reload: prepare.Reload, Error: &wire})
			return nil
		}
		operation := &childOperation{id: prepare.Reload, state: childLoading}
		reloads.operation = operation
		reloads.host.goTask(func() { reloads.run(operation, connection, prepare) })
		return nil
	}
	var id int
	switch typed := frame.(type) {
	case CommitFrame:
		id = typed.Reload
	case AbortFrame:
		id = typed.Reload
	default:
		return nil
	}
	operation := reloads.operation
	if operation == nil || operation.id != id {
		return nil
	}
	if _, ok := frame.(AbortFrame); ok {
		reloads.cancel(operation)
		return nil
	}
	if operation.state != childPrepared {
		return nil
	}
	reloads.operation = nil
	defer endReload(reloads.host)
	if operation.reload == nil {
		return nil
	}
	_, err := operation.reload.commit(func() {})
	return err
}

// parentLost rolls back when the parent link died: nobody will commit (now, or once pending work settles).
func (reloads *ChildReloads) parentLost() {
	if reloads.operation != nil {
		reloads.cancel(reloads.operation)
	}
}

func (reloads *ChildReloads) cancel(operation *childOperation) {
	host := reloads.host
	operation.cancelled = true
	if operation.state != childPrepared {
		// Loading or preparing: make prepare fail now; run unwinds when it settles.
		if reload := operation.reload; reload != nil {
			host.goTask(reload.rollback)
		}
		return
	}
	reloads.operation = nil
	reload := operation.reload
	if reload == nil {
		return
	}
	host.goTask(func() {
		reload.rollback()
		host.run(func() { endReload(host) })
	})
}

// run prepares operation and votes. It runs off the loop.
func (reloads *ChildReloads) run(operation *childOperation, connection *Connection, frame PrepareFrame) {
	host := reloads.host
	cancelled := func() error {
		return NewChordError(CodeAborted, "reload aborted by the server", ChordErrorOptions{Reason: "aborted"})
	}
	isCancelled := func(next string) (stop bool) {
		host.run(func() {
			if stop = operation.cancelled; !stop {
				operation.state = next
			}
		})
		return stop
	}
	err := func() error {
		plugins := make([]PluginRef, len(frame.Plugins))
		for index, module := range frame.Plugins {
			plugins[index] = ModuleRef(module)
		}
		options := ReloadOptions{
			Remove:         frame.Options.Remove,
			GateDeadline:   deadlineFromWire(frame.Options.GateDeadline),
			RetireDeadline: deadlineFromWire(frame.Options.RetireDeadline),
		}
		reload, err := beginReload(host, plugins, options, &frame.CodeVersion)
		if err != nil {
			return err
		}
		host.run(func() { operation.reload = reload })
		if isCancelled(childPreparing) {
			return cancelled()
		}
		if err := reload.prepare(); err != nil {
			return err
		}
		if isCancelled(childPrepared) {
			return cancelled()
		}
		host.run(func() { connection.send(VoteFrame{Reload: frame.Reload, Report: reportToWire(reload.summary())}) })
		return nil
	}()
	if err == nil {
		return
	}
	var reload *Reload
	host.run(func() { reload = operation.reload })
	if reload != nil {
		reload.rollback()
	}
	// One synchronous continuation, as upstream's after `await rollback()`: the reload slot, the operation and the
	// vote change together, so a prepare that arrives next finds both free.
	host.run(func() {
		if reload != nil {
			endReload(host)
		}
		if reloads.operation == operation {
			reloads.operation = nil
		}
		if !operation.cancelled {
			wire := ToWireError(err)
			connection.send(VoteFrame{Reload: frame.Reload, Error: &wire})
		}
	})
}
