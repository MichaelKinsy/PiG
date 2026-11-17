// Package ext is the Go fixture of the socket retirement rows (socket_retirement_runtime_test.go). The test names the gate directory in PIG_RETIREMENT_GATES.
package ext

import (
	"os"
	"path/filepath"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func touch(name string) {
	file, err := os.Create(filepath.Join(os.Getenv("PIG_RETIREMENT_GATES"), name))
	if err != nil {
		panic(err)
	}
	_ = file.Close()
}

func exists(name string) bool {
	_, err := os.Stat(filepath.Join(os.Getenv("PIG_RETIREMENT_GATES"), name))
	return err == nil
}

// Extension holds its factory while the test keeps the factory gate, before the SDK connects to the host.
func Extension() *sdk.Extension {
	if exists("factory-hold") {
		touch("factory-holding")
		for exists("factory-hold") {
			time.Sleep(5 * time.Millisecond)
		}
	}
	e := sdk.New("retire-probe")
	e.Command("retire-hold", "reload, then hold the command", func(ctx sdk.Context, _ string) error {
		if err := ctx.Reload(); err != nil {
			return err
		}
		touch("reloaded")
		for !exists("release-command") {
			time.Sleep(5 * time.Millisecond)
		}
		return nil
	})
	e.Command("retire-ping", "answer from this generation", func(sdk.Context, string) error {
		touch("pinged")
		return nil
	})
	return e
}
