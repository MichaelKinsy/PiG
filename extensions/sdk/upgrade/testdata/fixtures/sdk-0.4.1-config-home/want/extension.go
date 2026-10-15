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
		configHome, err := ctx.ConfigHome()
		if err != nil {
			return err
		}
		if err := c.save(configHome); err != nil {
			return err
		}
		cwd := ctx.Cwd()
		configHome2, err := ctx.ConfigHome()
		if err != nil {
			return err
		}
		if err := validateStepRuntime(cwd, configHome2, args); err != nil {
			return err
		}
		configHome3, err := ctx.ConfigHome()
		if err != nil {
			return err
		}
		if err := saveManifest(configHome3, args); err != nil {
			return err
		}
		mode2 := ctx.Mode()
		configHome4, err := ctx.ConfigHome()
		if err != nil {
			return err
		}
		record(mode2, configHome4)
		configHome5, err := ctx.ConfigHome()
		if err != nil {
			return err
		}
		fmt.Println(configHome5)
		configHome6, err := ctx.ConfigHome()
		if err != nil {
			return err
		}
		fmt.Printf("home %s\n", configHome6)
		configHome7, err := ctx.ConfigHome()
		if err != nil {
			return err
		}
		logf(configHome7)
		return nil
	})

	ext.OnSessionStart(func(ctx sdk.Context, event map[string]any) (any, error) {
		configHome8, err := ctx.ConfigHome()
		if err != nil {
			return nil, err
		}
		outputDir = filepath.Join(strings.TrimSpace(configHome8), "subagent-output")
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
	configHome9, err := ctx.ConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(configHome9, "agents", name), nil
}
