package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Upstream resource-loader.ts reuses the pre-trust factory and runtime in the
// final selection. Exercise the real entrypoints so a second host cannot hide
// behind correct lower-level load bookkeeping.
func TestStartupReusesPreTrustExtensionsAcrossEntrypoints(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, mode := range []string{"print", "json", "rpc", "model-error", "failed-preload"} {
		for _, trusted := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/trusted=%v", mode, trusted), func(t *testing.T) {
				home := t.TempDir()
				project := trustProjectFixture(t)
				marker := filepath.Join(home, "events")
				fixture := filepath.Join(home, "pretrust.mjs")
				decision := "no"
				if trusted {
					decision = "yes"
				}
				pidPath := filepath.Join(home, "pid")
				source := fmt.Sprintf(`import {appendFileSync,writeFileSync} from "node:fs";
export default function(pi) {
 writeFileSync(%q,String(process.pid));
 appendFileSync(%q,"factory\n");
 let calls=0;
 pi.on("project_trust",()=>{calls++;return {trusted:%q};});
 pi.on("session_start",()=>appendFileSync(%q,"session:"+calls+"\n"));
}`, pidPath, marker, decision, marker)
				if err := os.WriteFile(fixture, []byte(source), 0o600); err != nil {
					t.Fatal(err)
				}
				projectFixture := filepath.Join(project, codingagent.CONFIG_DIR_NAME, "extensions", "project.ts")
				if err := os.WriteFile(projectFixture, fmt.Appendf(nil, `import {appendFileSync} from "node:fs"; export default function(){appendFileSync(%q,"project\n");}`, marker), 0o600); err != nil {
					t.Fatal(err)
				}
				agentDir := filepath.Join(home, "agent")
				if err := os.MkdirAll(agentDir, 0o755); err != nil {
					t.Fatal(err)
				}
				models := `{"providers":{"test-faux":{"baseUrl":"http://localhost:0","api":"test-faux","authHeader":false,"models":[{"id":"faux-1","name":"Test Faux","api":"test-faux","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}}}`
				if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
					t.Fatal(err)
				}
				args := []string{"--model", "test-faux/faux-1", "--no-session", "-e", fixture}
				if mode == "failed-preload" {
					failedPath := filepath.Join(home, "failed.mjs")
					if err := os.WriteFile(failedPath, fmt.Appendf(nil, `import {appendFileSync} from "node:fs"; export default function(){appendFileSync(%q,"failed\n"); throw new Error("expected preload failure");}`, marker), 0o600); err != nil {
						t.Fatal(err)
					}
					args = append(args, "-e", failedPath)
				}
				switch mode {
				case "rpc":
					args = append(args, "--mode", "rpc")
				case "json":
					args = append(args, "--mode", "json", "--print", "What is 20+22?")
				case "model-error":
					args = append(args, "--print", "hello")
				default:
					args = append(args, "--print", "What is 20+22?")
				}
				command := exec.CommandContext(testbudget.Context(t), binary, args...)
				command.Dir = project
				command.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic")
				if mode == "model-error" {
					command.Env = append(command.Env, "PIG_TEST_FAUX=")
				}
				output, runErr := command.CombinedOutput()
				// Upstream main.ts exits 1 on any extension load error, including
				// a failed pre-trust preload, after the error and the -ne hint.
				if (runErr != nil) != (mode == "model-error" || mode == "failed-preload") {
					t.Fatalf("entrypoint error=%v output=%s", runErr, output)
				}
				if mode == "failed-preload" {
					failedPath := filepath.Join(home, "failed.mjs")
					for _, want := range []string{`Error: Failed to load extension "` + failedPath + `": Failed to load extension: `, `Hint: Start without extensions using "pig -ne".`} {
						if !strings.Contains(string(output), want) {
							t.Fatalf("output lacks %q:\n%s", want, output)
						}
					}
				}
				observed, err := os.ReadFile(marker)
				if err != nil {
					t.Fatalf("read factory marker: %v output=%s", err, output)
				}
				if strings.Count(string(observed), "factory\n") != 1 {
					t.Fatalf("factory executed more than once: %q", observed)
				}
				if mode != "model-error" && mode != "failed-preload" && !strings.Contains(string(observed), "session:1\n") {
					t.Fatalf("pre-trust state lost: %q output=%s", observed, output)
				}
				if mode == "failed-preload" && strings.Count(string(observed), "failed\n") != 1 {
					t.Fatalf("failed preload retried: %q", observed)
				}
				pidBytes, err := os.ReadFile(pidPath)
				if err != nil {
					t.Fatal(err)
				}
				pid, err := strconv.Atoi(string(pidBytes))
				if err != nil {
					t.Fatal(err)
				}
				probe := exec.CommandContext(testbudget.Context(t), "node", "-e", `
const pid=Number(process.argv[1]);
const deadline=Date.now()+5000;
function poll() {
 try { process.kill(pid,0); } catch(error) { if(error.code==="ESRCH") return; throw error; }
 if(Date.now()>=deadline) throw new Error("extension process remains alive");
 setTimeout(poll,10);
}
poll();`, strconv.Itoa(pid))
				if output, err := probe.CombinedOutput(); err != nil {
					t.Fatalf("extension process %d survived entrypoint exit: %v\n%s", pid, err, output)
				}

				if strings.Contains(string(observed), "project\n") != trusted {
					t.Fatalf("project selection trusted=%v events=%q", trusted, observed)
				}
			})
		}
	}
}
