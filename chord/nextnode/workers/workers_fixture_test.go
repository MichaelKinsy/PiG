package workers

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// The fixture processes of the workers tests (test/next-node/fixtures/workers/*.ts). A worker is an executable: the tests
// start this test binary again with fixtureEnv naming what it is to be, and TestMain runs that fixture instead of the tests.

const fixtureEnv = "CHORD_WORKERS_FIXTURE"

func TestMain(m *testing.M) {
	switch os.Getenv(fixtureEnv) {
	case "":
		os.Exit(m.Run())
	case "child":
		runChildFixture()
	case "worker":
		runWorkerFixture()
	case "lastwords":
		runLastWordsFixture()
	case "grandchild":
		time.Sleep(60 * time.Second)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown fixture %q\n", os.Getenv(fixtureEnv))
		os.Exit(2)
	}
}

// fixtureEntry is the executable of every fixture worker: this test binary.
func fixtureEntry(t testing.TB) string {
	t.Helper()
	entry, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

// runChildFixture is child.ts: writes to stdout and stderr, spawns a grandchild, sends a link frame and a control message
// at once, then lives until killed.
func runChildFixture() {
	grandchild := exec.Command(os.Args[0])
	grandchild.Env = append(os.Environ(), fixtureEnv+"=grandchild")
	if err := grandchild.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	_, _ = fmt.Fprintf(os.Stdout, "grandchild %s\n", strconv.Itoa(grandchild.Process.Pid))
	fmt.Fprint(os.Stderr, "an error line\n")
	port := openProcessChannel()
	port.start()
	_, _ = port.Send([]byte{1, 2, 3}, nil)
	payload, _ := envelope(FromWorker{Kind: FromWorkerStarted})
	port.SendControl(payload)
	select {}
}

// What the fixture of a worker that fails right after it started tells: a line of output, then its reason twice (the answer
// to its holder, over the link, then `fatal`).
const (
	lastWordsOutput = "last words\n"
	lastWordsAnswer = "the answer to the holder: Unknown model"
	lastWordsReason = "Unknown model"
)

// lastWordsGrandchild, when set, has the fixture start a process outside its tree, `before` or `after` it opened its channel.
const lastWordsGrandchild = "CHORD_WORKERS_FIXTURE_LASTWORDS_GRANDCHILD"

// startOutsideTree starts a process the worker's tree does not hold (where a platform has the means: the Unix tests set it).
var startOutsideTree func()

// runLastWordsFixture is a worker that fails right after it started: it reports `started`, answers its holder over the link,
// reports `fatal` with its reason, and exits 1 once all of it is written.
func runLastWordsFixture() {
	_, _ = fmt.Fprint(os.Stdout, lastWordsOutput)
	grandchild := os.Getenv(lastWordsGrandchild)
	if grandchild == "before" {
		startOutsideTree()
	}
	port := openProcessChannel()
	if grandchild == "after" {
		startOutsideTree()
	}
	port.start()
	started, _ := envelope(FromWorker{Kind: FromWorkerStarted})
	port.SendControl(started)
	_, _ = port.Send([]byte(lastWordsAnswer), nil)
	fatal, _ := envelope(FromWorker{Kind: FromWorkerFatal, Error: &CrossedError{Message: lastWordsReason}})
	port.sendControlThen(fatal, func() { os.Exit(1) })
	select {}
}

// fixtureEnvironment is the environment of a fixture worker of kind. A binary built with -race sleeps a second before it
// exits (TSan's atexit_sleep_ms), which Node does not: the workers' tests are about when a worker stops, so a fixture
// worker exits at once.
func fixtureEnvironment(kind string) map[string]string {
	race := "atexit_sleep_ms=0"
	if existing := os.Getenv("GORACE"); existing != "" {
		race = existing + " " + race
	}
	return map[string]string{fixtureEnv: kind, "GORACE": race}
}
