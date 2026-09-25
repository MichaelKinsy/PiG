package experimental

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// process.ts validates before deleting, including an explicitly empty role.
func TestInternalProcessRole(t *testing.T) {
	for _, role := range []string{"coordinator", "server", "session-worker", "", "other"} {
		t.Run(role, func(t *testing.T) {
			t.Setenv(InternalProcessEnv, role)
			got, err := GetInternalProcessRole()
			valid := role == "coordinator" || role == "server" || role == "session-worker"
			if valid && (err != nil || string(got) != role) {
				t.Fatalf("get: %q, %v", got, err)
			}
			if !valid && (err == nil || err.Error() != "Unsupported internal process role: "+role) {
				t.Fatalf("invalid: %q, %v", got, err)
			}
			_, err = ConsumeInternalProcessRole()
			_, present := os.LookupEnv(InternalProcessEnv)
			if valid && (err != nil || present) {
				t.Fatalf("role not consumed: %v", err)
			}
			if !valid && (err == nil || !present) {
				t.Fatal("invalid role must remain present")
			}
		})
	}
	t.Setenv(InternalProcessEnv, "coordinator")
	if err := os.Unsetenv(InternalProcessEnv); err != nil {
		t.Fatal(err)
	}
	if role, err := ConsumeInternalProcessRole(); role != "" || err != nil {
		t.Fatalf("absent: %q %v", role, err)
	}
}

func TestEncodeControlLine(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  string
	}{
		{nil, "null\n"},
		{map[string]any{"text": "<>&é😀\u2028\u2029"}, "{\"text\":\"<>&é😀\u2028\u2029\"}\n"},
		{json.RawMessage(`{"z":1,"a":null}`), "{\"z\":1,\"a\":null}\n"},
		{`\u2028\u2029`, "\"\\\\u2028\\\\u2029\"\n"},
	} {
		got, err := EncodeControlLine(tc.value)
		if err != nil || got != tc.want {
			t.Fatalf("got %q, %v; want %q", got, err, tc.want)
		}
	}
	if _, err := EncodeControlLine(make(chan int)); err == nil {
		t.Fatal("unsupported JSON value accepted")
	}
	// JSON string quotes and its newline count toward the UTF-8 byte limit.
	atLimit := strings.Repeat("x", MaxControlLineBytes-3)
	if line, err := EncodeControlLine(atLimit); err != nil || len(line) != MaxControlLineBytes {
		t.Fatalf("boundary: bytes=%d err=%v", len(line), err)
	}
	if _, err := EncodeControlLine(atLimit + "é"); err == nil || err.Error() != "Internal control message is too large" {
		t.Fatalf("oversize: %v", err)
	}
}

func TestSpawnInternalProcess(t *testing.T) {
	t.Setenv("PIG_PROCESS_TEST_INHERITED", "inherited")
	t.Setenv(InternalProcessEnv, "server")
	out := filepath.Join(t.TempDir(), "child.json")
	child, err := SpawnInternalProcess("session-worker", []string{"-test.run=^TestInternalProcessChild$", "--", out}, InternalProcessSpawnOptions{Env: map[string]string{
		"PIG_PROCESS_TEST_CHILD": "1", "PIG_PROCESS_TEST_OVERRIDE": "override", InternalProcessEnv: "invalid",
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := TerminateInternalProcess(child); err != nil {
			t.Error(err)
		}
	})
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Role, Inherited, Override, Cwd string
		PID                            int
		Consumed                       bool
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != "session-worker" || !got.Consumed || got.Inherited != "inherited" || got.Override != "override" || got.Cwd != cwd || got.PID != child.PID() || got.PID == os.Getpid() {
		t.Fatalf("child: %+v", got)
	}
	if os.Getenv(InternalProcessEnv) != "server" {
		t.Fatal("spawn mutated parent role")
	}
	if err := TerminateInternalProcess(child); err != nil {
		t.Fatal(err)
	}
}

func TestTerminateInternalProcess(t *testing.T) {
	child, err := SpawnInternalProcess("coordinator", []string{"-test.run=^TestInternalProcessChild$"}, InternalProcessSpawnOptions{Env: map[string]string{"PIG_PROCESS_TEST_CHILD": "wait"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := TerminateInternalProcess(child); err != nil {
			t.Error(err)
		}
	})
	if err := TerminateInternalProcess(child); err != nil {
		t.Fatal(err)
	}
	select {
	case <-child.Done():
	default:
		t.Fatal("terminate returned before exit")
	}
	if child.ProcessState() == nil || child.ProcessState().Success() {
		t.Fatal("child was not killed")
	}
	if err := TerminateInternalProcess(nil); err != nil {
		t.Fatal(err)
	}
}

func TestSpawnInternalProcessStartFailure(t *testing.T) {
	_, err := SpawnInternalProcess("server", nil, InternalProcessSpawnOptions{EntryPath: filepath.Join(t.TempDir(), "absent")})
	if err == nil {
		t.Fatal("missing executable accepted")
	}
}

func TestInternalProcessChild(t *testing.T) {
	if os.Getenv("PIG_PROCESS_TEST_CHILD") == "" {
		return
	}
	if os.Getenv("PIG_PROCESS_TEST_CHILD") == "wait" {
		select {}
	}
	role, err := ConsumeInternalProcessRole()
	if err != nil {
		t.Fatal(err)
	}
	_, present := os.LookupEnv(InternalProcessEnv)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(struct {
		Role, Inherited, Override, Cwd string
		PID                            int
		Consumed                       bool
	}{string(role), os.Getenv("PIG_PROCESS_TEST_INHERITED"), os.Getenv("PIG_PROCESS_TEST_OVERRIDE"), cwd, os.Getpid(), !present})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Args[len(os.Args)-1], data, 0o600); err != nil {
		t.Fatal(err)
	}
}
