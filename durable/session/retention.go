package session

import (
	"container/list"
	"math"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
)

// The document cache is bounded by use (session.ts SessionImpl): a document with no watch attached
// leaves it once it was not read or written for the retention period, and the next access loads it from Storage.
// Eviction runs as a job on the mutation line, so no transaction ever holds a tracker that leaves.

// defaultDocumentRetentionMs is the default document retention: ten minutes.
// upstream: packages/durable/src/session/session.ts:defaultRetention
const defaultDocumentRetentionMs = 600_000

// DocumentRetention returns ms when it is a number of milliseconds, 0 or more (+Inf keeps documents); otherwise the
// default. A nil ms and a NaN are not such a number.
func DocumentRetention(ms *float64) float64 {
	if ms != nil && *ms >= 0 {
		return *ms
	}
	return defaultDocumentRetentionMs
}

// idleDocument is one cached document with no observer attached, and the time of its last use.
type idleDocument struct {
	addressId string
	since     float64
}

// documentSweep is the pending eviction of the oldest idle document. timer is nil for a sweep queued on the line at
// once; it is guarded by the Session mutex.
type documentSweep struct {
	at     float64
	disarm func()
}

// documentCache is the retention bookkeeping of the Session's loaded documents, guarded by SessionImpl.mu.
type documentCache struct {
	// idle holds the cached documents with no observer attached, oldest use first.
	idle      map[string]*list.Element
	idleOrder *list.List
	// attached counts observers per document incarnation (record ID); a cached incarnation with any is never evicted.
	attached map[durable.DocumentId]int
	// sweep is the pending eviction, until its job runs.
	sweep     *documentSweep
	sweepJobs int
}

func newDocumentCache() documentCache {
	return documentCache{idle: map[string]*list.Element{}, idleOrder: list.New(), attached: map[durable.DocumentId]int{}}
}

// clearIdle forgets the idle documents; the caller drops them from the cache.
func (cache *documentCache) clearIdle() {
	clear(cache.idle)
	cache.idleOrder.Init()
}

func (cache *documentCache) removeIdle(addressId string) {
	if element, ok := cache.idle[addressId]; ok {
		cache.idleOrder.Remove(element)
		delete(cache.idle, addressId)
	}
}

// clockNow is the Session's wall clock. Cache bookkeeping must never change the outcome of the operation it follows: a
// clock that panics counts as now.
func (session *SessionImpl) clockNow() (now float64) {
	defer func() {
		// upstream: packages/durable/src/session/session.ts:#clock
		if recover() != nil {
			now = float64(time.Now().UnixMilli())
		}
	}()
	return session.now()
}

// retention is the document retention in milliseconds, read at each use. A retention source that panics counts as
// the default.
func (session *SessionImpl) retention() (ms float64) {
	defer func() {
		// upstream: packages/durable/src/session/session.ts:#documentRetentionMs
		if recover() != nil {
			ms = DocumentRetention(nil)
		}
	}()
	if session.retentionSource == nil {
		return DocumentRetention(nil)
	}
	return DocumentRetention(session.retentionSource())
}

// dropDocumentLocked removes a cached document with the Session mutex held.
func (session *SessionImpl) dropDocumentLocked(addressId string) {
	delete(session.documents, addressId)
	session.cache.removeIdle(addressId)
}

// touchDocument notes a use of a cached document: an unattached one becomes the newest idle one, and an eviction is
// scheduled.
func (session *SessionImpl) touchDocument(addressId string) {
	session.mu.Lock()
	cached := session.documents[addressId]
	if cached == nil || session.cache.attached[cached.Record.Id] > 0 {
		session.mu.Unlock()
		return
	}
	session.mu.Unlock()
	// The clock and the retention source are host callbacks: they run outside the mutex.
	now := session.clockNow()
	retention := session.retention()
	session.mu.Lock()
	cached = session.documents[addressId]
	if cached == nil || session.cache.attached[cached.Record.Id] > 0 {
		session.mu.Unlock()
		return
	}
	// A hot document moves to the back at most once per granule, a tenth of the retention up to 1 s: it may then leave
	// up to a granule early, which costs a reload, while a commit-heavy workload skips the reordering.
	if element, ok := session.cache.idle[addressId]; ok && now-element.Value.(*idleDocument).since < math.Min(1_000, retention/10) {
		session.mu.Unlock()
		return
	}
	session.cache.removeIdle(addressId)
	session.cache.idle[addressId] = session.cache.idleOrder.PushBack(&idleDocument{addressId: addressId, since: now})
	// A pending sweep for the oldest document comes first unless the retention was shortened since; then it moves up.
	reschedule := session.cache.sweep == nil || session.cache.sweep.at > now+retention
	session.mu.Unlock()
	if reschedule {
		session.scheduleDocumentSweep(now, retention)
	}
}

// scheduleDocumentSweep schedules the eviction of the oldest idle document: on the line at once when it expired, otherwise
// when it expires, by a timer. now and retention are the clock and the retention the caller read.
func (session *SessionImpl) scheduleDocumentSweep(now, retention float64) {
	session.mu.Lock()
	if session.closing != nil {
		session.mu.Unlock()
		return
	}
	oldest := session.cache.idleOrder.Front()
	if oldest == nil {
		session.mu.Unlock()
		return
	}
	at := oldest.Value.(*idleDocument).since + retention
	if session.cache.sweep != nil && session.cache.sweep.at <= at {
		session.mu.Unlock()
		return
	}
	if session.cache.sweep != nil && session.cache.sweep.disarm != nil {
		session.cache.sweep.disarm()
	}
	// The pending sweep stays set until its job runs, so touches while the line is busy queue no more.
	sweep := &documentSweep{at: at}
	session.cache.sweep = sweep
	delay := at - now
	if delay <= 0 {
		session.mu.Unlock()
		session.queueDocumentSweep(sweep)
		return
	}
	// A background timer (HarnessOptions.SetTimer); a host that declines it leaves eviction to the end of each commit
	// (session.ts #documentTimersUnavailable). A timer that fires early sweeps nothing and arms the next one.
	if session.documentTimersUnavailable {
		session.cache.sweep = nil
		session.mu.Unlock()
		return
	}
	session.mu.Unlock()
	disarm := session.timers.Background(func() { session.queueDocumentSweep(sweep) }, delay)
	session.mu.Lock()
	if disarm == nil {
		session.documentTimersUnavailable = true
		if session.cache.sweep == sweep {
			session.cache.sweep = nil
		}
	} else {
		sweep.disarm = disarm
	}
	session.mu.Unlock()
}

// queueDocumentSweep queues the sweep's job on the mutation line. The job joins the line's FIFO of jobs, which the close
// job follows, so Storage closes only after it settled.
func (session *SessionImpl) queueDocumentSweep(sweep *documentSweep) {
	session.mu.Lock()
	session.cache.sweepJobs++
	session.mu.Unlock()
	session.enqueueDetached(func() {
		session.mu.Lock()
		if session.cache.sweep == sweep {
			session.cache.sweep = nil
		}
		closing := session.closing != nil
		session.mu.Unlock()
		if !closing {
			session.sweepDocuments()
		}
	})
}

// enqueueDetached queues job on the mutation line without waiting for it: it takes its ticket now, so it runs before any
// job queued later, and runs on its own goroutine once the earlier jobs settled.
func (session *SessionImpl) enqueueDetached(job func()) {
	session.lineMu.Lock()
	previous := session.tail
	done := make(chan struct{})
	session.tail = done
	session.lineJobs++
	session.lineMu.Unlock()
	go func() {
		defer func() {
			session.lineMu.Lock()
			session.lineJobs--
			session.lineMu.Unlock()
			close(done)
		}()
		if previous != nil {
			<-previous
		}
		job()
	}()
}

// sweepDocuments runs on the line: it evicts the idle documents whose retention passed, oldest first, then schedules the
// next eviction.
func (session *SessionImpl) sweepDocuments() {
	now := session.clockNow()
	retention := session.retention()
	expired := now - retention
	session.mu.Lock()
	for element := session.cache.idleOrder.Front(); element != nil; element = session.cache.idleOrder.Front() {
		idle := element.Value.(*idleDocument)
		if idle.since > expired {
			break
		}
		session.dropDocumentLocked(idle.addressId)
	}
	session.mu.Unlock()
	session.scheduleDocumentSweep(now, retention)
}

// pinDocument pins a cached incarnation for one observer. The returned release unpins it once; the document is idle
// again after the last.
func (session *SessionImpl) pinDocument(addressId string, recordId durable.DocumentId) (release func()) {
	session.mu.Lock()
	session.cache.attached[recordId]++
	session.cache.removeIdle(addressId)
	session.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			session.mu.Lock()
			count, ok := session.cache.attached[recordId]
			if !ok {
				count = 1
			}
			count--
			if count > 0 {
				session.cache.attached[recordId] = count
				session.mu.Unlock()
				return
			}
			delete(session.cache.attached, recordId)
			cached := session.documents[addressId]
			current := cached != nil && cached.Record.Id == recordId
			session.mu.Unlock()
			if current {
				session.touchDocument(addressId)
			}
		})
	}
}

// DocumentCacheSizes is what the document cache holds.
type DocumentCacheSizes struct {
	// Cached is the number of cached documents.
	Cached int
	// Idle is the number of cached documents with no observer attached.
	Idle int
	// Attached is the number of incarnations pinned by an observer.
	Attached int
	// Sweeps is the number of eviction jobs queued on the line so far.
	Sweeps int
}

// DocumentCacheSizes reports how many documents the cache holds, how many are idle, how many incarnations are pinned, and
// how many eviction jobs were queued on the line so far.
func (session *SessionImpl) DocumentCacheSizes() DocumentCacheSizes {
	session.mu.Lock()
	defer session.mu.Unlock()
	return DocumentCacheSizes{
		Cached:   len(session.documents),
		Idle:     len(session.cache.idle),
		Attached: len(session.cache.attached),
		Sweeps:   session.cache.sweepJobs,
	}
}

// touchChanged notes the use of every document a commit changed.
func (session *SessionImpl) touchChanged(documents []durable.DocumentCommitChange) {
	for _, change := range documents {
		var record durable.DocumentRecord
		switch change := change.(type) {
		case durable.DocumentChange:
			record = change.Record
		case durable.DocumentCopyChange:
			record = change.Record
		}
		session.touchDocument(durable.AddressId(durable.DocumentAddress{Kind: record.Kind, Scope: record.Scope, Key: record.Key}))
	}
}
