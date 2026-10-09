package experimental

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestExperimentalCLICommands(t *testing.T) {
	t.Parallel()
	const unsupportedServerOptions = "The experimental server command does not support existing CLI options yet"
	const unsupportedClientOptions = "The experimental client command does not support existing CLI options yet"

	// upstream: packages/coding-agent/test/experimental-cli-command.test.ts:8
	t.Run("parses server configuration", func(t *testing.T) {
		got, err := Cli.Parse([]string{"server", "--server-id", "00000000-0000-4000-8000-000000000001", "--session-dir", "~/pi-sessions", "--provider", "anthropic", "--model", "claude-sonnet-4-5", "-e", "./first-plugin", "-e=./second-plugin"})
		want := CommandParseResult{Ok: true, Command: ServerCommand{
			Command: "server", ServerId: new("00000000-0000-4000-8000-000000000001"), SessionDir: new("~/pi-sessions"),
			Provider: new("anthropic"), Model: new("claude-sonnet-4-5"), PluginPackages: []string{"./first-plugin", "./second-plugin"},
		}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("parse = %#v, %v; want %#v", got, err, want)
		}
	})

	// upstream: packages/coding-agent/test/experimental-cli-command.test.ts:37
	t.Run("parses provider-qualified server models", func(t *testing.T) {
		got, err := Cli.Parse([]string{"server", "--model", "anthropic/claude-sonnet-4-5:high"})
		want := CommandParseResult{Ok: true, Command: ServerCommand{Command: "server", Model: new("anthropic/claude-sonnet-4-5:high")}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("parse = %#v, %v; want %#v", got, err, want)
		}
	})

	// D64: the second, Radius-only assertion at :49-58 is designed out by the owner on 2026-09-28; the Unix assertion remains unchanged.
	// upstream: packages/coding-agent/test/experimental-cli-command.test.ts:44
	t.Run("parses client transport addresses", func(t *testing.T) {
		for _, test := range []struct {
			args []string
			want ClientCommand
		}{
			{[]string{"client", "--connect", "unix:///tmp/pi.sock"}, ClientCommand{Command: "client", Connect: &TransportAddress{Transport: "unix", Path: "/tmp/pi.sock"}}},
		} {
			got, err := Cli.Parse(test.args)
			want := CommandParseResult{Ok: true, Command: test.want}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("parse(%q) = %#v, %v; want %#v", test.args, got, err, want)
			}
		}
	})

	// upstream: packages/coding-agent/test/experimental-cli-command.test.ts:61 (rows :62-65)
	for _, test := range []struct {
		option   string
		property string
	}{
		{"-c", "continue"}, {"--continue", "continue"}, {"-r", "resume"}, {"--resume", "resume"},
	} {
		t.Run("parses client Session selection "+test.option, func(t *testing.T) {
			command := ClientCommand{Command: "client"}
			if test.property == "continue" {
				command.Continue = new(true)
			} else {
				command.Resume = new(true)
			}
			got, err := Cli.Parse([]string{"client", test.option})
			want := CommandParseResult{Ok: true, Command: command}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("parse = %#v, %v; want %#v", got, err, want)
			}
		})
	}

	// upstream: packages/coding-agent/test/experimental-cli-command.test.ts:73
	t.Run("parses a Session selection followed by a one-shot prompt", func(t *testing.T) {
		got, err := Cli.Parse([]string{"client", "-r", "Explain this project"})
		want := CommandParseResult{Ok: true, Command: ClientCommand{Command: "client", Resume: new(true), Prompt: new("Explain this project")}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("parse = %#v, %v; want %#v", got, err, want)
		}
	})

	// upstream: packages/coding-agent/test/experimental-cli-command.test.ts:80
	t.Run("parses client model and plugin configuration", func(t *testing.T) {
		got, err := Cli.Parse([]string{"client", "--model", "anthropic/claude-sonnet-4-5:high", "-e", "./first-plugin", "-e", "./second-plugin"})
		want := CommandParseResult{Ok: true, Command: ClientCommand{Command: "client", Model: new("anthropic/claude-sonnet-4-5:high"), PluginPackages: []string{"./first-plugin", "./second-plugin"}}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("parse = %#v, %v; want %#v", got, err, want)
		}
	})

	// D64: explicit relay authentication cases at :101-108 are designed out by the owner on 2026-09-28.

	// upstream: packages/coding-agent/test/experimental-cli-command.test.ts:110
	for _, name := range []string{"server", "client"} {
		t.Run("permits omitted authentication for "+name, func(t *testing.T) {
			got, err := Cli.Parse([]string{name})
			var want NamedCommandInvocation = ClientCommand{Command: name}
			if name == "server" {
				want = ServerCommand{Command: name}
			}
			if err != nil || !reflect.DeepEqual(got, CommandParseResult{Ok: true, Command: want}) {
				t.Fatalf("parse = %#v, %v; want command %#v", got, err, want)
			}
		})
	}

	// upstream: packages/coding-agent/test/experimental-cli-command.test.ts:114 (rows :115-134 except Radius row :119, designed out under D64 on 2026-09-28)
	for _, test := range []struct {
		argv  []string
		error string
	}{
		{[]string{"client", "--listen", "unix:///tmp/pi.sock"}, unsupportedClientOptions},
		{[]string{"server", "--listen", "unix:///tmp/pi.sock"}, unsupportedServerOptions},
		{[]string{"server", "--connect", "unix:///tmp/pi.sock"}, unsupportedServerOptions},
		{[]string{"client", "--connect", "ws://localhost:8080"}, `Unsupported --connect transport "ws:"`},
		{[]string{"client", "--connect", "unix://relative.sock"}, "Unix transport address must not include an authority"},
		{[]string{"client", "--connect", "unix:///tmp/pi.sock?wrong=value"}, "Invalid --connect address"},
		{[]string{"client", "--provider", "anthropic"}, "--provider requires --model"},
		{[]string{"client", "-c", "-r"}, "--session-id, --continue, and --resume are mutually exclusive"},
		{[]string{"client", "--continue=true"}, "--continue does not take a value"},
		{[]string{"server", "--provider", "anthropic"}, "--provider requires --model"},
		{[]string{"server", "--server-id", "not-a-uuid"}, "Invalid --server-id"},
		{[]string{"server", "--server-id"}, "--server-id requires a value"},
		{[]string{"server", "--session-dir"}, "--session-dir requires a value"},
		{[]string{"client", "-e"}, "-e requires a value"},
		{[]string{"server", "--session-dir", "/tmp/first", "--session-dir=/tmp/second"}, "--session-dir may only be specified once"},
		{[]string{"client", "--connect="}, "--connect requires a value"},
	} {
		t.Run("rejects invalid experimental input "+strings.Join(test.argv, " "), func(t *testing.T) {
			got, err := Cli.Parse(test.argv)
			if err != nil || got.Ok || !slices.ContainsFunc(got.Errors, func(message string) bool { return strings.Contains(message, test.error) }) {
				t.Fatalf("parse = %#v, %v; want failed result with diagnostic containing %q", got, err, test.error)
			}
		})
	}

	// upstream: packages/coding-agent/test/experimental-cli-command.test.ts:141
	t.Run("rejects unsupported options without parsing them through the stable CLI", func(t *testing.T) {
		got, err := Cli.Parse([]string{"client", "--tui-mode", "wrong", "--model", "claude-sonnet"})
		want := CommandParseResult{Errors: []string{unsupportedClientOptions}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("parse = %#v, %v; want %#v", got, err, want)
		}
	})
}

// Source-derived boundaries supplement the upstream suite without changing its rows.
// upstream: packages/coding-agent/src/cli/experimental/command.ts:166-224
// upstream: packages/coding-agent/src/cli/experimental/commands/client.ts:54-86
func TestExperimentalCLIParserBoundaries(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		args []string
		want CommandParseResult
	}{
		{"separator permits leading dash", []string{"client", "--", "--model"}, CommandParseResult{Ok: true, Command: ClientCommand{Command: "client", Prompt: new("--model")}}},
		{"same selection aliases are independent options", []string{"client", "-c", "--continue"}, CommandParseResult{Ok: true, Command: ClientCommand{Command: "client", Continue: new(true)}}},
		{"inline value may start with dash", []string{"client", "--model=-local"}, CommandParseResult{Ok: true, Command: ClientCommand{Command: "client", Model: new("-local")}}},
		{"option scan stops at first positional", []string{"client", "hello", "--model", "m"}, CommandParseResult{Errors: []string{"The experimental client command does not support existing CLI options yet"}}},
		{"empty prompt rejected", []string{"client", ""}, CommandParseResult{Errors: []string{"The experimental client command does not support existing CLI options yet"}}},
		{"bare separator rejected", []string{"client", "--"}, CommandParseResult{Errors: []string{"The experimental client command does not support existing CLI options yet"}}},
		{"selection conflicts with explicit session", []string{"client", "--session-id", "s", "--continue"}, CommandParseResult{Errors: []string{"--session-id, --continue, and --resume are mutually exclusive"}}},
		{"resume conflicts with explicit session", []string{"client", "--session-id", "s", "--resume"}, CommandParseResult{Errors: []string{"--session-id, --continue, and --resume are mutually exclusive"}}},
		{"duplicate flag", []string{"client", "-r", "-r"}, CommandParseResult{Errors: []string{"-r may only be specified once"}}},
		{"missing values do not consume next option", []string{"client", "--model", "--provider", "p"}, CommandParseResult{Errors: []string{"--model requires a value", "--provider requires --model"}}},
		{"invalid first value does not create duplicate", []string{"server", "--server-id", "invalid", "--server-id", "00000000-0000-4000-8000-000000000001"}, CommandParseResult{Errors: []string{`Invalid --server-id "invalid"; expected a lowercase UUIDv4`}}},
		{"parser errors precede builder errors", []string{"client", "--model=", "--provider", "p", "-c", "-r", "--unknown"}, CommandParseResult{Errors: []string{"--model requires a value", "--provider requires --model", "--session-id, --continue, and --resume are mutually exclusive", "The experimental client command does not support existing CLI options yet"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := Cli.Parse(test.args)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parse(%q) = %#v, %v; want %#v", test.args, got, err, test.want)
			}
		})
	}
}

// upstream: packages/coding-agent/src/cli/experimental/command-options.ts:40-106
func TestExperimentalTransportAddressBoundaries(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		input string
		want  TransportAddress
	}{
		{"unix:///", TransportAddress{Transport: "unix", Path: "/"}},
		{"unix:///tmp/a%20b%2Fsock", TransportAddress{Transport: "unix", Path: "/tmp/a b/sock"}},
		{"unix:///tmp/%E2%98%83", TransportAddress{Transport: "unix", Path: "/tmp/☃"}},
		{`unix:///tmp/back\slash`, TransportAddress{Transport: "unix", Path: `/tmp/back\slash`}},
	} {
		t.Run(test.input, func(t *testing.T) {
			got, err := Cli.Parse([]string{"client", "--connect", test.input})
			want := CommandParseResult{Ok: true, Command: ClientCommand{Command: "client", Connect: &test.want}}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("parse = %#v, %v; want %#v", got, err, want)
			}
		})
	}
	for _, input := range []string{
		"not a URL", "unix:////tmp/sock", "unix:/tmp/sock", "UNIX:///tmp/sock", " unix:///tmp/sock", "unix:///tmp/so\nck",
		"unix:///tmp/sock?", "unix:///tmp/sock#", "unix:///tmp/a b", "unix:///tmp/☃", "unix:///tmp/../sock", "unix:///tmp/%2E/sock", "unix:///tmp/.%2e/sock",
		"unix:///tmp/%00", "unix:///tmp/%FF", "unix:///tmp/%ED%A0%80", "unix:///tmp/%", "unix://host:65536/tmp/sock", "unix://[bad]/tmp/sock",
		"https://", "http://host:65536", "file://user@host/path",
		// Node 24 (.node-version) percent-encodes ^ in a URL path, so new URL("unix:///tmp/a^b").href differs from the input.
		"unix:///tmp/a^b", "unix:///^",
	} {
		t.Run(input, func(t *testing.T) {
			got, err := Cli.Parse([]string{"client", "--connect", input})
			want := CommandParseResult{Errors: []string{`Invalid --connect address "` + input + `"`}}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("parse = %#v, %v; want %#v", got, err, want)
			}
		})
	}
	for _, test := range []struct{ input, diagnostic string }{
		{"unix://host/tmp/sock", "Unix transport address must not include an authority"},
		{"unix://u:p@host/tmp/sock", "Unix transport address must not include an authority"},
		{"unix://[::1]/tmp/sock", "Unix transport address must not include an authority"},
		{"https://example.com", `Unsupported --connect transport "https:"`},
		{"ws://localhost:8080", `Unsupported --connect transport "ws:"`},
		{"mailto:a%ZZ", `Unsupported --connect transport "mailto:"`},
		{"custom:path", `Unsupported --connect transport "custom:"`},
	} {
		t.Run(test.input, func(t *testing.T) {
			got, err := Cli.Parse([]string{"client", "--connect", test.input})
			want := CommandParseResult{Errors: []string{test.diagnostic}}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("parse = %#v, %v; want %#v", got, err, want)
			}
		})
	}
}
