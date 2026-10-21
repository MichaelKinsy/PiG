package testfixture

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// statePushWindow is how long a state-push-probe reader waits for the value it armed on to change before it reports the value it still holds.
const statePushWindow = 5 * time.Second

// registerStatePushProbe registers the state-push-probe command (TestHostStateChangeReachesABackgroundReaderAcrossSDKs). The command arms a goroutine that reads one replicated accessor from outside every request, as an extension's timer does, and returns: the host then changes its own state with no call and no event to this extension, and the goroutine reports the first value that differs from the one it armed on, or the value it still holds after statePushWindow, to a file in the directory the arguments name.
// The arguments are {"kind": "model" | "session-name" | "theme" | "settings:<key>" | "mcp", "dir": <directory>}. The report is "<armed>=><read>". Every SDK fixture reads the same state with its own accessor.
func registerStatePushProbe(ext *sdk.Extension) {
	ext.Command("state-push-probe", "Read one replicated accessor from a goroutine between requests", func(ctx sdk.Context, args string) error {
		var request struct {
			Kind string `json:"kind"`
			Dir  string `json:"dir"`
		}
		if err := json.Unmarshal([]byte(args), &request); err != nil {
			return err
		}
		read, err := statePushReader(ctx, request.Kind)
		if err != nil {
			return err
		}
		armed := read()
		go func() {
			current := armed
			for deadline := time.Now().Add(statePushWindow); current == armed && time.Now().Before(deadline); {
				time.Sleep(2 * time.Millisecond)
				current = read()
			}
			report := filepath.Join(request.Dir, "state-push-"+strings.ReplaceAll(request.Kind, ":", "_"))
			if err := os.WriteFile(report+".tmp", []byte(armed+"=>"+current), 0o600); err == nil {
				_ = os.Rename(report+".tmp", report)
			}
		}()
		return nil
	})
}

// statePushReader returns the read of one replicated accessor as a string, or an error for a kind the Go SDK does not replicate: its thinking level is a host call, which is live.
func statePushReader(ctx sdk.Context, kind string) (func() string, error) {
	switch {
	case kind == "model":
		return ctx.Model, nil
	case kind == "session-name":
		return ctx.SessionName, nil
	case kind == "theme":
		return func() string { return ctx.UITheme().Name }, nil
	case strings.HasPrefix(kind, "settings:"):
		key := strings.TrimPrefix(kind, "settings:")
		return func() string {
			settings, err := ctx.GetSettings()
			if err != nil {
				return "error:" + err.Error()
			}
			encoded, err := json.Marshal(settings[key])
			if err != nil {
				return "error:" + err.Error()
			}
			return string(encoded)
		}, nil
	case kind == "mcp":
		return func() string {
			servers, err := ctx.GetMcpServers()
			if err != nil {
				return "error:" + err.Error()
			}
			names := make([]string, len(servers))
			for i, server := range servers {
				names[i] = server.Name
			}
			return strings.Join(names, ",")
		}, nil
	}
	return nil, fmt.Errorf("state-push-probe: unsupported kind %q", kind)
}
