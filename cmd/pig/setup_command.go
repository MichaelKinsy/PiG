package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

const setupUsage = `Usage: pig setup [status|go|container]

  status      Show the toolchains PiG uses for extensions and Piglet builds (default)
  go          Install a verified Go toolchain under $PIG_HOME/toolchains/go
              --version goX.Y.Z   Go release to install (default: this binary's Go)
  container   Show how to install a container runtime on this system
`

// runSetupCommand handles `pig setup`. It reports -1 for other commands.
func runSetupCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "setup" {
		return -1
	}
	topic, rest := "status", args[1:]
	if len(rest) > 0 {
		topic, rest = rest[0], rest[1:]
	}
	switch topic {
	case "status":
		return setupStatus(stdout)
	case "go":
		return setupGo(rest, stdout, stderr)
	case "container":
		_, _ = io.WriteString(stdout, containerGuide(runtime.GOOS))
		return 0
	case "help", "-h", "--help":
		_, _ = io.WriteString(stdout, setupUsage)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "pig setup: unknown topic %q\n%s", topic, setupUsage)
		return 2
	}
}

type setupTool struct {
	name, purpose, remedy string
	find                  func() (string, bool)
}

func onPath(name string) func() (string, bool) {
	return func() (string, bool) {
		path, err := exec.LookPath(name)
		return path, err == nil
	}
}

func setupTools() []setupTool {
	return []setupTool{
		{name: "go", purpose: "Go extension cells and native Piglet builds", remedy: "pig setup go", find: func() (string, bool) {
			path, err := toolchain.Go()
			return path, err == nil
		}},
		{name: "container", purpose: "container Piglet builds", remedy: "pig setup container", find: func() (string, bool) {
			name, path, ok := toolchain.ContainerRuntime()
			return name + " (" + path + ")", ok
		}},
		{name: "cargo", purpose: "Rust extension cells", remedy: "install Rust from https://rustup.rs", find: onPath("cargo")},
		{name: "node", purpose: "Node and TypeScript extensions", remedy: "install Node.js 22.13 or newer from https://nodejs.org", find: onPath("node")},
		{name: python3Name(), purpose: "Python extensions", remedy: "install Python 3 from https://www.python.org/downloads/", find: onPath(python3Name())},
		{name: "git", purpose: "git: Package sources", remedy: "install Git from https://git-scm.com/downloads", find: onPath("git")},
	}
}

func python3Name() string {
	if runtime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}

func setupStatus(stdout io.Writer) int {
	_, _ = fmt.Fprintf(stdout, "PiG toolchains (%s/%s)\n\n", runtime.GOOS, runtime.GOARCH)
	var missing []setupTool
	for _, tool := range setupTools() {
		location, ok := tool.find()
		if !ok {
			location = "missing"
			missing = append(missing, tool)
		}
		_, _ = fmt.Fprintf(stdout, "  %-10s %-44s %s\n", tool.name, location, tool.purpose)
	}
	if len(missing) == 0 {
		_, _ = io.WriteString(stdout, "\nEvery toolchain is available.\n")
		return 0
	}
	_, _ = io.WriteString(stdout, "\nNext steps:\n")
	for _, tool := range missing {
		_, _ = fmt.Fprintf(stdout, "  %-10s %s\n", tool.name, tool.remedy)
	}
	_, _ = io.WriteString(stdout, "\nPiG runs without these tools. They are needed only for the extension languages and Piglet builds listed above.\n")
	return 0
}

func setupGo(args []string, stdout, stderr io.Writer) int {
	version := runtime.Version()
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--version" && i+1 < len(args):
			i++
			version = args[i]
		case strings.HasPrefix(arg, "--version="):
			version = strings.TrimPrefix(arg, "--version=")
		default:
			_, _ = fmt.Fprintf(stderr, "pig setup go: unknown option %q\n%s", arg, setupUsage)
			return 2
		}
	}
	if !strings.HasPrefix(version, "go1.") {
		_, _ = fmt.Fprintf(stderr, "pig setup go: %q is not a Go release; pass --version goX.Y.Z\n", version)
		return 2
	}
	root, err := toolchain.ConfigRoot()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "pig setup go: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	_, _ = fmt.Fprintf(stdout, "Installing %s for %s/%s from %s (SHA-256 checked against the published index)...\n", version, runtime.GOOS, runtime.GOARCH, toolchain.GoDownloadBase)
	client := &http.Client{Timeout: 15 * time.Minute}
	goCommand, err := toolchain.InstallGo(ctx, client, toolchain.GoDownloadBase, version, runtime.GOOS, runtime.GOARCH, root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "pig setup go: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Installed %s at %s.\nPiG uses it automatically when go is not on PATH. To use it in your shell, add %s to PATH.\n", version, goCommand, filepath.Dir(goCommand))
	return 0
}

func containerGuide(goos string) string {
	const image = "The container builder also needs a digest-pinned builder image in PIG_BUILDERS_FILE; see `pig docs show piglet-binaries`.\n"
	switch goos {
	case "darwin":
		return "Install one container runtime:\n" +
			"  Docker Desktop  https://docs.docker.com/desktop/setup/install/mac-install/\n" +
			"  Podman          brew install podman && podman machine init && podman machine start\n" +
			"  Colima          brew install colima docker && colima start\n\n" + image
	case "windows":
		return "Install one container runtime:\n" +
			"  Docker Desktop  winget install Docker.DockerDesktop   (uses WSL 2)\n" +
			"  Podman Desktop  winget install RedHat.Podman-Desktop\n\n" + image
	default:
		return "Install one container runtime:\n" +
			"  Docker Engine   https://docs.docker.com/engine/install/\n" +
			"  Podman          sudo apt install podman   (Debian, Ubuntu)\n" +
			"                  sudo dnf install podman   (Fedora, RHEL)\n\n" +
			"Then confirm it works without sudo: docker info (or podman info).\n\n" + image
	}
}
