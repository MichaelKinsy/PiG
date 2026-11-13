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

// fixtureReportExecutable, when set, has a worker fixture write the path of the executable it runs from to its stdout.
const fixtureReportExecutable = "CHORD_WORKERS_FIXTURE_REPORT_EXECUTABLE"

// fixtureReportArgs, when set, has a worker fixture write the arguments it was started with to its stdout.
const fixtureReportArgs = "CHORD_WORKERS_FIXTURE_REPORT_ARGS"

// fixtureReportCompileCache, when set, has a worker fixture write its NODE_COMPILE_CACHE to its stdout.
const fixtureReportCompileCache = "CHORD_WORKERS_FIXTURE_REPORT_COMPILE_CACHE"

func TestMain(m *testing.M) {
	switch os.Getenv(fixtureEnv) {
	case "":
		os.Exit(m.Run())
	case "child":
		runChildFixture()
	case "worker":
		runWorkerFixture()
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
