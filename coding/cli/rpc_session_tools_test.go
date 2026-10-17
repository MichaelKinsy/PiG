package cli

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

// Pi's RPC mode never sets the active tools (rpc-mode.ts): the Session starts with the tools its options select (sdk.ts:258-276 initialActiveToolNames minus excluded tools; agent-session.ts _refreshToolRegistry), and its system prompt lists them (_rebuildSystemPrompt). Covered: the default tools, a --tools order, repeated --tools names, --no-tools, an allowlist that admits only listed extension tools, an allowlist minus excluded tools, and excluded builtin and extension tools.
func TestRPCSessionStartsWithThePromptsTools(t *testing.T) {
	extension, err := filepath.Abs(filepath.Join("testdata", "ctx-signal.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{nil, []string{"read", "bash", "edit", "write", "inner", "echo_bridge"}},
		{[]string{"--tools", "write,read,bash"}, []string{"write", "read", "bash"}},
		{[]string{"--tools", "inner,bash"}, []string{"inner", "bash"}},
		{[]string{"--exclude-tools", "read"}, []string{"bash", "edit", "write", "inner", "echo_bridge"}},
		{[]string{"--exclude-tools", "inner"}, []string{"read", "bash", "edit", "write", "echo_bridge"}},
		{[]string{"--tools", "read,echo_bridge"}, []string{"read", "echo_bridge"}},
		{[]string{"--tools", "read,inner,echo_bridge", "--exclude-tools", "echo_bridge"}, []string{"read", "inner"}},
		// sdk.ts:279-295: --no-tools is an empty allowlist, which admits no builtin and no extension tool.
		{[]string{"--no-tools"}, nil},
		// agent-session.ts _refreshToolRegistry keeps the first occurrence of a repeated --tools name (a Set).
		{[]string{"--tools", "inner,read,inner,bash,read"}, []string{"inner", "read", "bash"}},
	} {
		builder, cwd, agentDir := benchReplacementBuilder(t, 0)
		builder.flags = parseArgs(append([]string{"--model", "test-faux/faux-1", "--mode", "rpc", "-e", extension}, tc.args...))
		ctx := context.Background()
		build, err := builder.buildResources(ctx, cliBuildInput{CWD: cwd})
		if err != nil {
			t.Fatal(err)
		}
		manager, err := coding.NewInMemorySessionManager(cwd)
		if err != nil {
			t.Fatal(err)
		}
		if err := builder.buildSession(ctx, build, cliBuildInput{CWD: cwd, Manager: manager}); err != nil {
			t.Fatal(err)
		}
		inputs := builder.rpcInputs(ctx, build, &rpcStartup{Manager: manager})
		factory := newCLISessionFactory(ctx, inputs, struct{}{}, nil)
		rt, err := coding.CreateAgentSessionRuntime(ctx, factory.Factory(), coding.CreateAgentSessionRuntimeOptions{CWD: cwd, AgentDir: agentDir, SessionManager: manager})
		if err != nil {
			t.Fatal(err)
		}
		active := rt.Session().ActiveToolNames()
		prompt := rt.Session().SystemPrompt()
		_ = rt.Close()
		factory.Close()
		if !slices.Equal(active, tc.want) {
			t.Errorf("%v: active tools %v, want %v", tc.args, active, tc.want)
		}
		for _, name := range []string{"read", "bash", "edit", "write"} {
			if listed := strings.Contains(prompt, "- "+name+":"); listed != slices.Contains(tc.want, name) {
				t.Errorf("%v: system prompt lists %s = %v", tc.args, name, listed)
			}
		}
	}
}
