package subagent

import (
	"fmt"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Extension is written for SDK 0.4.1: Context.ConfigHome returns one value.
func Extension() *sdk.Extension {
	ext := sdk.New("subagent")

	ext.Command("save", "Save the configuration", func(ctx sdk.Context, args string) error {
		c := config{}
		if err := c.save(ctx.ConfigHome()); err != nil {
			return err
		}
		if err := validateStepRuntime(ctx.Cwd(), ctx.ConfigHome(), args); err != nil {
			return err
		}
		if err := saveManifest(ctx.ConfigHome(), args); err != nil {
			return err
		}
		record(ctx.Mode(), ctx.ConfigHome())
		fmt.Println(ctx.ConfigHome())
		fmt.Printf("home %s\n", ctx.ConfigHome())
		logf(ctx.ConfigHome())
		return nil
	})

	ext.OnSessionStart(func(ctx sdk.Context, event map[string]any) (any, error) {
		outputDir = filepath.Join(strings.TrimSpace(ctx.ConfigHome()), "subagent-output")
		return nil, nil
	})

	return ext
}

var outputDir string

type config struct{}

func (config) save(home string) error { return nil }

func validateStepRuntime(workdir, home, step string) error {
	if step == "" {
		return fmt.Errorf("no step in %s or %s", workdir, home)
	}
	return nil
}

func saveManifest(home, manifest string) error { return nil }

func record(mode, home string) { fmt.Println(mode, home) }

func logf(format string, args ...any) { fmt.Println(format, args) }

// agentPath returns the error from a function whose last result is an error.
func agentPath(ctx sdk.Context, name string) (string, error) {
	return filepath.Join(ctx.ConfigHome(), "agents", name), nil
}
