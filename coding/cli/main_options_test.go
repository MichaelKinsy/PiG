package cli

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// embeddedMainEnv makes the test binary a program that embeds the command line through Main.
const embeddedMainEnv = "PIG_TEST_EMBEDDED_MAIN"

// embeddedTrustEnv makes the test binary a program whose own extension decides project trust.
const embeddedTrustEnv = "PIG_TEST_EMBEDDED_TRUST"

// embeddedPackageTrustEnv makes the test binary a program whose own extension decides project trust for a package command.
const embeddedPackageTrustEnv = "PIG_TEST_EMBEDDED_PACKAGE_TRUST"

// embeddedFailingLogEnv makes the test binary a program whose own extension appends a line to the named file and fails.
const embeddedFailingLogEnv = "PIG_TEST_EMBEDDED_FAILING_LOG"

// trustingFactory is an extension that answers "yes" to project_trust.
func trustingFactory(api extension.API) error {
	api.OnProjectTrust(func(context.Context, extension.ProjectTrustEvent, extension.ProjectTrustContext) (extension.ProjectTrustEventResult, error) {
		return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustYes}, nil
	})
	return nil
}

func embeddedCommand(name string) extension.ExtensionFactory {
	return func(api extension.API) error {
		api.RegisterCommand(name, extension.CommandOptions{Description: name + " from the embedding program", Handler: func(context.Context, string) error { return nil }})
		return nil
	}
}

// embeddedNoBuiltinsEnv makes the embedding program one whose build strips every built-in extension (D92).
const embeddedNoBuiltinsEnv = "PIG_TEST_EMBEDDED_NO_BUILTINS"

// Pi's main(args, { extensionFactories }) loads the program's extensions after the built-in ones (main.ts:569-575, resource-loader.ts:376-377,1138-1141). A program that runs the command line through Main(args, &MainOptions{ExtensionFactories}) gets its named and bare factories loaded as `<inline:name>` and `<inline:N>`, numbered among the non-built-in entries, and their commands reach RPC get_commands after the built-in extensions' commands. A build that strips every built-in extension still loads the program's own.
// mutation-checked: Main dropping options.ExtensionFactories, buildResources not loading the inline extensions, or keeping the in-process extensions only when a built-in exists, fails.
func TestMainLoadsTheProgramsExtensionFactories(t *testing.T) {
	if os.Getenv(embeddedMainEnv) == "1" {
		if os.Getenv(embeddedNoBuiltinsEnv) == "1" {
			builtInExtensions = nil
		}
		// The child process: the test binary is the embedding program, and the arguments after -- are its command line.
		Main(flag.Args(), &MainOptions{ExtensionFactories: []extension.InlineExtension{
			extension.NamedInlineExtension{Name: "embedded", Factory: embeddedCommand("embedded-named")},
			extension.ExtensionFactory(embeddedCommand("embedded-bare")),
		}})
		return
	}
	if testing.Short() {
		t.Skip("runs the command line in a child process")
	}
	for _, noBuiltins := range []string{"0", "1"} {
		t.Run("no-builtins="+noBuiltins, func(t *testing.T) {
			home := t.TempDir()
			p := startRPCCommandAt(t, t.TempDir(), []string{
				embeddedMainEnv + "=1", embeddedNoBuiltinsEnv + "=" + noBuiltins, "HOME=" + home, "USERPROFILE=" + home, "PIG_TEST_FAUX=1", "PIG_OFFLINE=1",
			}, os.Args[0], "-test.run=^TestMainLoadsTheProgramsExtensionFactories$", "--", "--mode", "rpc", "--no-session")
			p.send(`{"id":"commands","type":"get_commands"}`)
			p.await("the command catalog", func(record rpcRecord) bool {
				if record["type"] != "response" || record["id"] != "commands" {
					return false
				}
				data, _ := record["data"].(map[string]any)
				commands, _ := data["commands"].([]any)
				var names, paths []string
				for _, value := range commands {
					command, _ := value.(map[string]any)
					info, _ := command["sourceInfo"].(map[string]any)
					names = append(names, command["name"].(string))
					if info["source"] == "inline" {
						paths = append(paths, info["path"].(string))
					}
				}
				// The built-in extensions' commands come first; which of them run depends on the strip build tags.
				if want := []string{"embedded-named", "embedded-bare"}; len(names) < len(want) || !slices.Equal(names[len(names)-len(want):], want) {
					t.Fatalf("commands = %v, want them to end with %v", names, want)
				}
				if want := []string{"<inline:embedded>", "<inline:2>"}; !slices.Equal(paths, want) {
					t.Fatalf("inline extension paths = %v, want %v", paths, want)
				}
				return true
			})
			p.closeAndWait("the catalog was read")
		})
	}
}

// Pi loads the program's extension factories in the project-trust pass (resource-loader.ts:501-507 loadProjectTrustExtensions with includeInlineFactories, :692-701), so their project_trust handlers decide the trust of a project with trust-requiring resources before the project's resources load (project-trust.ts:46-70), and the final pass keeps the same extensions without running the factories again (:768-773). A program's factory that answers "yes" makes a non-interactive start, which would otherwise leave the project untrusted, load the project's prompt template.
// mutation-checked: buildResources not loading the inline extensions in the pre-trust pass leaves the project untrusted and the template out; the final pass loading them again lists inline-load-2.
func TestMainRunsTheProgramsExtensionFactoriesInTheProjectTrustPass(t *testing.T) {
	if os.Getenv(embeddedTrustEnv) == "1" {
		loads := 0
		Main(flag.Args(), &MainOptions{ExtensionFactories: []extension.InlineExtension{
			extension.ExtensionFactory(func(api extension.API) error {
				// The command names the factory's run, so the catalog shows whether the final pass ran it again.
				loads++
				api.RegisterCommand("inline-load-"+strconv.Itoa(loads), extension.CommandOptions{Handler: func(context.Context, string) error { return nil }})
				api.OnProjectTrust(func(context.Context, extension.ProjectTrustEvent, extension.ProjectTrustContext) (extension.ProjectTrustEventResult, error) {
					return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustYes}, nil
				})
				return nil
			}),
		}})
		return
	}
	if testing.Short() {
		t.Skip("runs the command line in a child process")
	}
	project := t.TempDir()
	prompts := filepath.Join(project, codingagent.CONFIG_DIR_NAME, "prompts")
	if err := os.MkdirAll(prompts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prompts, "project-template.md"), []byte("---\ndescription: from the project\n---\nproject body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	p := startRPCCommandAt(t, project, []string{
		embeddedTrustEnv + "=1", "HOME=" + home, "USERPROFILE=" + home, "PIG_TEST_FAUX=1", "PIG_OFFLINE=1",
	}, os.Args[0], "-test.run=^TestMainRunsTheProgramsExtensionFactoriesInTheProjectTrustPass$", "--", "--mode", "rpc", "--no-session")
	p.send(`{"id":"commands","type":"get_commands"}`)
	p.await("the command catalog", func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != "commands" {
			return false
		}
		data, _ := record["data"].(map[string]any)
		commands, _ := data["commands"].([]any)
		var names []string
		for _, value := range commands {
			command, _ := value.(map[string]any)
			names = append(names, command["name"].(string))
		}
		if !slices.Contains(names, "project-template") {
			t.Fatalf("commands = %v, want the trusted project's template project-template", names)
		}
		if !slices.Contains(names, "inline-load-1") || slices.Contains(names, "inline-load-2") {
			t.Fatalf("commands = %v, want the factory run once, in the project-trust pass", names)
		}
		return true
	})
	p.closeAndWait("the catalog was read")
}

// Pi's main passes its extension factories, the program's after the built-in ones, to handlePackageCommand (main.ts:575,597), whose project-trust pass runs them (package-manager-cli.ts createCommandSettingsManager; package-command-paths.test.ts:307). A program's factory that answers "yes" to project_trust makes `list` show the project's packages.
// mutation-checked: Main not passing its factories to runPackageCommand leaves the project untrusted and its packages out.
func TestMainPassesTheProgramsExtensionFactoriesToPackageCommands(t *testing.T) {
	if os.Getenv(embeddedPackageTrustEnv) == "1" {
		Main(flag.Args(), &MainOptions{ExtensionFactories: []extension.InlineExtension{extension.ExtensionFactory(trustingFactory)}})
		return
	}
	if testing.Short() {
		t.Skip("runs the command line in a child process")
	}
	project := t.TempDir()
	settings := filepath.Join(project, codingagent.CONFIG_DIR_NAME, "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"packages":["npm:@project/pkg"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMainPassesTheProgramsExtensionFactoriesToPackageCommands$", "--", "list")
	cmd.Dir = project
	cmd.Env = append(os.Environ(), embeddedPackageTrustEnv+"=1", "HOME="+home, "USERPROFILE="+home, "PIG_OFFLINE=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("list: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Project packages:") || !strings.Contains(string(output), "npm:@project/pkg") {
		t.Fatalf("list output = %q, want the trusted project's package npm:@project/pkg", output)
	}
}

// With a project-trust pass, Pi runs the program's factories once, in that pass; the final pass keeps the extensions that loaded and runs no factory again, even when every factory failed (resource-loader.ts:768-778). The factory's failure is reported and stops startup (main.ts:908-916).
// mutation-checked: the final pass running the factories again when none loaded in the project-trust pass logs two runs.
func TestMainRunsAFailingProgramFactoryOnceWithAProjectTrustPass(t *testing.T) {
	if log := os.Getenv(embeddedFailingLogEnv); log != "" {
		Main(flag.Args(), &MainOptions{ExtensionFactories: []extension.InlineExtension{
			extension.ExtensionFactory(func(extension.API) error {
				file, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
				if err != nil {
					return err
				}
				_, _ = file.WriteString("run\n")
				_ = file.Close()
				return errors.New("embedded factory failed")
			}),
		}})
		return
	}
	if testing.Short() {
		t.Skip("runs the command line in a child process")
	}
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, codingagent.CONFIG_DIR_NAME, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "runs.log")
	home := t.TempDir()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMainRunsAFailingProgramFactoryOnceWithAProjectTrustPass$", "--", "--mode", "rpc", "--no-session")
	cmd.Dir = project
	cmd.Env = append(os.Environ(), embeddedFailingLogEnv+"="+log, "HOME="+home, "USERPROFILE="+home, "PIG_TEST_FAUX=1", "PIG_OFFLINE=1")
	cmd.Stdin = strings.NewReader("")
	output, err := cmd.CombinedOutput()
	if exitErr, ok := errors.AsType[*exec.ExitError](err); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("rpc start: %v, want exit status 1\n%s", err, output)
	}
	if !strings.Contains(string(output), "<inline:1>") || !strings.Contains(string(output), "embedded factory failed") {
		t.Fatalf("output = %q, want the failure of <inline:1>", output)
	}
	runs, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(runs) != "run\n" {
		t.Fatalf("factory runs = %q, want one run", runs)
	}
}
