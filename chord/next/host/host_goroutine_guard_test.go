package host

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Go-only (no Pi case): the guard of the host's join. A test's `delivered()` (Pi's `setTimeout(0)`, test/support.ts:138) runs after
// every promise continuation whose promise has settled, and in Go that is: every owned task that can run has run (the loop's join,
// loopseam.Quiesce). A goroutine that production code starts with `Loop.Go` or a bare `go`, or a callback it hands to
// `context.AfterFunc` (which the runtime starts on a goroutine of its own when the context is cancelled), is not counted: a test that
// asserts after `deliveredOn` fails at a rate, one test at a time. So the package starts a goroutine in three ways only: through the
// loop's task API (startTask, goTask, Loop.task, Loop.joined), through the loop's abort listener (Loop.onAbort), and at the sites
// below, each of which waits for something outside the loop and says what covers it.

// startsGoroutine is a line of production code that starts a goroutine the join does not count.
var startsGoroutine = regexp.MustCompile(`\.Go\(|(^|[^A-Za-z0-9_.])go\s+(func|[A-Za-z_][A-Za-z0-9_.]*\()|context\.AfterFunc\(`)

// uncountedSite is a line of production code that starts a goroutine the join does not count, and why that is right.
type uncountedSite struct {
	// file and code are the file and the trimmed code of the line (its comment cut), count how many such lines the file has.
	file, code string
	count      int
	// reason is what the goroutine is, and what covers it.
	reason string
	// pending is set while the line is converted by a branch that has not met this one: the entry may match nothing then.
	pending bool
}

var uncountedSites = []uncountedSite{
	{
		file: "host_loop.go", code: "l.tasks.Go(fn)", count: 1,
		reason: "Loop.Go itself: the WaitGroup Loop.Wait joins; the join of a test counts hops and tasks, which Loop.joined and Loop.task start",
	},
	{
		file: "host_loop_seam.go", code: "l.Go(func() {", count: 2,
		reason: "Loop.joined and Loop.task: they count the goroutine before they start it",
	},
	{
		file: "host_loop_seam.go", code: "loop.Go(fn)", count: 1,
		reason: "goTask of a loop that is not a *Loop (a test's virtual-time loop): there is no join to count it",
	},
	{
		file: "host_loop_lend.go", code: "l.Go(func() { c.stop() })", count: 1,
		reason: "the stop of a coroutine whose handler ended its goroutine (runtime.Goexit): a clean-up, no state of a host",
	},
	{
		file: "host_loop_abort.go", code: "stopAfter = context.AfterFunc(ctx, once)", count: 1,
		reason: "onAbortNow's listener for a source chordctx.OnAbort cannot listen to (a deadline, a stdlib cancel): the runtime's goroutine, which enters the loop through Queue (counted from there)",
	},
	{
		file: "host_promise.go", code: "stopAfter = context.AfterFunc(ctx, func() {", count: 1,
		reason: "thenWithContext's listener for a source chordctx.OnAbort cannot listen to (a deadline, a stdlib cancel): the runtime's goroutine, which enters the loop with Run",
	},
	{
		file: "host_host_data.go", code: "loop.Go(func() {", count: 1,
		reason: "acquireLine's wait for the documents line, a lock held by a commit: a wait on something outside the loop (a mutex Pi's `await line.acquire()` is), raced against ctx; its caller waits for the line itself, not a continuation a test's `delivered()` can overtake",
	},
	{
		file: "host_sleep.go", code: "go job()", count: 1,
		reason: "systemLoop.Queue of Sleep outside a host: sleepOn never calls Queue (sleep.ts:23 uses only setTimeout)",
	},
	{
		file: "remote_host_link.go", code: "l.host.loop().Go(func() {", count: 1,
		reason: "the inflate of a deflated part: Pi awaits the web DecompressionStream, real asynchronous IO that a `setTimeout(0)` does not wait for; a test waits with `transported`",
	},
	{
		file: "host_native.go", code: "owner.loop.Go(func() {", count: 1, pending: true,
		reason: "closeValue's closer: a counted goTask on port/chord-slice-14-p2c (72b7028d6); this entry may match nothing once the branches met",
	},
}

func TestProductionStartsNoGoroutineTheJoinDoesNotCount(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]int{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.SplitSeq(string(source), "\n") {
			code := strings.TrimSpace(withoutComment(line))
			if code != "" && startsGoroutine.MatchString(code) {
				found[file+"\x00"+code]++
			}
		}
	}
	listed := map[string]uncountedSite{}
	for _, site := range uncountedSites {
		if site.reason == "" {
			t.Errorf("%s: `%s` is listed without its reason", site.file, site.code)
		}
		listed[site.file+"\x00"+site.code] = site
	}
	for key, n := range found {
		file, code, _ := strings.Cut(key, "\x00")
		site, ok := listed[key]
		switch {
		case !ok:
			t.Errorf("%s: `%s` starts a goroutine the host's join does not count: an unawaited promise of Pi is an owned task (startTask, goTask), "+
				"an abort listener is Loop.onAbort; a wait for something outside the loop is listed in uncountedSites with its reason", file, code)
		case n != site.count:
			t.Errorf("%s: `%s` appears %d times, uncountedSites lists %d", file, code, n, site.count)
		}
	}
	for key, site := range listed {
		if found[key] == 0 && !site.pending {
			t.Errorf("%s: `%s` is listed in uncountedSites and no longer there: remove the entry", site.file, site.code)
		}
	}
}

// answersCounted is a method that answers sectionLoop.counted (a receiver without a name too).
var answersCounted = regexp.MustCompile(`^func \((\w+ )?\*?\w+\) counted\(\)`)

// Go-only (no Pi case): a loop that fronts the host's loop must not hide it from the tasks started on it. goTask starts a task on
// loop.counted(); a loop that answers nil has nothing to join, and a test's `delivered()` runs ahead of every task started on it
// (jobSections did, until sectionLoop asked: a persisted run's reservation, its run, its outcome and its finish were invisible to
// the join, so a test that asserted on what they write failed at a rate under load). A wrapper that embeds sectionLoop answers for
// the loop it wraps; only *Loop answers for itself, and only a test's loop (a _test.go file) says it counts nothing.
func TestNoProductionLoopHidesTheHostsCount(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var answers []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.SplitSeq(string(source), "\n") {
			if answersCounted.MatchString(line) {
				answers = append(answers, file+": "+strings.TrimSpace(withoutComment(line)))
			}
		}
	}
	want := "host_loop_seam.go: func (l *Loop) counted() *Loop { return l }"
	if len(answers) != 1 || answers[0] != want {
		t.Fatalf("the production answers of sectionLoop.counted are %q, want only %q: a production loop that wraps the host's embeds sectionLoop, "+
			"and one that counts nothing is a test's", answers, want)
	}
}

// withoutComment is line up to its `//` comment, if any (a `//` inside a string or a rune literal is not one).
func withoutComment(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == '\\' && quote != '`' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '`' || c == '\'':
			quote = c
		case c == '/' && i+1 < len(line) && line[i+1] == '/':
			return line[:i]
		}
	}
	return line
}
