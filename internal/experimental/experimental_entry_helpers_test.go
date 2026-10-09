package experimental

// pi: packages/coding-agent/src/experimental/coordinator-entry.ts

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
)

const experimentalTestEntryEnv = "PIG_TEST_EXPERIMENTAL_ENTRY"

// Upstream remote-runtime tests spawn the real internal source entry. The native fixture re-executes this test binary and must dispatch those roles instead of running m.Run again. An explicit server role also supports cold activation from older coordinator fixtures.
func runExperimentalTestEntry(ctx context.Context, args []string) (bool, error) {
	if os.Getenv(experimentalTestEntryEnv) != "1" && os.Getenv(InternalProcessEnv) != "server" {
		return false, nil
	}
	role, err := GetInternalProcessRole()
	if err != nil {
		return true, err
	}
	if role == "" {
		return false, nil
	}
	if role == "coordinator" {
		// Preserve the special coordinator fixtures, but never let their inherited flags select a different role's entry.
		if fixture := os.Getenv("PIG_TEST_COORDINATOR"); fixture == "1" || fixture == "paused" {
			return false, nil
		}
		return true, RunCoordinatorEntry(ctx, args)
	}
	if _, err := ConsumeInternalProcessRole(); err != nil {
		return true, err
	}
	if role == "server" {
		return true, RunServerProcess(ctx, args)
	}
	if os.Getenv(experimentalFauxWorkerEnv) == "1" {
		return true, runFauxSessionWorker(ctx, args)
	}
	return true, RunSessionWorkerProcess(ctx, args)
}

// This drives TestMain in a real child. Without its fixture dispatch, the Go test flag selects an empty suite and incorrectly exits successfully.
func TestExperimentalFixtureChildDoesNotReenterSuite(t *testing.T) {
	isolateExperimentalTest(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ name, entry, coordinator string }{
		{"selected remote entry", "1", ""},
		{"server role before inherited coordinator", "", "1"},
		{"server role before inherited paused coordinator", "", "paused"},
	} {
		t.Run(row.name, func(t *testing.T) {
			child := exec.CommandContext(t.Context(), executable, "-test.run=^$")
			child.Env = append(os.Environ(), experimentalTestEntryEnv+"="+row.entry, "PIG_TEST_COORDINATOR="+row.coordinator, "PIG_TEST_CHILD_READY=", InternalProcessEnv+"=server")
			output, err := child.CombinedOutput()
			exit, ok := errors.AsType[*exec.ExitError](err)
			if !ok || exit.ExitCode() != 1 || string(output) != "Internal server requires an absolute server directory\n" {
				t.Fatalf("child output = %q, error = %v; want real server validation and exit 1", output, err)
			}
		})
	}
}

// upstream: packages/coding-agent/src/experimental/cli.ts:1-12 and experimental/process.ts:consumeInternalProcessRole. Entry validation consumes a valid role once before the corresponding real process function runs.
func TestExperimentalFixtureDispatchesActualInternalRoles(t *testing.T) {
	for _, row := range []struct{ role, want string }{
		{"coordinator", "Coordinator requires public and control socket paths"},
		{"server", "Internal server requires an absolute server directory"},
		{"session-worker", "Session worker requires one options argument"},
	} {
		t.Run(row.role, func(t *testing.T) {
			t.Setenv(experimentalTestEntryEnv, "1")
			t.Setenv(InternalProcessEnv, row.role)
			handled, err := runExperimentalTestEntry(t.Context(), nil)
			if !handled || err == nil || err.Error() != row.want {
				t.Fatalf("entry = %v, %v; want handled with %q", handled, err, row.want)
			}
			if _, exists := os.LookupEnv(InternalProcessEnv); exists {
				t.Fatal("consumed role remains available to descendants")
			}
		})
	}
}

func TestExperimentalFixtureEntryPreservesCoordinatorFixtures(t *testing.T) {
	t.Setenv(experimentalTestEntryEnv, "1")
	t.Setenv(InternalProcessEnv, "coordinator")
	for _, fixture := range []string{"1", "paused"} {
		t.Setenv("PIG_TEST_COORDINATOR", fixture)
		if handled, err := runExperimentalTestEntry(t.Context(), nil); handled || err != nil {
			t.Fatalf("coordinator fixture %q was intercepted: %v, %v", fixture, handled, err)
		}
		if os.Getenv(InternalProcessEnv) != "coordinator" {
			t.Fatal("coordinator fixture role was consumed before its own entry")
		}
	}
}

func TestExperimentalFixtureEntryGatesNonServerRolesAndPreservesInvalidRole(t *testing.T) {
	t.Setenv(InternalProcessEnv, "invalid")
	for _, marker := range []string{"", "true", "0"} {
		t.Setenv(experimentalTestEntryEnv, marker)
		if handled, err := runExperimentalTestEntry(t.Context(), nil); handled || err != nil {
			t.Fatalf("disabled entry = %v, %v", handled, err)
		}
		if os.Getenv(InternalProcessEnv) != "invalid" {
			t.Fatal("disabled entry consumed the role")
		}
	}
	t.Setenv(experimentalTestEntryEnv, "1")
	if handled, err := runExperimentalTestEntry(t.Context(), nil); !handled || err == nil || err.Error() != "Unsupported internal process role: invalid" {
		t.Fatalf("invalid role entry = %v, %v", handled, err)
	}
	if os.Getenv(InternalProcessEnv) != "invalid" {
		t.Fatal("invalid role was consumed")
	}
	if err := os.Unsetenv(InternalProcessEnv); err != nil {
		t.Fatal(err)
	}
	if handled, err := runExperimentalTestEntry(t.Context(), nil); handled || err != nil {
		t.Fatalf("role-free entry = %v, %v", handled, err)
	}
}
