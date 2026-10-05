package toolchain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/linkerexec"
)

// pig additive (D20): PiG compiles extensions, which Pi does not, and resolves one Go toolchain for every build.
//
// GoToolchain is one Go installation: a go command and the GOROOT it belongs to.
// Every build in a PiG process runs with exactly this pair. The go command
// compiles with the tools of whichever GOROOT it is given, and refuses tools of
// another release (`compile: version "go1.26.7" does not match go tool version
// "go1.26.1"`), so the pair must never come from two different selections.
type GoToolchain struct {
	// Command is the go executable to run.
	Command string
	// Root is the GOROOT of the installation Command belongs to. It is empty when
	// the command could not report it; the command then finds its own root.
	Root string
}

// Environ returns environ with GOROOT set to this installation, replacing any
// inherited value. An inherited GOROOT that names another installation is the
// source of the release mismatch above: version managers such as mise export
// GOROOT for the project directory a shell is in, while the go command found
// on PATH can belong to another version.
func (t GoToolchain) Environ(environ []string) []string {
	out := slices.DeleteFunc(slices.Clone(environ), func(kv string) bool { return envKey(kv, "GOROOT") })
	if t.Root == "" {
		return out
	}
	return append(out, "GOROOT="+t.Root)
}

// envKey reports whether kv assigns key. Windows compares variable names
// without regard to case.
func envKey(kv, key string) bool {
	name, _, _ := strings.Cut(kv, "=")
	if runtime.GOOS == "windows" {
		return strings.EqualFold(name, key)
	}
	return name == key
}

type resolveKey struct{ command, goroot, gotoolchain, cwd, home string }

var (
	resolveMu   sync.Mutex
	resolveMemo = map[resolveKey]GoToolchain{}
)

// ResolveGo selects the Go installation for builds: the go command on PATH, else
// the PiG-managed toolchain. It resolves once per selection and process, so
// concurrent extension builds cannot pick different installations. Nothing is
// run when the command lives inside its own installation, which is the case for
// a release archive, Homebrew and every version manager's real install. A shim or
// a distribution binary outside its GOROOT is asked where its root is, with the
// inherited GOROOT removed, so the answer describes that command rather than an
// environment left by another selection.
func ResolveGo() (GoToolchain, error) {
	command, managed, err := goCommand()
	if err != nil {
		return GoToolchain{}, err
	}
	cwd, _ := os.Getwd()
	home, _ := ConfigRoot()
	key := resolveKey{command, os.Getenv("GOROOT"), os.Getenv("GOTOOLCHAIN"), cwd, home}
	resolveMu.Lock()
	defer resolveMu.Unlock()
	if t, ok := resolveMemo[key]; ok {
		return t, nil
	}
	t := managed
	if t.Command == "" {
		if t, err = resolveGoCommand(command); err != nil {
			return GoToolchain{}, err
		}
	}
	resolveMemo[key] = t
	return t, nil
}

// goCommand finds the go command. The PiG-managed toolchain is a complete
// installation at a known root, so it is returned already resolved.
func goCommand() (string, GoToolchain, error) {
	if path, err := exec.LookPath("go"); err == nil {
		return path, GoToolchain{}, nil
	}
	root, err := ConfigRoot()
	if err == nil {
		goRoot := ManagedGoRoot(root)
		managed := filepath.Join(goRoot, "bin", exeName("go"))
		if info, statErr := os.Stat(managed); statErr == nil && !info.IsDir() {
			return managed, GoToolchain{Command: managed, Root: goRoot}, nil
		}
	}
	return "", GoToolchain{}, fmt.Errorf("go is not on PATH and no PiG-managed Go toolchain is installed; run `pig setup go`: %w", exec.ErrNotFound)
}

func resolveGoCommand(command string) (GoToolchain, error) {
	physical := command
	if resolved, err := filepath.EvalSymlinks(command); err == nil {
		physical = resolved
	}
	if abs, err := filepath.Abs(physical); err == nil {
		physical = abs
	}
	if root := filepath.Dir(filepath.Dir(physical)); isGoRoot(root) {
		return GoToolchain{Command: physical, Root: root}, nil
	}
	root, err := askGoRoot(command)
	if err != nil {
		// The build reports a command that cannot run; resolution only locates it.
		return GoToolchain{Command: command}, nil
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	// Pin the installation's own binary: a shim would choose again from whichever
	// directory each build runs in.
	if own := filepath.Join(root, "bin", exeName("go")); fileExists(own) {
		return GoToolchain{Command: own, Root: root}, nil
	}
	return GoToolchain{Command: command, Root: root}, nil
}

func isGoRoot(root string) bool {
	info, err := os.Stat(filepath.Join(root, "pkg", "tool"))
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func askGoRoot(command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := linkerexec.CommandContext(ctx, command, "env", "GOROOT")
	cmd.Env = append(slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return envKey(kv, "GOROOT") || envKey(kv, "GOTOOLCHAIN")
	}), "GOTOOLCHAIN=local")
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exit.Stderr)))
		}
		return "", err
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", errors.New("go env GOROOT printed nothing")
	}
	return root, nil
}

var goReleaseMismatch = lazyregexp.New(`compile: version "([^"]+)" does not match go tool version "([^"]+)"`)

// ReleaseMismatch reports a go build that ran the compiler of one Go release
// under the go command of another. It returns one line naming both releases and
// the fix.
func (t GoToolchain) ReleaseMismatch(output []byte) (string, bool) {
	match := goReleaseMismatch.FindSubmatch(output)
	if match == nil {
		return "", false
	}
	where := t.Command
	if t.Root != "" {
		where += " with GOROOT " + t.Root
	}
	return fmt.Sprintf("Go toolchain mismatch: the compiler is %s but the go command is %s (%s); reinstall that Go release so its tools match, or put an installation whose tools match on PATH", match[1], match[2], where), true
}
