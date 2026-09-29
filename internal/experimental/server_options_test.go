package experimental

import (
	"os"
	"path/filepath"
	"testing"
)

// upstream: packages/coding-agent/src/experimental/server.ts:51-56. D2 changes the selected namespace, not nullish precedence or the sibling server/agent layout.
func TestResolveServerDirectorySelectedNamespace(t *testing.T) {
	isolateExperimentalTest(t)
	home := os.Getenv("HOME")
	pigRoot := os.Getenv("PIG_HOME")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		name, shared      string
		pig, pi, explicit *string
		want              string
	}{
		{name: "Pig default", shared: "0", want: filepath.Join(pigRoot, "server")},
		{name: "Pi default", shared: "1", want: filepath.Join(home, ".pi", "server")},
		{name: "Pig ignores Pi override", shared: "0", pi: new("pi-override"), want: filepath.Join(pigRoot, "server")},
		{name: "Pi ignores Pig override", shared: "1", pig: new("pig-override"), want: filepath.Join(home, ".pi", "server")},
		{name: "Pig override", shared: "0", pig: new("pig-override"), pi: new("pi-override"), want: filepath.Join(cwd, "pig-override")},
		{name: "Pi override", shared: "1", pig: new("pig-override"), pi: new("pi-override"), want: filepath.Join(cwd, "pi-override")},
		{name: "empty Pig override", shared: "0", pig: new(""), want: cwd},
		{name: "empty Pi override", shared: "1", pi: new(""), want: cwd},
		{name: "explicit relative", shared: "1", pi: new("ignored"), explicit: new("chosen"), want: filepath.Join(cwd, "chosen")},
		{name: "explicit empty", shared: "0", pig: new("ignored"), explicit: new(""), want: cwd},
		{name: "explicit tilde", shared: "0", explicit: new("~/chosen"), want: filepath.Join(home, "chosen")},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Setenv("PIG_USE_PI_DIRS", row.shared)
			for name, value := range map[string]*string{EnvServerDir: row.pig, "PI_SERVER_DIR": row.pi} {
				t.Setenv(name, "")
				if value == nil {
					if err := os.Unsetenv(name); err != nil {
						t.Fatal(err)
					}
				} else {
					t.Setenv(name, *value)
				}
			}
			// Agent-only relocation does not move Pi's separate server directory.
			t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "relocated-pig-agent"))
			t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "relocated-pi-agent"))
			got, err := ResolveServerDirectory(row.explicit)
			if err != nil || got != row.want {
				t.Fatalf("directory=%q error=%v, want %q", got, err, row.want)
			}
		})
	}
}

// upstream: packages/coding-agent/src/experimental/server.ts:530,715. Selection retains supplied empty strings; profile acquisition owns canonical-ID validation.
func TestRequestedServerIdSelectedNamespace(t *testing.T) {
	isolateExperimentalTest(t)
	for _, row := range []struct {
		name, shared      string
		pig, pi, explicit *string
		want              *string
	}{
		{name: "absent", shared: "0"},
		{name: "Pig ignores Pi", shared: "0", pi: new("pi")},
		{name: "Pi ignores Pig", shared: "1", pig: new("pig")},
		{name: "Pig selected", shared: "0", pig: new("pig"), pi: new("pi"), want: new("pig")},
		{name: "Pi selected", shared: "1", pig: new("pig"), pi: new("pi"), want: new("pi")},
		{name: "empty environment retained", shared: "0", pig: new(""), want: new("")},
		{name: "explicit retained", shared: "1", pi: new("pi"), explicit: new("chosen"), want: new("chosen")},
		{name: "explicit empty retained", shared: "0", pig: new("pig"), explicit: new(""), want: new("")},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Setenv("PIG_USE_PI_DIRS", row.shared)
			for name, value := range map[string]*string{EnvServerID: row.pig, "PI_SERVER_ID": row.pi} {
				t.Setenv(name, "")
				if value == nil {
					if err := os.Unsetenv(name); err != nil {
						t.Fatal(err)
					}
				} else {
					t.Setenv(name, *value)
				}
			}
			got := requestedServerId(row.explicit)
			if (got == nil) != (row.want == nil) || got != nil && *got != *row.want {
				t.Fatalf("server ID=%v, want %v", got, row.want)
			}
		})
	}
}
