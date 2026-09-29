package experimental

import (
	"context"
	"os"
	"reflect"
	"testing"
)

// D64: the owner designed out experimental Radius on 2026-09-28. These are rejection guards, not ports of Pi's positive Radius/auth cases.
func TestExperimentalRadiusSelectionIsClosed(t *testing.T) {
	t.Parallel()
	for _, address := range []string{
		"radius://00000000-0000-4000-8000-000000000001",
		"radius://00000000-0000-4000-8000-000000000001/",
		"radius://not-a-server",
	} {
		t.Run(address, func(t *testing.T) {
			calls := 0
			result, err := Cli.Execute(t.Context(), []string{"client", "--connect", address}, CliContext{
				RunClient: func(context.Context, ClientCommand) error { calls++; return nil },
				RunServer: func(context.Context, ServerCommand) error { calls++; return nil },
			})
			want := CommandParseResult{Errors: []string{`Unsupported --connect transport "radius:"`}}
			if err != nil || !reflect.DeepEqual(result, want) || calls != 0 {
				t.Fatalf("execute = %#v, %v; actions = %d; want %#v, no action", result, err, calls, want)
			}
		})
	}
	for _, name := range []string{"server", "client"} {
		for _, option := range []string{"--auth-token", "--auth-token-file"} {
			t.Run(name+option, func(t *testing.T) {
				calls := 0
				result, err := Cli.Execute(t.Context(), []string{name, option, "not-read"}, CliContext{
					RunClient: func(context.Context, ClientCommand) error { calls++; return nil },
					RunServer: func(context.Context, ServerCommand) error { calls++; return nil },
				})
				want := CommandParseResult{Errors: []string{"The experimental " + name + " command does not support existing CLI options yet"}}
				if err != nil || !reflect.DeepEqual(result, want) || calls != 0 {
					t.Fatalf("execute = %#v, %v; actions = %d; want %#v, no action", result, err, calls, want)
				}
			})
		}
	}
}

func TestExperimentalClientRejectsNonUnixBeforeDiscovery(t *testing.T) {
	t.Parallel()
	for _, transport := range []string{"radius", "ws", ""} {
		t.Run(transport, func(t *testing.T) {
			directory := t.TempDir() + "/uncreated"
			runtime, err := OpenClientRuntime(t.Context(), ClientCommand{
				Connect:  &TransportAddress{Transport: transport, Path: "not-read"},
				Provider: new("not-resolved"),
			}, OpenClientRuntimeOptions{Directory: &directory})
			if runtime != nil || err == nil || err.Error() != "Experimental clients support only Unix transport" {
				t.Fatalf("open = %v, %v; want transport rejection", runtime, err)
			}
			if _, statErr := os.Stat(directory); !os.IsNotExist(statErr) {
				t.Fatalf("unsupported transport touched discovery: %v", statErr)
			}
		})
	}
}
