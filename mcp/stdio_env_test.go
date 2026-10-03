package mcp_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Pi's StdioTransport spawns the server with the env {...options.env} when
// inheritEnv is false (packages/mcp/src/transports/stdio.ts:94). Node's spawn
// writes each property into the block as "name=value", so "PIG_A" and
// "PIG_A=B" both reach the server, although their entries share the text
// before the first "=". Node's spawnSync of the same fixture with the same env
// object, whose keys PiG applies in sorted order as JSON gives them, is the
// oracle; the fixture reports its environment in block order.
func TestStdioTransportPassesEveryPropertyWhoseNameHoldsAnEqualsSign(t *testing.T) {
	command, args, _ := fixtureStdioOptions("environ")
	env := map[string]string{fixtureEnv: "environ", "GORACE": "atexit_sleep_ms=0", "PIG_A": "one", "PIG_A=B": "two"}
	off := false
	transport := mcp.NewStdioTransport(mcp.StdioTransportOptions{Command: command, Args: args, Env: env, InheritEnv: &off})
	closed := make(chan struct{})
	transport.OnClose(func() { close(closed) })
	if err := transport.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(30 * time.Second):
		t.Fatal("the fixture did not exit")
	}
	var got []string
	if err := json.Unmarshal([]byte(transport.Stderr()), &got); err != nil {
		t.Fatalf("decode %q: %v", transport.Stderr(), err)
	}
	input, err := json.Marshal(map[string]any{"file": command, "args": args, "env": env})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "-e", `
const { spawnSync } = require("node:child_process");
const { file, args, env } = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
const result = spawnSync(file, args, { env, encoding: "utf8" });
if (result.error || result.status !== 0) throw result.error ?? new Error(result.stderr);
process.stdout.write(result.stderr);
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("node spawn: %v; output %s", err, out)
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if !slices.Contains(want, "PIG_A=one") {
		t.Fatalf("Node's child environment %q lacks PIG_A=one", want)
	}
	if !slices.Equal(got, want) {
		t.Errorf("server environment\n got %q\nwant %q", got, want)
	}
}
