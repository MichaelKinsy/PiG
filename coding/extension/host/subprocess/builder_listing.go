package subprocess

import (
	"os"
	"runtime"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// GoListing is how to read the packages of a standalone Go source extension
// the way its build resolves them: the go command, the flags that select the
// staged SDK, and the environment.
type GoListing struct {
	Command string
	Flags   []string
	Env     []string
}

// WithGoListing resolves srcDir as buildGo does, with the staged SDK when its
// go.mod needs one, and calls use. It compiles nothing and removes its
// temporary go.mod afterward.
//
// pig additive (D109): pig extension upgrade reads the types of an extension the way its build does.
func (b *Builder) WithGoListing(srcDir string, use func(GoListing) error) error {
	stagedSDK, err := b.resolveStagedSDK(srcDir, "go")
	if err != nil {
		return err
	}
	var flags []string
	if stagedSDK != "" {
		modPath, remove, err := stagedGoModFile(srcDir, stagedSDK)
		if err != nil {
			return err
		}
		defer remove()
		flags = append(flags, "-modfile="+modPath)
	}
	goToolchain, err := toolchain.ResolveGo()
	if err != nil {
		return err
	}
	env := toolchain.WorkDirEnv(srcDir, append(goToolchain.Environ(goBuildEnvironment(srcDir, os.Environ())), runtimecell.GoBuildCgoEnv(runtime.GOOS, runtime.GOARCH), "GOWORK=off"))
	return use(GoListing{Command: goToolchain.Command, Flags: flags, Env: env})
}
