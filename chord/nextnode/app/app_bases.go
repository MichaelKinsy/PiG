package app

import (
	"errors"
	"os"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports the bases' part of packages/chord/src/node/app/app.ts (Pi 284e63d9): `buildBases`, `watchBaseCode` and `editedSinceStart`.
// After the listen, off the start's path, the kit builds the bases (the surfaces' base bundles, each worker kind's base) and records
// the base code the `base` mark compares.
//
// Forced Go differences. A Promise is a channel closed when it settles (`BasesBuilt`, `BaseWatched`). The server's own code is its
// executable (D603), where Pi scans the static import graph of its start script; a worker kind's base is its executable too, which
// holds its code already (workers' doc.go): its build copies the entry (the check that it can be started) and records its bytes. The one
// goroutine that builds and records is the app's, joined by nothing: the close does not wait for it, and what it finishes after the
// close is dropped.

// futureSlack is how far in the future a file's timestamp may be and still count as an edit since the start: clock skew.
const futureSlack = 5 * time.Second

// editedSinceStartHash is what the record holds for a file changed since the process started: a hash no bytes have.
const editedSinceStartHash = "changed since the server started"

// processStart is when this process started, as far as the program can tell (Pi's `performance.timeOrigin`).
var processStart = time.Now() //portlint:allow clock Pi's `performance.timeOrigin`, the process's own start: the test seam is AppOptions.processStart

// baseBuild is a base the kit builds after the listen: its name in reports and its build, which returns the files it was built from
// or why it failed.
type baseBuild struct {
	name  string
	build func() (plugins.BaseInputs, error)
}

// comparisons are the first comparisons of every record added after the start, to wait for.
type comparisons struct {
	mu   sync.Mutex
	list []<-chan struct{}
}

func (c *comparisons) add(done <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list = append(c.list, done)
}

// wait returns once every comparison added so far ended.
func (c *comparisons) wait() {
	c.mu.Lock()
	list := append([]<-chan struct{}(nil), c.list...)
	c.mu.Unlock()
	for _, done := range list {
		<-done
	}
}

// BasesBuilt closes once every base (the web and terminal base bundles, each worker kind's base) was built after the start, or
// failed (reported, `base_build`). It never fails.
func (a *App) BasesBuilt() <-chan struct{} { return a.basesBuilt }

// BaseWatched closes once the base code is recorded, watched and compared once, after the start: the bases' records (after
// BasesBuilt) and the scan of the server's own code. Until then `Plugins.Marks().Base` is not known (`false` may mean "not
// compared yet"); the server's executable changed since the process started is marked until a restart (the running process may
// have other bytes than the ones read).
func (a *App) BaseWatched() <-chan struct{} { return a.baseWatched }

// isClosing is whether the app is closing or closed: what runs after the start stops there.
func (a *App) isClosing() bool { return a.closing.Load() }

// reportLate is a report of what runs after the start: nothing once the app closes (a build or scan finishing late).
func (a *App) reportLate(kind AppErrorKind, err any, base string) {
	if a.isClosing() {
		return
	}
	if a.options.OnError != nil {
		a.options.OnError(AppError{Kind: kind, Error: err, Base: base})
	}
}

// startBases starts building the bases right after the listen, at once, from the files as they are now (owner decision "A": nothing
// is kept between starts; the server answers meanwhile, each request needing a base waits for it), then records the server's own
// code. A built base's record is added to what the plugins compare; a failed one is reported (`base_build`) and the server runs on.
func (a *App) startBases(bases []baseBuild) {
	built, watched := a.basesBuilt, a.baseWatched
	//portlint:allow golifetime the bases' builds cannot be cancelled (esbuild) and the close does not wait for them: what they finish after it is dropped, as upstream's
	go func() {
		defer close(watched)
		compared := &comparisons{}
		var builds sync.WaitGroup
		for _, each := range bases {
			builds.Go(func() {
				inputs, err := each.build()
				if a.isClosing() {
					return
				}
				if err != nil {
					a.reportLate(AppErrorBaseBuild, err, each.name)
					return
				}
				compared.add(a.Plugins.WatchBaseCompared(inputs))
			})
		}
		builds.Wait()
		close(built)
		a.watchBaseCode(compared)
	}()
}

// watchBaseCode adds the server's own code to what the plugins compare, once the bases are built (the read of a large executable
// would contend with the builds the first requests wait for): its executable, recorded as differing when it changed since the
// process started (editedSinceStart). Best effort: one that cannot be read is reported (`server_scan`) and not compared; one under
// the Go build cache is never compared. It returns once every record's first comparison ended (the bases' too: `base` knows them
// all).
func (a *App) watchBaseCode(compared *comparisons) {
	if a.isClosing() {
		return
	}
	exe, ok := a.serverExecutable()
	if a.isClosing() {
		return
	}
	if ok {
		compared.add(a.Plugins.WatchBaseCompared(a.editedSinceStart(exe)))
	}
	compared.wait()
}

// editedSinceStart is the record of the file at path: its hash, or one no bytes have when it changed since this process started (its
// mtime or ctime after the start): the process may run other bytes than the ones read now, so its first comparison marks `base`
// (until a restart: the bytes the process runs are unknown). Conservative: a metadata-only change (ctime) marks too. Per timestamp:
// one later than now (plus futureSlack) is no edit since the start (a file stamped in the future, by a skewed clock or an archive,
// would mark every start until that time); a file neither of whose timestamps is in range keeps its hash, compared by content
// alone.
func (a *App) editedSinceStart(path string) plugins.BaseInputs {
	started := processStart
	if a.options.processStart != nil {
		started = a.options.processStart()
	}
	hash, readable := plugins.HashFile(path)
	if !readable {
		// Gone since it was checked: its first comparison finds it so.
		return plugins.BaseInputs{path: ""}
	}
	if info, err := os.Stat(path); err == nil {
		latest := time.Now().Add(futureSlack) //portlint:allow clock Pi's `Date.now() + FUTURE_SLACK_MS` of editedSinceStart: a file's timestamps are wall-clock, and the tests set AppOptions.processStart instead
		for _, stamp := range []time.Time{info.ModTime(), ctimeOf(info)} {
			if stamp.After(started) && !stamp.After(latest) {
				hash = editedSinceStartHash
				break
			}
		}
	}
	return plugins.BaseInputs{path: hash}
}

// baseBuilds are the bases of the app: its surfaces' base bundles, then each worker kind's base.
func (a *App) baseBuilds() []baseBuild {
	var bases []baseBuild
	for _, each := range a.surfaces {
		bases = append(bases, baseBuild{name: each.name, build: func() (plugins.BaseInputs, error) {
			built := each.build()
			return built.Inputs, built.Err
		}})
	}
	for _, kind := range a.options.Workers {
		name := kind.Kind.Kind
		bases = append(bases, baseBuild{name: name, build: func() (plugins.BaseInputs, error) {
			built := a.Workers[name].Build()
			return built.Inputs, built.Err
		}})
	}
	return bases
}

// linksAfterTheBases holds a link's upgrade until the bases are built: a client that connects while the server builds (a page
// reconnecting to a restarted server) would hold the scripts object's empty set, and its base is none the page runs (it reloads).
// Pi's client has that window too; its build is quicker than a Go server's, which starts the plugins' Node. A request the server
// closes meanwhile is refused. The options are the zero ones the links endpoint gave every socket.
func (a *App) linksAfterTheBases(chordhttp.HttpRequest, data.Principal) (chordhttp.LinkOptions, error) {
	select {
	case <-a.basesBuilt:
		return chordhttp.LinkOptions{}, nil
	case <-a.ended:
		return chordhttp.LinkOptions{}, errors.New("the app is closing")
	}
}
