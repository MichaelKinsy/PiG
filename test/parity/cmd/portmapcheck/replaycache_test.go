package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestMain(m *testing.M) {
	serveCacheServerIfAsked() // the binary is also the replays' cache server
	os.Exit(m.Run())
}

// scratchTemp points the temporary directory at a fresh one, where the replay directories of the test live.
func scratchTemp(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, tmp)
	}
	return tmp
}

// replayLeftovers lists what a replay leaves behind in the temporary directory and beside the build cache (the current and the legacy
// clone patterns), so one assertion covers both layouts.
func replayLeftovers(tmp, cacheParent string) []string {
	var left []string
	for _, g := range []string{filepath.Join(tmp, replayDirPrefix+"*"), filepath.Join(cacheParent, legacyClonePrefix+"*")} {
		m, _ := filepath.Glob(g)
		left = append(left, m...)
	}
	return left
}

func countFiles(t *testing.T, dir string) (n int) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// seedEntry stores body in dir in the layout of the go command's disk cache under the action id of key, as the go command does.
func seedEntry(t *testing.T, dir, key string, body []byte) (action, out [sha256.Size]byte) {
	t.Helper()
	action, out = sha256.Sum256([]byte("action "+key)), sha256.Sum256(body)
	for path, data := range map[string][]byte{
		fileName(dir, out[:], "d"):    body,
		fileName(dir, action[:], "a"): fmt.Appendf(nil, "v1 %x %x %20d %20d\n", action, out, len(body), time.Now().UnixNano()),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return action, out
}

// cacheClient is the go command's side of the protocol, in process.
type cacheClient struct {
	t    *testing.T
	in   *io.PipeWriter
	enc  *json.Encoder
	dec  *json.Decoder
	done chan error
	id   int64
}

func startServer(t *testing.T, shared, store, stats string) *cacheClient {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &cacheClient{t: t, in: inW, enc: json.NewEncoder(inW), dec: json.NewDecoder(outR), done: make(chan error, 1)}
	go func() {
		err := serveReplayCache(inR, outW, shared, store, stats)
		_ = outW.Close()
		c.done <- err
	}()
	var hello cacheResponse
	if err := c.dec.Decode(&hello); err != nil || hello.ID != 0 || len(hello.KnownCommands) != 3 {
		t.Fatalf("the first message must declare get, put and close: %v %+v", err, hello)
	}
	return c
}

func (c *cacheClient) call(req cacheRequest, body []byte) cacheResponse {
	c.t.Helper()
	c.id++
	req.ID = c.id
	if err := c.enc.Encode(req); err != nil {
		c.t.Fatal(err)
	}
	if req.BodySize > 0 {
		if err := c.enc.Encode(body); err != nil { // a []byte is a JSON string of base64, like the go command's body line
			c.t.Fatal(err)
		}
	}
	var res cacheResponse
	if err := c.dec.Decode(&res); err != nil || res.ID != req.ID {
		c.t.Fatalf("response to %+v: %v %+v", req, err, res)
	}
	return res
}

func (c *cacheClient) get(action [sha256.Size]byte) cacheResponse {
	return c.call(cacheRequest{Command: "get", ActionID: action[:]}, nil)
}

func (c *cacheClient) close() {
	c.t.Helper()
	if res := c.call(cacheRequest{Command: "close"}, nil); res.Err != "" {
		c.t.Fatal(res.Err)
	}
	_ = c.in.Close()
	if err := <-c.done; err != nil {
		c.t.Fatal(err)
	}
}

// TestCacheServerAnswersOnlyWhatItCanVerify: a lookup is a hit only for an entry that parses exactly as cmd/go parses it and whose output
// has the recorded size and digest; every other shape of the user's cache is a miss, never a hit, and the user's cache is never written.
func TestCacheServerAnswersOnlyWhatItCanVerify(t *testing.T) {
	root := t.TempDir()
	shared, store, stats := filepath.Join(root, "user"), filepath.Join(root, "store"), filepath.Join(root, "stats")
	good, goodOut := seedEntry(t, shared, "good", []byte("compiled package"))
	short, _ := seedEntry(t, shared, "truncated entry", []byte("x"))
	wrongSize, wrongSizeOut := seedEntry(t, shared, "wrong size", []byte("size"))
	wrongSum, wrongSumOut := seedEntry(t, shared, "wrong digest", []byte("digest"))
	dirOut, dirOutOut := seedEntry(t, shared, "executable", []byte("a cached executable is a directory"))
	moved, _ := seedEntry(t, shared, "moved entry", []byte("m"))
	if err := os.Truncate(fileName(shared, short[:], "a"), entrySize-1); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileName(shared, wrongSizeOut[:], "d"), []byte("sizes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileName(shared, wrongSumOut[:], "d"), []byte("digeSt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fileName(shared, dirOutOut[:], "d")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(fileName(shared, dirOutOut[:], "d"), "a.out"), 0o755); err != nil {
		t.Fatal(err)
	}
	// An entry file under the name of another action id.
	other := sha256.Sum256([]byte("another action"))
	if err := os.MkdirAll(filepath.Dir(fileName(shared, other[:], "a")), 0o755); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(fileName(shared, moved[:], "a")); err != nil || os.WriteFile(fileName(shared, other[:], "a"), b, 0o644) != nil {
		t.Fatal(err)
	}
	before := countFiles(t, shared)

	c := startServer(t, shared, store, stats)
	res := c.get(good)
	if res.Miss || res.DiskPath != fileName(store, goodOut[:], "d") || res.Size != int64(len("compiled package")) || !bytes.Equal(res.OutputID, goodOut[:]) {
		t.Fatalf("a good entry of the user's cache is a hit served from a copy in the store: %+v", res)
	}
	for name, action := range map[string][sha256.Size]byte{
		"an action nobody stored": sha256.Sum256([]byte("nothing")), "a truncated entry": short, "an output of another size": wrongSize,
		"an output of another digest": wrongSum, "an output that is a directory": dirOut, "an entry stored under another id": other,
	} {
		if r := c.get(action); !r.Miss || r.DiskPath != "" {
			t.Errorf("%s must be a miss, got %+v", name, r)
		}
	}
	// What the go command puts is stored in the store, answered from it, and never reaches the user's cache.
	put, putOut := sha256.Sum256([]byte("put action")), sha256.Sum256([]byte("mutated package"))
	pr := c.call(cacheRequest{Command: "put", ActionID: put[:], OutputID: putOut[:], BodySize: int64(len("mutated package"))}, []byte("mutated package"))
	if pr.Err != "" || pr.DiskPath != fileName(store, putOut[:], "d") {
		t.Fatalf("put: %+v", pr)
	}
	if r := c.get(put); r.Miss || r.DiskPath != pr.DiskPath || r.Size != int64(len("mutated package")) {
		t.Fatalf("a put must be a hit: %+v", r)
	}
	if got := countFiles(t, shared); got != before {
		t.Fatalf("the user's cache gained %d files", got-before)
	}
	c.close()
	b, _ := os.ReadFile(stats)
	if st := parseStats(string(b)); st != (cacheStats{Hits: 2, Misses: 6, Rejected: 5, Puts: 1, Copied: int64(len("compiled package"))}) {
		t.Fatalf("counters %+v from %q: want 2 hits (the user's entry and the put), 6 misses, 5 rejected entries, 1 put", st, b)
	}
}

// TestSharedHitIsACopyTheRunOwns: a hit in the user's cache is answered with a file of the run's own cache, so a trim of the user's cache
// (go deletes entries not used for five days) in the middle of a replay cannot take it away; and the copy is not the user's file: writing
// it leaves the user's bytes as they were.
func TestSharedHitIsACopyTheRunOwns(t *testing.T) {
	root := t.TempDir()
	shared, store := filepath.Join(root, "user"), filepath.Join(root, "store")
	body := []byte("compiled package of the user's cache")
	action, out := seedEntry(t, shared, "trimmed", body)
	c := startServer(t, shared, store, "")
	res := c.get(action)
	if res.Miss || res.DiskPath == fileName(shared, out[:], "d") || !strings.HasPrefix(res.DiskPath, store) {
		t.Fatalf("the hit must name a file of the store, not the user's: %+v", res)
	}
	for _, p := range []string{fileName(shared, out[:], "d"), fileName(shared, action[:], "a")} { // the trim
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := os.ReadFile(res.DiskPath); err != nil || !bytes.Equal(got, body) {
		t.Fatalf("the path handed out must still open after the user's file is gone, with the same bytes: %v %q", err, got)
	}
	if r := c.get(action); r.Miss || r.DiskPath != res.DiskPath { // later lookups are the store's own
		t.Fatalf("the adopted entry must stay a hit: %+v", r)
	}
	c.close()
}

// TestServerReadsTheLayoutOfGo pins the cache layout the server reads to the repository's go: a build into an ordinary cache is read back
// through the server, which must answer it with a copy at the place in the store where the go command put it in its own cache. When a Go release changes the layout this turns red, instead
// of every lookup silently missing.
func TestServerReadsTheLayoutOfGo(t *testing.T) {
	mod := t.TempDir()
	for name, body := range map[string]string{"go.mod": "module fixture\n\ngo 1.26\n", "lib/lib.go": "package lib\n\nconst X = 1\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(mod, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(mod, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	goList := func(env ...string) string {
		t.Helper()
		cmd := exec.Command("go", "list", "-export", "-f", "{{.Export}}", "./lib")
		cmd.Dir = mod
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go list: %v\n%s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	user := filepath.Join(t.TempDir(), "user")
	want := goList("GOCACHE=" + user) // go's own cache: where does it keep the export data of lib?
	root := t.TempDir()
	exe, err := quoteProgram(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	stats := filepath.Join(root, "stats")
	got := goList("GOCACHE="+filepath.Join(root, "store"), "GOCACHEPROG="+exe+" "+cacheServerArg, envSharedCache+"="+user, envStatsFile+"="+stats)
	wantRel, err1 := filepath.Rel(user, want)
	gotRel, err2 := filepath.Rel(filepath.Join(root, "store"), got)
	if err1 != nil || err2 != nil || gotRel != wantRel {
		t.Fatalf("through the server the export data of lib is at %q (in the store), go's own cache keeps it at %q: the server does not read go's layout", got, want)
	}
	b, _ := os.ReadFile(stats)
	if st := parseStats(string(b)); st.Hits == 0 || st.Rejected != 0 || st.Puts != 0 {
		t.Fatalf("the server must hit what go stored without a put or a rejection, counters %+v from %q", st, b)
	}
}

// TestServerEndsWithTheGoCommand: the cache server is started by the go command, which closes its stdin when it ends, or dies with it
// (a killed replay): the server must then exit by itself, so no server outlives its replay.
func TestServerEndsWithTheGoCommand(t *testing.T) {
	for _, how := range []string{"stdin closed", "stdin closed in the middle of a put"} {
		t.Run(how, func(t *testing.T) {
			root := t.TempDir()
			cmd := exec.Command(os.Args[0], cacheServerArg)
			cmd.Env = append(os.Environ(), envSharedCache+"="+filepath.Join(root, "user"), "GOCACHE="+filepath.Join(root, "store"))
			in, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			var hello cacheResponse
			if err := json.NewDecoder(out).Decode(&hello); err != nil || len(hello.KnownCommands) != 3 {
				t.Fatalf("hello: %v %+v", err, hello)
			}
			if how != "stdin closed" {
				_, _ = io.WriteString(in, `{"ID":1,"Command":"put","BodySize":9}`+"\n\"YWJj")
			}
			_ = in.Close()
			_, _ = io.Copy(io.Discard, out)
			_ = cmd.Wait() // blocks until the server has exited: a server that waited for more input would hold it until the package clock
		})
	}
}

// TestReplaysUseAPrivateCacheServedFromTheSharedOne: a replay gets a private GOCACHE inside a directory the run owns and a cache server that
// reads the user's cache; what replays add stays private and is removed after replaysPerTrim replays; the user's cache is never written;
// the directory goes at the end.
func TestReplaysUseAPrivateCacheServedFromTheSharedOne(t *testing.T) {
	tmp := scratchTemp(t)
	parent := t.TempDir()
	shared := filepath.Join(parent, "go-build")
	seedEntry(t, shared, "entry", []byte("compiled"))
	before := countFiles(t, shared)
	env := []string{"HOME=/x", "GOCACHE=" + shared}
	c := newReplayCaches()
	c.every = 3
	var cache string
	for i := 1; i <= 3; i++ {
		add, done, err := c.acquire(env)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, kv := range add {
			k, v, _ := strings.Cut(kv, "=")
			got[k] = v
		}
		if got["GOCACHE"] == "" || got["GOCACHE"] == shared || filepath.Dir(filepath.Dir(got["GOCACHE"])) != tmp ||
			!strings.HasPrefix(filepath.Base(filepath.Dir(got["GOCACHE"])), replayDirPrefix) {
			t.Fatalf("replay %d: GOCACHE=%q, want a cache inside a %s directory of %s", i, got["GOCACHE"], replayDirPrefix, tmp)
		}
		if !strings.HasSuffix(got["GOCACHEPROG"], " "+cacheServerArg) || got[envSharedCache] != shared {
			t.Fatalf("replay %d: the cache server is not wired to the user's cache: %v", i, add)
		}
		if cache != "" && got["GOCACHE"] != cache {
			t.Fatalf("replay %d: a second private cache %q beside %q", i, got["GOCACHE"], cache)
		}
		cache = got["GOCACHE"]
		if err := os.WriteFile(filepath.Join(cache, "mutant"+strconv.Itoa(i)), []byte("mutated"), 0o644); err != nil {
			t.Fatal(err)
		}
		done()
		if g, _ := filepath.Glob(filepath.Join(cache, "mutant*")); (i < 3) != (len(g) == i) {
			t.Fatalf("replay %d: after it the private cache holds %v; entries go only after %d replays", i, g, c.every)
		}
	}
	if g, _ := filepath.Glob(filepath.Join(cache, "*")); len(g) != 0 {
		t.Fatalf("after the reset the private cache must be empty: %v", g)
	}
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(cache), ownerFile)); err != nil || !strings.Contains(string(b), `"pid":`+strconv.Itoa(os.Getpid())) {
		t.Fatalf("the directory must carry its owner: %v %q", err, b)
	}
	if left := replayLeftovers(tmp, parent); len(left) != 1 {
		t.Fatalf("want one directory: %v", left)
	}
	c.close()
	if left := replayLeftovers(tmp, parent); len(left) != 0 {
		t.Fatalf("the directory outlived the check: %v", left)
	}
	if countFiles(t, shared) != before {
		t.Fatal("the user's cache was written to")
	}
	// A cache that does not exist is no reason to fail: every lookup misses.
	missing := newReplayCaches()
	if add, done, err := missing.acquire([]string{"GOCACHE=" + filepath.Join(parent, "missing")}); err != nil || len(add) == 0 {
		t.Fatalf("a missing user cache: %v %v", add, err)
	} else {
		done()
	}
	missing.close()
	if add, _, err := newReplayCaches().acquire([]string{"GOCACHE=off"}); err != nil || add != nil {
		t.Fatalf("a disabled cache needs nothing: %v %v", add, err)
	}
	if add, _, err := (*replayCaches)(nil).acquire(env); err != nil || add != nil {
		t.Fatalf("a nil *replayCaches adds nothing: %v %v", add, err)
	}
	if left := replayLeftovers(tmp, parent); len(left) != 0 {
		t.Fatalf("left over: %v", left)
	}
}

// TestReplaySetupDoesNotGrowWithTheCache: what the first replay creates is the same for a cache of ten files and of five thousand (the
// clone it replaces hard-linked every file of the cache first), and nothing of the cache is linked into it.
func TestReplaySetupDoesNotGrowWithTheCache(t *testing.T) {
	tmp := scratchTemp(t)
	var sizes []int
	for _, n := range []int{10, 5000} {
		shared := filepath.Join(t.TempDir(), "go-build")
		for i := range n {
			seedEntry(t, shared, strconv.Itoa(i), []byte("x"))
		}
		c := newReplayCaches()
		_, done, err := c.acquire([]string{"GOCACHE=" + shared})
		if err != nil {
			t.Fatal(err)
		}
		done()
		sizes = append(sizes, countFiles(t, tmp))
		c.close()
	}
	if sizes[0] != sizes[1] || sizes[0] > 2 {
		t.Fatalf("files created by setup for a cache of 10 and of 5000 files: %v, want the same few", sizes)
	}
}

// ownerOfThisProcess is the marker of a live owner.
func ownerOfThisProcess(t *testing.T) replayOwner {
	t.Helper()
	start, err := processStart(os.Getpid())
	if err != nil {
		t.Skipf("the start time of a process is not available here: %v", err)
	}
	return replayOwner{PID: os.Getpid(), Start: start, Scope: processScope(), Created: time.Now()}
}

func mkReplayDir(t *testing.T, parent, name string, owner *replayOwner, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(filepath.Join(dir, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if owner != nil {
		b, _ := json.Marshal(owner)
		if err := os.WriteFile(filepath.Join(dir, ownerFile), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(dir, when, when); err != nil {
		t.Fatal(err)
	}
	return dir
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// TestSweepRemovesOnlyWhatNoLiveRunOwns: a directory whose owner is alive is never removed by the sweep; one whose owner is gone, or whose
// pid is now another process, is; a directory that only has the name of a replay directory is removed by its age alone.
func TestSweepRemovesOnlyWhatNoLiveRunOwns(t *testing.T) {
	tmp := t.TempDir()
	now := time.Now()
	self := ownerOfThisProcess(t)
	// A process that has ended: its pid and start time are what a killed run's marker holds.
	child := exec.Command(os.Args[0], "-test.run=^$")
	if err := child.Run(); err != nil {
		t.Fatal(err)
	}
	gone := replayOwner{PID: child.Process.Pid, Start: "1", Scope: self.Scope, Created: now}
	reused := self
	reused.Start = self.Start + "0" // the same pid, started at another time: another process
	foreignScope := gone
	foreignScope.Scope = "another pid namespace"
	ancient := self
	ancient.Created = now.Add(-replayStaleAfter - time.Hour)

	live := mkReplayDir(t, tmp, replayDirPrefix+"live", &self, 0)
	deadOwner := mkReplayDir(t, tmp, replayDirPrefix+"dead", &gone, 0)
	pidReuse := mkReplayDir(t, tmp, replayDirPrefix+"reused", &reused, 0)
	otherScope := mkReplayDir(t, tmp, replayDirPrefix+"scope", &foreignScope, 0)
	liveButAncient := mkReplayDir(t, tmp, replayDirPrefix+"ancient", &ancient, 0)
	foreignYoung := mkReplayDir(t, tmp, replayDirPrefix+"foreign-young", nil, replayStaleAfter/2)
	foreignOld := mkReplayDir(t, tmp, replayDirPrefix+"foreign-old", nil, replayStaleAfter+time.Hour)
	unrelated := mkReplayDir(t, tmp, "other-"+replayDirPrefix, nil, replayStaleAfter+time.Hour)
	doomed := mkReplayDir(t, tmp, replayDirPrefix+"doomed", nil, 0)
	if err := os.WriteFile(filepath.Join(doomed, doomedFile), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	garbage := mkReplayDir(t, tmp, replayDirPrefix+"garbage-marker", nil, replayStaleAfter/2)
	if err := os.WriteFile(filepath.Join(garbage, ownerFile), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	asFile := filepath.Join(tmp, replayDirPrefix+"file")
	if err := os.WriteFile(asFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	_, _ = sweepReplayDirs(tmp, now)
	for name, tc := range map[string]struct {
		dir  string
		kept bool
	}{
		"an owner that is alive": {live, true}, "an owner that has ended": {deadOwner, false}, "a pid that another process now holds": {pidReuse, false},
		"an owner in another pid namespace (cannot be told)": {otherScope, true}, "an owner alive but past the bound": {liveButAncient, false},
		"no marker, younger than the bound": {foreignYoung, true}, "no marker, older than the bound": {foreignOld, false},
		"another name": {unrelated, true}, "a removal that was cut short": {doomed, false}, "an unreadable marker, younger than the bound": {garbage, true},
		"a file with the name": {asFile, true},
	} {
		if exists(tc.dir) != tc.kept {
			t.Errorf("%s: kept=%v, want %v", name, exists(tc.dir), tc.kept)
		}
	}
	// A link with the name is not followed: neither it nor its target is removed.
	t.Run("a symbolic link", func(t *testing.T) {
		target := mkReplayDir(t, t.TempDir(), "target", nil, replayStaleAfter+time.Hour)
		link := filepath.Join(tmp, replayDirPrefix+"link")
		testenv.RequireDirectoryLink(t, target, link)
		_, _ = sweepReplayDirs(tmp, now)
		if !exists(target) || !exists(link) {
			t.Errorf("a symbolic link with the name of a replay directory was followed")
		}
	})
}

// TestSweepRemovesLegacyClonesByAge: the clones the hard-linking version left beside the cache have no marker; they go once older than
// the bound, and a younger one (a run of that version may still use it) stays.
func TestSweepRemovesLegacyClonesByAge(t *testing.T) {
	parent := t.TempDir()
	now := time.Now()
	young := mkReplayDir(t, parent, legacyClonePrefix+"young", nil, time.Hour)
	old := mkReplayDir(t, parent, legacyClonePrefix+"old", nil, replayStaleAfter+time.Hour)
	if err := os.WriteFile(filepath.Join(old, "cache", "entry-d"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cut := mkReplayDir(t, parent, legacyClonePrefix+"cut", nil, 0)
	if err := os.WriteFile(filepath.Join(cut, doomedFile), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cache := mkReplayDir(t, parent, "go-build", nil, replayStaleAfter+time.Hour)
	_, _ = sweepLegacyClones(parent, now)
	if !exists(young) || exists(old) || exists(cut) || !exists(cache) {
		t.Fatalf("young kept=%v old kept=%v cut kept=%v the cache itself kept=%v", exists(young), exists(old), exists(cut), exists(cache))
	}
}

// TestCleanReplaysRemovesLeftovers: `portmapcheck -clean-replays` runs both sweeps now and says what it removed.
func TestCleanReplaysRemovesLeftovers(t *testing.T) {
	tmp := scratchTemp(t)
	parent := t.TempDir()
	t.Setenv("GOCACHE", filepath.Join(parent, "go-build"))
	orphan := mkReplayDir(t, tmp, replayDirPrefix+"orphan", nil, replayStaleAfter+time.Hour)
	legacy := mkReplayDir(t, parent, legacyClonePrefix+"old", nil, replayStaleAfter+time.Hour)
	var out strings.Builder
	if err := cleanReplayLeftovers(&out); err != nil {
		t.Fatal(err)
	}
	if exists(orphan) || exists(legacy) || !strings.Contains(out.String(), "2 leftover replay directories removed") {
		t.Fatalf("orphan kept=%v legacy kept=%v output %q", exists(orphan), exists(legacy), out.String())
	}
}

// TestCleanReplaysFailsWhatItCannotSweep: a cleanup that cannot read a directory or remove a leftover says so, naming the path, and
// fails; the other leftovers are still removed.
func TestCleanReplaysFailsWhatItCannotSweep(t *testing.T) {
	t.Run("a temporary directory that cannot be read", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "not-a-directory") // ReadDir of a file fails on every system and for every user
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
			t.Setenv(name, file)
		}
		t.Setenv("GOCACHE", filepath.Join(t.TempDir(), "go-build"))
		var out strings.Builder
		err := cleanReplayLeftovers(&out)
		if err == nil || !strings.Contains(err.Error(), file) {
			t.Fatalf("an unreadable temporary directory must fail and name %s, got %v", file, err)
		}
	})
	t.Run("a leftover that cannot be removed", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("a read-only directory does not stop its entries from being removed on Windows")
		}
		if os.Geteuid() == 0 {
			t.Skip("root removes entries of a read-only directory")
		}
		tmp := scratchTemp(t)
		t.Setenv("GOCACHE", filepath.Join(t.TempDir(), "go-build"))
		stuck := mkReplayDir(t, tmp, replayDirPrefix+"stuck", nil, replayStaleAfter+time.Hour)
		if err := os.WriteFile(filepath.Join(stuck, "cache", "f"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(stuck, "cache"), 0o500); err != nil { // its entry cannot be unlinked
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(stuck, "cache"), 0o700) })
		gone := mkReplayDir(t, tmp, replayDirPrefix+"gone", nil, replayStaleAfter+time.Hour)
		var out strings.Builder
		err := cleanReplayLeftovers(&out)
		if err == nil || !strings.Contains(err.Error(), stuck) {
			t.Fatalf("a leftover that cannot be removed must fail and name %s, got %v", stuck, err)
		}
		if exists(gone) || !strings.Contains(out.String(), "1 leftover replay directories removed") {
			t.Fatalf("the other leftover must still be removed: kept=%v output %q", exists(gone), out.String())
		}
	})
}

// TestCleanReplaysSweepsTheTempDirWhenTheBuildCacheIsNotFound: the two sweeps are independent. With no GOCACHE and no go to ask, the
// temporary directory is still swept and the lookup's error is returned.
func TestCleanReplaysSweepsTheTempDirWhenTheBuildCacheIsNotFound(t *testing.T) {
	tmp := scratchTemp(t)
	t.Setenv("GOCACHE", "")
	t.Setenv("PATH", t.TempDir()) // no go binary to ask
	orphan := mkReplayDir(t, tmp, replayDirPrefix+"orphan", nil, replayStaleAfter+time.Hour)
	var out strings.Builder
	err := cleanReplayLeftovers(&out)
	if err == nil || !strings.Contains(err.Error(), "cannot find the build cache") {
		t.Fatalf("the failed lookup must be returned, got %v", err)
	}
	if exists(orphan) || !strings.Contains(out.String(), "1 leftover replay directories removed") {
		t.Fatalf("the temporary directory must be swept anyway: orphan kept=%v output %q", exists(orphan), out.String())
	}
}

// TestMutantReplaysLeaveTheSharedBuildCacheAlone: whatever the go command of a replay writes into its build cache is gone when the check ends
// and never reaches the user's cache, which the replay still reads (through the cache server). The stand-in go runs everything else against the real cache, so nothing compiles cold.
func TestMutantReplaysLeaveTheSharedBuildCacheAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in go is a shell script")
	}
	const marked = "package lib\n\nimport \"testing\"\n\n// pi: packages/fx/src/widget.ts\nfunc TestWidget(t *testing.T) {\n\tif (Widget{Name: \"a\"}).Hello() != \"hello a\" {\n\t\tt.Fatal(\"hello\")\n\t}\n}\n"
	const mutant = `{"packages/fx/src/widget.ts":[{"file":"lib/widget.go","old":"\"hello \" + w.Name","new":"\"bye \" + w.Name"}]}`
	realCache := strings.TrimSpace(goEnv(t, "GOCACHE"))
	real, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	tmp := scratchTemp(t)
	parent := t.TempDir()
	shared := filepath.Join(parent, "go-build")
	if err := os.MkdirAll(filepath.Join(shared, "ab"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "ab", "entry-d"), []byte("compiled"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "seen.log")
	script := "#!/bin/sh\ncase \"$*\" in\n*-overlay*) [ -f \"$PORTMAP_REPLAY_SHARED_GOCACHE/ab/entry-d\" ] && [ -n \"$GOCACHEPROG\" ] && [ \"$GOCACHE\" != \"$PORTMAP_REPLAY_SHARED_GOCACHE\" ] && echo hit >> " + log + "; mkdir -p \"$GOCACHE/zz\" && echo mutated > \"$GOCACHE/zz/mutant-d\"; exit 1;;\nesac\nGOCACHE=" + realCache + " exec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOCACHE", shared)
	root := markerFixture(t, marked, mutant)
	res, err := checkWith(root, true)
	if err != nil || res[0].Proven {
		t.Fatalf("the replay fails, so the row is unproven and it is not an infrastructure error: %v %+v", err, res)
	}
	if b, _ := os.ReadFile(log); strings.TrimSpace(string(b)) != "hit" {
		t.Fatalf("the replay must see the user's entries through its cache server: %q", b)
	}
	if _, err := os.Stat(filepath.Join(shared, "zz")); !os.IsNotExist(err) {
		t.Fatalf("the replay wrote into the shared cache: %v", err)
	}
	if left := replayLeftovers(tmp, parent); len(left) != 0 {
		t.Fatalf("a replay directory outlived the check: %v", left)
	}
}

func goEnv(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// helperEnv makes TestReplayHelperProcess run a replay that never ends instead of returning at once.
const helperEnv = "PORTMAP_TEST_REPLAY_HELPER"

// TestReplayHelperProcess is the child of TestAKilledReplayLeavesNoOrphan: it replays a mutant with a stand-in go that never finishes.
func TestReplayHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		t.Skip("child of TestAKilledReplayLeavesNoOrphan")
	}
	root := markerFixture(t, "package lib\n\nimport \"testing\"\n\n// pi: packages/fx/src/widget.ts\nfunc TestWidget(t *testing.T) { _ = Widget{} }\n", `{}`)
	res := result{Cited: []string{"lib/widget.go"}, Tests: []string{"lib#TestWidget"}}
	mu := mutant{File: "lib/widget.go", Old: "\"hello \" + w.Name", New: "\"bye \" + w.Name"}
	killed(root, res, mu, newReplayCaches(), nil)
	os.Exit(0)
}

// standInGo puts a go first on PATH that reports its pid through a fifo when a replay runs it and then waits for an hour. It answers
// `go env` at once, which the check also runs.
func standInGo(t *testing.T) (fifo string) {
	t.Helper()
	dir := t.TempDir()
	fifo = filepath.Join(dir, "started")
	if out, err := exec.Command("mkfifo", fifo).CombinedOutput(); err != nil {
		t.Fatalf("mkfifo: %v %s", err, out)
	}
	script := "#!/bin/sh\n[ \"$1\" = env ] && exit 0\necho $$ > " + fifo + "\nexec sleep 3600\n"
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return fifo
}

// TestAKilledReplayLeavesNoOrphan: a replay killed with SIGKILL (a cancelled CI job, the package clock's panic, a stopped container) never
// runs close(). Whatever it created must not outlive the NEXT replay, which removes it.
func TestAKilledReplayLeavesNoOrphan(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in go is a shell script")
	}
	tmp := scratchTemp(t)
	cacheParent := t.TempDir()
	shared := filepath.Join(cacheParent, "go-build")
	seedEntry(t, shared, "entry", []byte("compiled"))
	t.Setenv("GOCACHE", shared)
	t.Setenv("GOMODCACHE", t.TempDir())
	fifo := standInGo(t)
	child := exec.Command(os.Args[0], "-test.run=^TestReplayHelperProcess$")
	child.Env = append(os.Environ(), helperEnv+"=1")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(fifo) // blocks until the stand-in go of the replay is running: the replay is in progress
	if err != nil {
		t.Fatal(err)
	}
	pidText, _ := io.ReadAll(f)
	_ = f.Close()
	if pid, err := strconv.Atoi(strings.TrimSpace(string(pidText))); err == nil {
		if p, err := os.FindProcess(pid); err == nil {
			t.Cleanup(func() { _ = p.Kill() })
		}
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	if left := replayLeftovers(tmp, cacheParent); len(left) != 1 {
		t.Fatalf("the killed replay should leave exactly its one directory, got %v", left)
	}
	c := newReplayCaches()
	_, done, err := c.acquire([]string{"GOCACHE=" + shared})
	if err != nil {
		t.Fatal(err)
	}
	done()
	if left := replayLeftovers(tmp, cacheParent); len(left) != 1 {
		t.Fatalf("the next replay must remove the orphan and hold only its own directory, got %v", left)
	}
	c.close()
	if left := replayLeftovers(tmp, cacheParent); len(left) != 0 {
		t.Fatalf("the orphan or the new replay's directory outlived the check: %v", left)
	}
}

// TestARealReplayHitsTheUsersCache: a mutant is killed through the cache server with the repository's go, and the replay's counters say
// its lookups hit the user's cache (the unmutated packages) and that what it compiled was put into the private cache.
func TestARealReplayHitsTheUsersCache(t *testing.T) {
	scratchTemp(t)
	const marked = "package lib\n\nimport \"testing\"\n\n// pi: packages/fx/src/widget.ts\nfunc TestWidget(t *testing.T) {\n\tif (Widget{Name: \"a\"}).Hello() != \"hello a\" {\n\t\tt.Fatal(\"hello\")\n\t}\n}\n"
	root := markerFixture(t, marked, `{}`)
	warm := exec.Command("go", "test", "-count=1", "-run", "^$", "./lib") // the user's cache holds the unmutated packages, as after the evidence builds
	warm.Dir = root
	if out, err := warm.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	res := result{Cited: []string{"lib/widget.go"}, Tests: []string{"lib#TestWidget"}}
	mu := mutant{File: "lib/widget.go", Old: "\"hello \" + w.Name", New: "\"bye \" + w.Name"}
	c := newReplayCaches()
	dead, infra, note := killed(root, res, mu, c, nil)
	c.close()
	if !dead || infra != "" {
		t.Fatalf("the mutant must be killed through the cache server: dead=%v infra=%q note=%q", dead, infra, note)
	}
	if c.stats.Hits == 0 || c.stats.Rejected != 0 || c.stats.Puts == 0 {
		t.Fatalf("the replay must hit the user's cache and put what it compiled: %s, %+v", c.summary(), c.stats)
	}
}

// BenchmarkReplaySetup: the time and allocations to get the first replay's cache and to give it up, against build caches of growing size
// (files of 1 KiB spread over 256 directories like the real one). PORTMAP_BENCH_CACHE names a real cache to measure as well.
func BenchmarkReplaySetup(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		b.Run("files="+strconv.Itoa(n), func(b *testing.B) {
			shared := filepath.Join(b.TempDir(), "go-build")
			body := make([]byte, 1024)
			for i := range n {
				dir := filepath.Join(shared, strconv.FormatInt(int64(i%256)+256, 16)[1:])
				if i < 256 {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						b.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(i)+"-d"), body, 0o644); err != nil {
					b.Fatal(err)
				}
			}
			benchSetup(b, shared)
		})
	}
	if real := os.Getenv("PORTMAP_BENCH_CACHE"); real != "" {
		b.Run("real", func(b *testing.B) { benchSetup(b, real) })
	}
}

func benchSetup(b *testing.B, shared string) {
	b.Helper()
	b.ReportAllocs()
	for b.Loop() {
		c := newReplayCaches()
		_, done, err := c.acquire([]string{"GOCACHE=" + shared})
		if err != nil {
			b.Fatal(err)
		}
		done()
		c.close()
	}
}
