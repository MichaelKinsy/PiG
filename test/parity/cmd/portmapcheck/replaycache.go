package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// A mutant replay compiles the mutated package and everything that imports it, and no later replay reads those entries: a shard with
// hundreds of replays would grow the user's Go build cache by gigabytes nothing reuses (a GitHub runner's disk ran out that way). The
// replays therefore never write the user's cache. Each `go test` of a replay runs with
//
//	GOCACHE      = <dir>/cache         a private cache of the run, holding only what the replays compile
//	GOCACHEPROG  = this binary         a cache server (replaycacheserver.go): it answers a lookup from the private cache first and then
//	                                   from the user's cache, read only, and stores what the go command puts into the private one
//
// so every entry the user's cache holds is still a hit, and the cache is never linked, cloned or written: setting a replay up costs the
// same for a cache of a megabyte and of a terabyte, and nothing the run leaves behind can pin a file of the user's cache. A hit is
// handed to the go command as a file of the private cache: the server copies that one verified output (adoptShared) on its first hit,
// because the go command keeps reading it for the whole replay and an ordinary `go` cache trim may delete the user's file meanwhile.
//
// <dir> is a directory of the system's temporary directory that the run owns (replayDirPrefix + random). It carries an owner marker
// (ownerFile: pid, the owner's start time, creation time). A run that is killed never reaches close(), so the first replay of every later
// run sweeps the directories whose owner is gone (sweepReplayDirs), and the legacy clones of the version that hard-linked the whole cache
// beside it (legacyClonePrefix, sweepLegacyClones) once they are older than replayStaleAfter. `portmapcheck -clean-replays` does both
// at once.
const (
	replayDirPrefix   = "portmap-replay-"
	legacyClonePrefix = ".portmap-gocache-"
	ownerFile         = "owner.json"
	statsFile         = "stats"
	// doomedFile is written into a directory before its removal starts: a removal that is cut short (the sweep's own process is killed,
	// or a multi-terabyte legacy clone takes minutes) leaves a directory whose age no longer says anything, and this marks it for the
	// next sweep whatever its age.
	doomedFile = ".portmap-doomed"
	// replayStaleAfter is the age beyond which a directory of a replay is removed whoever its owner is, and the age a directory with the
	// name of a replay directory but no valid marker (or a legacy clone, which has none) must reach before it is removed. The longest
	// legitimate run is a CI shard (the job limit of GitHub-hosted runners is 6 hours; the package clock of this tool's tests is 10
	// minutes), so 12 hours is twice the longest run any runner allows and far above any developer's.
	replayStaleAfter = 12 * time.Hour
	// replaysPerTrim is how many replays may add entries to the private cache before they are removed.
	replaysPerTrim = 16

	envSharedCache = "PORTMAP_REPLAY_SHARED_GOCACHE" // the user's cache the cache server reads
	envStatsFile   = "PORTMAP_REPLAY_STATS"          // where each cache server appends its counters
	cacheServerArg = "-portmap-replay-cache-server"  // the one argument of the binary when it runs as the cache server
)

// errProcessGone says a process does not exist (or is a zombie); any other error of processStart means its state cannot be told.
var errProcessGone = errors.New("no such process")

// replayOwner is the content of ownerFile.
type replayOwner struct {
	PID   int    `json:"pid"`
	Start string `json:"start"` // processStart of PID when the directory was made; "" when the system cannot tell
	// Scope names what a pid is relative to (processScope); an owner of another scope cannot be looked up here.
	Scope   string    `json:"scope"`
	Created time.Time `json:"created"`
}

// ownerAlive reports whether the process that made a directory may still be running. It answers true whenever it cannot tell: a
// directory is then removed only by its age.
func ownerAlive(o replayOwner) bool {
	if o.Scope != processScope() {
		return true
	}
	start, err := processStart(o.PID)
	switch {
	case errors.Is(err, errProcessGone):
		return false
	case err != nil || o.Start == "":
		return true
	}
	return start == o.Start // a pid reused by another process has another start time
}

// reapable reports whether dir, which has the name of a replay directory, may be removed now.
func reapable(dir string, now time.Time) bool {
	if _, err := os.Lstat(filepath.Join(dir, doomedFile)); err == nil {
		return true
	}
	if b, err := os.ReadFile(filepath.Join(dir, ownerFile)); err == nil {
		var o replayOwner
		if json.Unmarshal(b, &o) == nil && o.PID > 0 && !o.Created.IsZero() {
			return now.Sub(o.Created) > replayStaleAfter || !ownerAlive(o)
		}
	}
	info, err := os.Lstat(dir)
	return err == nil && now.Sub(info.ModTime()) > replayStaleAfter
}

// sweepDirs removes the directories of parent whose name starts with prefix and which are reapable, and returns them. Symbolic links
// are never followed. What it cannot read or remove it names in the error (a parent that does not exist has nothing to sweep); it goes on
// to the next directory either way.
func sweepDirs(parent, prefix string, now time.Time) (removed []string, err error) {
	entries, rerr := os.ReadDir(parent)
	if rerr != nil {
		if errors.Is(rerr, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read %s: %w", parent, rerr)
	}
	var errs []error
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		dir := filepath.Join(parent, e.Name())
		if !reapable(dir, now) {
			continue
		}
		_ = os.WriteFile(filepath.Join(dir, doomedFile), nil, 0o600)
		if rerr := os.RemoveAll(dir); rerr != nil {
			errs = append(errs, fmt.Errorf("cannot remove %s: %w", dir, rerr))
			continue
		}
		removed = append(removed, dir)
	}
	return removed, errors.Join(errs...)
}

// sweepReplayDirs removes the replay directories in tmp whose owner is gone (killed, or its pid reused), and every one older than
// replayStaleAfter. A directory of a live owner, and a directory with the name but no marker that is younger than the bound, stay.
func sweepReplayDirs(tmp string, now time.Time) ([]string, error) {
	return sweepDirs(tmp, replayDirPrefix, now)
}

// sweepLegacyClones removes the clones the version that hard-linked the build cache left beside it (parent is the directory that holds
// the cache) once they are older than replayStaleAfter: they carry no marker, so age is all there is to go by. Each pins every file of
// the cache of its moment, which is why they are removed.
func sweepLegacyClones(parent string, now time.Time) ([]string, error) {
	return sweepDirs(parent, legacyClonePrefix, now)
}

// cleanReplayLeftovers is `portmapcheck -clean-replays`: both sweeps, run to the end. They are independent: the temporary directory is
// swept whether or not the build cache can be found. It reports what it removed, and returns what failed (the lookup of the build
// cache, and every directory it could not read or remove), so the command exits non-zero when a leftover may still be there.
func cleanReplayLeftovers(w io.Writer) error {
	now := time.Now()
	removed, errs := sweepReplayDirs(os.TempDir(), now)
	cache, lerr := buildCacheDir()
	if lerr != nil {
		errs = errors.Join(errs, fmt.Errorf("cannot find the build cache, so the legacy clones beside it were not swept: %w", lerr))
	} else if cache != "" && cache != "off" {
		legacy, serr := sweepLegacyClones(filepath.Dir(cache), now)
		removed, errs = append(removed, legacy...), errors.Join(errs, serr)
	}
	for _, d := range removed {
		if _, err := fmt.Fprintln(w, "removed", d); err != nil {
			return errors.Join(err, errs)
		}
	}
	_, err := fmt.Fprintf(w, "portmap-check: %d leftover replay directories removed\n", len(removed))
	return errors.Join(err, errs)
}

// buildCacheDir is the user's build cache: GOCACHE, else what `go env GOCACHE` says.
func buildCacheDir() (string, error) {
	if v := os.Getenv("GOCACHE"); v != "" {
		return v, nil
	}
	out, err := exec.Command("go", "env", "GOCACHE").Output()
	return strings.TrimSpace(string(out)), err
}

// replayCaches gives the replays of a run their cache (see above). Every replaysPerTrim replays, while none runs, what the replays added
// to the private cache is removed; the directory is removed at the end.
type replayCaches struct {
	every  int
	mu     sync.RWMutex // replays hold it shared; a reset or the final removal holds it exclusively
	initMu sync.Mutex   // guards the directory's creation
	dir    string       // the run's directory; "" before the first replay and after close
	failed error        // why the directory could not be made
	cnt    sync.Mutex
	done   int // replays since the last reset
	// What the cache servers that have exited reported (see cacheStats).
	stats cacheStats
}

func newReplayCaches() *replayCaches { return &replayCaches{every: replaysPerTrim} }

// quoteProgram writes path as the one word GOCACHEPROG's own splitting (single or double quotes, no escapes) reads back.
func quoteProgram(path string) (string, error) {
	switch {
	case !strings.ContainsFunc(path, func(r rune) bool { return unicode.IsSpace(r) || r == '"' || r == '\'' }):
		return path, nil
	case !strings.Contains(path, `"`):
		return `"` + path + `"`, nil
	case !strings.Contains(path, "'"):
		return "'" + path + "'", nil
	}
	return "", errors.New("the path of this program holds spaces and both kinds of quote, which GOCACHEPROG cannot carry: " + path)
}

// newReplayDir sweeps what earlier runs left and makes the directory of this one, marked with its owner.
func (c *replayCaches) newReplayDir(userCache string) error {
	now := time.Now()
	tmp := os.TempDir()
	_, _ = sweepReplayDirs(tmp, now)
	// A legacy clone can be terabytes of links: remove it beside the replays, never before them.
	go func() { _, _ = sweepLegacyClones(filepath.Dir(userCache), now) }()
	dir, err := os.MkdirTemp(tmp, replayDirPrefix)
	if err != nil {
		return err
	}
	start, _ := processStart(os.Getpid())
	marker, _ := json.Marshal(replayOwner{PID: os.Getpid(), Start: start, Scope: processScope(), Created: now})
	if err := os.WriteFile(filepath.Join(dir, ownerFile), marker, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	if err := os.Mkdir(filepath.Join(dir, "cache"), 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	c.dir = dir
	return nil
}

// acquire returns the environment a replay must add to its own (nil: the cache is disabled, so the replay needs none; a nil
// *replayCaches answers the same) and the function that gives it back. The error says the run's directory or the cache server's
// command could not be made: that is infrastructure, and the replay must not fall back to the user's cache.
func (c *replayCaches) acquire(env []string) (add []string, release func(), err error) {
	none := func() {}
	if c == nil {
		return nil, none, nil
	}
	user := ""
	for _, kv := range slices.Backward(env) {
		if v, ok := strings.CutPrefix(kv, "GOCACHE="); ok {
			user = v
			break
		}
	}
	if user == "" || user == "off" {
		return nil, none, nil
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = quoteProgram(exe)
	}
	if err != nil {
		return nil, none, err
	}
	c.initMu.Lock()
	if c.dir == "" && c.failed == nil {
		c.failed = c.newReplayDir(user)
	}
	dir, failed := c.dir, c.failed
	c.initMu.Unlock()
	if failed != nil {
		return nil, none, failed
	}
	if dir == "" {
		return nil, none, errors.New("the replay caches are closed")
	}
	c.mu.RLock()
	cache := filepath.Join(dir, "cache")
	return []string{"GOCACHE=" + cache, "GOCACHEPROG=" + exe + " " + cacheServerArg, envSharedCache + "=" + user, envStatsFile + "=" + filepath.Join(dir, statsFile)}, func() {
		c.mu.RUnlock()
		c.cnt.Lock()
		c.done++
		due := c.done >= c.every
		c.cnt.Unlock()
		if due {
			c.reset()
		}
	}, nil
}

// absorbStats adds what the cache servers of the directory reported to the totals. The caller holds mu exclusively.
func (c *replayCaches) absorbStats() {
	if b, err := os.ReadFile(filepath.Join(c.dir, statsFile)); err == nil {
		c.stats.add(parseStats(string(b)))
		_ = os.Remove(filepath.Join(c.dir, statsFile))
	}
}

// reset empties the private cache once the replays running now have finished.
func (c *replayCaches) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cnt.Lock()
	c.done = 0
	c.cnt.Unlock()
	if c.dir == "" {
		return
	}
	c.absorbStats()
	cache := filepath.Join(c.dir, "cache")
	_ = os.RemoveAll(cache)
	_ = os.Mkdir(cache, 0o700)
}

// close removes the run's directory.
func (c *replayCaches) close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dir != "" {
		c.absorbStats()
		_ = os.RemoveAll(c.dir)
		c.dir = ""
	}
}

// summary is the counters of the replays' cache servers, for the run's report. Read it after close.
func (c *replayCaches) summary() string {
	if c == nil {
		return "none"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats.String()
}

// cacheStats counts what the cache servers of a run answered. A server that cannot read the user's cache's entries any more (a Go
// release that changed the layout) answers every lookup as a miss and the build still passes, only slowly: the counters make that
// visible in the report ("0 hits").
type cacheStats struct {
	Hits     int // answered from the user's cache or from what the replays stored
	Misses   int // not in either, and so compiled
	Rejected int // entries of the user's cache the server read but would not trust (malformed, wrong size or digest); they count as misses
	Puts     int
	Copied   int64 // bytes of the user's cache entries copied into the run's own cache (hits are copied, not served in place)
}

func (s *cacheStats) add(o cacheStats) {
	s.Hits += o.Hits
	s.Misses += o.Misses
	s.Rejected += o.Rejected
	s.Puts += o.Puts
	s.Copied += o.Copied
}

func (s cacheStats) String() string {
	out := strconv.Itoa(s.Hits) + " hits, " + strconv.Itoa(s.Misses) + " misses"
	if s.Rejected > 0 {
		out += " (" + strconv.Itoa(s.Rejected) + " user cache entries rejected)"
	}
	if s.Copied > 0 {
		out += ", " + strconv.FormatInt(s.Copied>>10, 10) + " KiB copied from the user cache"
	}
	return out
}

// statsLine is one cache server's line of statsFile.
func (s cacheStats) statsLine() string {
	return fmt.Sprintf("%d %d %d %d %d\n", s.Hits, s.Misses, s.Rejected, s.Puts, s.Copied)
}

func parseStats(text string) (s cacheStats) {
	for line := range strings.SplitSeq(text, "\n") {
		var o cacheStats
		if n, _ := fmt.Sscanf(line, "%d %d %d %d %d", &o.Hits, &o.Misses, &o.Rejected, &o.Puts, &o.Copied); n == 5 {
			s.add(o)
		}
	}
	return s
}
