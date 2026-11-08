package subprocess

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// The first generation of a Node factory launcher carries its Host's group when the Host shares cells, as every later admission does, because the registry offers that process to other Hosts: without it the spawner's own instance runs group-less and counts another session's running tools as siblings (pig additive (D232)).
func TestFactoryLauncherEnvCarriesTheHostGroupOnlyWhenSharing(t *testing.T) {
	cwd := t.TempDir()
	if env := NewHost(cwd).factoryLauncherEnv(); !slices.Equal(env, []string{"PIG_EXT_CWD=" + cwd}) {
		t.Fatalf("a Host that shares no cells passes %v, want only its cwd", env)
	}
	h := NewHost(cwd)
	h.ShareCells(NewCellRegistry())
	if group := h.Group(); group == "" {
		t.Fatal("a Host that joined a registry has no group")
	} else if env := h.factoryLauncherEnv(); !slices.Equal(env, []string{"PIG_EXT_CWD=" + cwd, "PIG_EXT_GROUP=" + group}) {
		t.Fatalf("a Host in a registry passes %v, want its cwd and group %q", env, group)
	}
}

// cli.mjs puts PIG_EXT_GROUP into the first admission and removes it from the environment (the same pattern as PIG_EXT_CWD); without it the group is "".
func TestNodeLauncherAdmitsItsFirstGenerationInTheHostGroup(t *testing.T) {
	nodeCellRequireNode(t)
	dir, err := filepath.Abs("runtime-node")
	if err != nil {
		t.Fatal(err)
	}
	probe := `import { pathToFileURL } from "node:url";
const dir = process.env.PROBE_DIR;
const { Generations } = await import(pathToFileURL(dir + "/generations.mjs").href);
Generations.prototype.admit = async function (admission) {
  console.log(JSON.stringify({ group: admission.group, name: admission.name, env: process.env.PIG_EXT_GROUP ?? null }));
  return false;
};
process.argv[2] = "/ext/probe.mjs";
await import(pathToFileURL(dir + "/cli.mjs").href);
`
	run := func(env ...string) (got struct {
		Group *string `json:"group"`
		Name  string  `json:"name"`
		Env   *string `json:"env"`
	}) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", probe)
		cmd.Env = append(os.Environ(), append([]string{"PROBE_DIR=" + dir, "PIG_EXT_NAME=probe", "PIG_EXT_SOCKET=/nonexistent.sock", "PIG_EXT_GROUP="}, env...)...)
		out, _ := cmd.Output() // the probe's admit fails, so cli.mjs exits 1 after printing
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("probe output %q: %v", out, err)
		}
		return got
	}
	if got := run("PIG_EXT_GROUP=g7"); got.Group == nil || *got.Group != "g7" || got.Env != nil {
		t.Fatalf("with PIG_EXT_GROUP=g7: group %v, env after %v; want the group g7 in the admission and the variable removed", deref(got.Group), deref(got.Env))
	}
	if got := run(); got.Group == nil || *got.Group != "" {
		t.Fatalf("without PIG_EXT_GROUP: group %v, want the empty group", deref(got.Group))
	}
}

func deref(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}
