package runtimecell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// GoBuildModule is the generated module a build of one Go extension compiles,
// and the go command that compiles it. A tool reads the extension's types in
// it exactly as the build sees them: the same SDK, requirements and replaces.
type GoBuildModule struct {
	// Dir holds go.mod, go.sum and the generated runner.
	Dir string
	// Command is the go command and Env its environment, which pins the module graph to the generated one.
	Command string
	Env     []string
	// SDKRoot is the SDK the build resolves.
	SDKRoot string
}

// WithGoBuildModule stages the module a build of extension compiles, holds the
// SDK against a restage while use runs, and removes the module after. It
// compiles nothing.
//
// pig additive (D109): pig extension upgrade reads the types of an extension the way its build does.
func WithGoBuildModule(ctx context.Context, extension GoExtension, preferredSDKRoot string, use func(GoBuildModule) error) error {
	normalized, err := normalizeGoExtensions([]GoExtension{extension}, true)
	if err != nil {
		return err
	}
	candidates := []string{preferredSDKRoot, os.Getenv("PIG_SDK_GO_ROOT"), GoSDKPathOverride(normalized[0].Root)}
	candidates = append(candidates, stagedGoSDKRoots()...)
	_, err = pigsdklock.WithBuildCandidates(ctx, candidates, func() (struct{}, error) {
		sdkRoot, err := findSDKRootWithPreferred(normalized, preferredSDKRoot)
		if err != nil {
			return struct{}{}, err
		}
		return pigsdklock.WithBuildForRoot(ctx, sdkRoot, func() (struct{}, error) {
			buildDir, err := os.MkdirTemp("", "pig-go-upgrade-*")
			if err != nil {
				return struct{}{}, fmt.Errorf("create generated Go module directory: %w", err)
			}
			defer func() { _ = os.RemoveAll(buildDir) }()
			if err := writeGoBuildDir(buildDir, normalized, sdkRoot); err != nil {
				return struct{}{}, err
			}
			goToolchain, err := toolchain.ResolveGo()
			if err != nil {
				explained, _ := explainMissingToolchain("go", err)
				return struct{}{}, explained
			}
			env := toolchain.WorkDirEnv(buildDir, append(goToolchain.Environ(cacheBuildEnvironment(buildDir)), GoBuildCgoEnv(runtime.GOOS, runtime.GOARCH), "GOWORK=off"))
			return struct{}{}, use(GoBuildModule{Dir: filepath.Clean(buildDir), Command: goToolchain.Command, Env: env, SDKRoot: sdkRoot})
		})
	})
	return err
}
