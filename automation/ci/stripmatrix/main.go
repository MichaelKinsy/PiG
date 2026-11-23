// Command stripmatrix is make piglet-strip-matrix: it checks cmd/pig under Piglet strip tag sets, single build tags and
// then all of them (coding/piglet.StripBuildTags, the tags a Piglet Binary is built with).
//
// For each set, in order:
//
//	vet    automation/ci/go-vet.sh over every package and its tests, which fails on a file that names something the set compiles out;
//	       the live tag is added, so the live test files are vetted with the set as well
//	test   go test of package ai, where every test passes but the ones the set's red list names, which fail (redDir), and of wholePackages
//	smoke  cmd/pig built with the set: --list-models exits 0 and lists models, none of a provider the set strips and some of every other
//	       strippable provider; provider-bridge and an extension's pi-ai complete() answer a request for a model the set keeps and
//	       end a request for a model on a stripped API with the strip message
//
// -sets api (the default) takes the single tags that strip a model API; -sets all takes every single tag. -tests api (the
// default) runs the test step for the sets that strip a model API, all tags among them; -tests all runs it for every set.
//
// -guard is make piglet-strip-guard, the part of the matrix that costs seconds: for each set, go vet of guardPackages only, with the
// live tag added, and no other step. It catches a file of package ai or of a provider package that names something a strip tag
// compiles out, which is how a change most often breaks a stripped build; a file of another package that does so, a failing test and
// a stripped binary that misbehaves are caught only by the matrix.
//
// -coding is make piglet-strip-tests: for each set, go test of codingPackages (the tests of the coding agent) and no other step. The
// matrix's own test step runs ./ai/ only, so a test of ./coding or ./internal/codingagent that needs a feature a set strips was never
// run under that set, and a new one could fail there unseen. -print-sets lists the sets as JSON, for a job matrix that runs one set per job.
//
// Where it runs: the default matrix took 2610 s at 8 CPUs, so it is a step of make release-check and a job of the nightly workflow
// (.github/workflows/nightly.yml), and make ci-contracts, which every pull request runs, has the guard.
//
// Usage: go run ./automation/ci/stripmatrix [-guard|-coding|-print-sets] [-sets api|all] [-tests api|all] [-set tags]... [-step vet|test|smoke|coding]...
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// aiPackage holds the built-in provider tables (ai.BuiltinProviders, ai.BuiltinModels, ai.APIKeyProviders, the catalog). Its tests run
// whole; the tests known to fail under a set are listed in redDir.
const aiPackage = "./ai/"

// wholePackages have no test known to fail under a strip tag: the optional provider packages and the consumer of ai.BuiltinModels.
var wholePackages = []string{"./ai/providers/...", "./ai/all/...", "./durable/harness/..."}

// guardPackages are what -guard vets in place of every package: package ai, the provider packages and the rest of its tree.
const guardPackages = "./ai/..."

// codingPackages are what -coding tests under each set: the coding agent's packages, whose tests build sessions, registries and the
// interactive mode over the built-ins a strip tag compiles out. The test files that need a stripped feature carry a build constraint
// naming its tag, or ask pigstrip.Has, so every test of these packages passes under every set.
var codingPackages = []string{"./coding/...", "./internal/codingagent/..."}

// codingTestTimeout bounds go test of codingPackages under one set: coding/cli and coding/extension/host/subprocess alone take
// about twenty minutes at eight CPUs.
const codingTestTimeout = "90m"

// wholeTestTimeout bounds go test of aiPackage and wholePackages under one set.
// Plain ./ai/ in this step measures 428.898 to 592.346 s across eight runs at eight CPUs on xd670 (load up to 94, plus 600.047 s
// killed by go test's ten-minute default at the gate's opening burst) and 438.196 to 534.135 s across all four sets on a 4-CPU
// GitHub ubuntu runner (nightly job 137304936373: 438.196, 468.893, 500.160, 534.135 s) — 73 to 99 percent of the ten-minute
// default in both places. Three times the worst measure (592.346 s) is 29.6 minutes, so 30m matches ci.yml's plain ./ai timeout.
const wholeTestTimeout = "30m"

// redDir holds one file per tag set, named after the tag (all.txt for all tags): the top-level tests of aiPackage that fail under
// the set, one per line, the name followed by the reason. The list can only shrink: a listed test that passes fails the gate, and
// so does a failing test that is not listed.
const redDir = "automation/ci/stripmatrix/red"

// redTests reads the tests known to fail under the set, in file order. A set without a file has none.
func redTests(set tagSet) ([]string, error) {
	name := "all"
	if len(set.tags) == 1 {
		name = set.tags[0]
	}
	data, err := os.ReadFile(filepath.Join(redDir, name+".txt"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseRed(string(data))
}

// parseRed reads a red list: blank lines and # comments aside, every line is a test name and a reason.
func parseRed(text string) ([]string, error) {
	var tests []string
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		name := fields[0]
		if !strings.HasPrefix(name, "Test") || len(fields) < 2 {
			return nil, fmt.Errorf("red list line %q is not a test name and a reason", line)
		}
		if slices.Contains(tests, name) {
			return nil, fmt.Errorf("red list names %s twice", name)
		}
		tests = append(tests, name)
	}
	return tests, nil
}

// aiTestArgs returns the go arguments that test aiPackage outside red under set.
func aiTestArgs(set tagSet, red []string) []string {
	args := []string{"test", "-tags", set.String(), "-count=1", "-timeout", wholeTestTimeout}
	if len(red) > 0 {
		args = append(args, "-skip", "^("+strings.Join(red, "|")+")$")
	}
	return append(args, aiPackage)
}

// wholeTestArgs returns the go arguments that test wholePackages under set.
func wholeTestArgs(set tagSet) []string {
	return append([]string{"test", "-tags", set.String(), "-count=1", "-timeout", wholeTestTimeout}, wholePackages...)
}

// testAI runs aiPackage under the set. Every test outside the set's red list must pass in one run of the package, and every listed
// test must fail in a process of its own (a panic there ends that test only).
func testAI(set tagSet, scratch string) error {
	red, err := redTests(set)
	if err != nil {
		return err
	}
	if err := run(nil, "go", aiTestArgs(set, red)...); err != nil {
		return fmt.Errorf("a test of %s outside the red list fails: %w", aiPackage, err)
	}
	if len(red) == 0 {
		return nil
	}
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	binary := filepath.Join(scratch, "ai.test")
	if err := run(nil, "go", "test", "-c", "-tags", set.String(), "-o", binary, aiPackage); err != nil {
		return err
	}
	passed := make([]bool, len(red))
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	for i, name := range red {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			//nolint:gosec // G204 (class b): program is binary, the test binary of aiPackage that the go test -c above built into the scratch directory this process made with os.MkdirTemp; args are the fixed -test.run and -test.count=1 with a name parseRed read from the committed red list (redDir), which must start with Test. Nothing comes from a flag or the environment.
			cmd := exec.Command(binary, "-test.run", "^"+name+"$", "-test.count=1")
			cmd.Dir = aiPackage
			passed[i] = cmd.Run() == nil
		})
	}
	wg.Wait()
	var stale []string
	for i, name := range red {
		if passed[i] {
			stale = append(stale, name)
		}
	}
	if len(stale) > 0 {
		return fmt.Errorf("listed in the red list but passing (or gone): remove %s", strings.Join(stale, ", "))
	}
	return nil
}

// tagSet is one row of the matrix.
type tagSet struct {
	tags []string
	// apis are the model APIs the set compiles out.
	apis []string
}

func (s tagSet) String() string { return strings.Join(s.tags, ",") }

// pickSet reports whether the run checks the set: the sets named by -set when there are any, otherwise every set of -sets all, and of
// -sets api the sets that strip a model API.
func pickSet(set tagSet, only []string, single string) bool {
	if len(only) > 0 {
		return slices.Contains(only, set.String())
	}
	return single == "all" || len(set.apis) > 0
}

// tagSets lists each build tag alone, then all of them. apiTags maps the tag of each strippable API to the API.
func tagSets(tags []string, apiTags map[string]string) []tagSet {
	sets := make([]tagSet, 0, len(tags)+1)
	all := tagSet{tags: tags}
	for _, tag := range tags {
		set := tagSet{tags: []string{tag}}
		if api, ok := apiTags[tag]; ok {
			set.apis = []string{api}
			all.apis = append(all.apis, api)
		}
		sets = append(sets, set)
	}
	return append(sets, all)
}

// strippedProviders lists, in catalog order, the providers left without a chat model when apis are stripped: each has catalog models, all on those APIs.
func strippedProviders(models []ai.GeneratedModel, providers, apis []string) []string {
	var stripped []string
	for _, provider := range providers {
		own, kept := 0, 0
		for i := range models {
			if models[i].Provider != provider {
				continue
			}
			own++
			if !slices.Contains(apis, string(models[i].API)) {
				kept++
			}
		}
		if own > 0 && kept == 0 {
			stripped = append(stripped, provider)
		}
	}
	return stripped
}

// listedProviders counts the model rows of each provider in --list-models output: the rows after the header, whose first column is the provider.
func listedProviders(output string) map[string]int {
	rows := map[string]int{}
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "provider") {
		return rows
	}
	for _, line := range lines[1:] {
		if fields := strings.Fields(line); len(fields) > 1 {
			rows[fields[0]]++
		}
	}
	return rows
}

// checkList fails when the model list holds a row of a provider in stripped, or no row of a provider in kept.
func checkList(output string, stripped, kept []string) error {
	rows := listedProviders(output)
	if len(rows) == 0 {
		return fmt.Errorf("--list-models listed no model:\n%s", output)
	}
	var problems []string
	for _, provider := range stripped {
		if rows[provider] > 0 {
			problems = append(problems, fmt.Sprintf("%d models of stripped provider %s", rows[provider], provider))
		}
	}
	for _, provider := range kept {
		if rows[provider] == 0 {
			problems = append(problems, fmt.Sprintf("no model of provider %s, which this set keeps", provider))
		}
	}
	if len(problems) > 0 {
		return errors.New("--list-models lists " + strings.Join(problems, "; "))
	}
	return nil
}

// run executes one command of a step with the process's output.
func run(env []string, name string, args ...string) error {
	//nolint:gosec // G204 (class a): program is the literal go or automation/ci/go-vet.sh at every call site; args are fixed subcommands and flags, the constant package patterns (aiPackage, wholePackages, guardPackages, ./cmd/pig), an output path under the os.MkdirTemp scratch directory, and -tags of a tagSet, whose tags are piglet.StripBuildTags (the generated strip ID table). The -set flag only selects among those sets by equality and is never passed on.
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), env...)
	return cmd.Run()
}

// probeModel is the pi-ai model object of a probe request: a model of provider on api at baseURL.
func probeModel(provider, api, baseURL string) map[string]any {
	return map[string]any{"provider": provider, "id": "strip-matrix", "name": "Strip matrix", "api": api, "baseUrl": baseURL, "reasoning": false,
		"input": []string{"text"}, "contextWindow": 128000, "maxTokens": 4096, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}
}

// stripMessage is what a request for a model of provider on a stripped api ends with in a build that names no Piglet.
func stripMessage(provider, api string) string {
	return fmt.Sprintf("Provider %s: API %s is stripped from this Piglet (strip.apis: %s)", provider, api, api)
}

// checkBridge sends `pig provider-bridge` one stream request for a model of an API the set keeps (it must end with the server's
// text) and one for a model of each stripped API (it must end with the strip message), and requires a clean exit.
func checkBridge(binary string, env []string, serverURL string, stripped map[string]string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "provider-bridge")
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	want := map[string]string{"kept": "DONE"}
	requests := []map[string]any{{"id": "kept", "model": probeModel("strip-matrix", "openai-completions", serverURL+"/v1")}}
	for api, provider := range stripped {
		want[api] = stripMessage(provider, api)
		requests = append(requests, map[string]any{"id": api, "model": probeModel(provider, api, serverURL)})
	}
	for _, request := range requests {
		request["type"], request["simple"] = "stream", true
		request["request"] = map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi", "timestamp": 0}}, "apiKey": "strip-matrix"}
		line, err := json.Marshal(request)
		if err != nil {
			return err
		}
		if _, err := stdin.Write(append(line, '\n')); err != nil {
			return fmt.Errorf("provider-bridge: %w\n%s", err, stderr.String())
		}
	}
	var problems []string
	lines := bufio.NewScanner(stdout)
	lines.Buffer(nil, 16<<20)
	for len(want) > 0 && lines.Scan() {
		var reply struct{ ID, Type string }
		if json.Unmarshal(lines.Bytes(), &reply) != nil || reply.Type != "result" && reply.Type != "error" {
			continue
		}
		if text, ok := want[reply.ID]; ok && !strings.Contains(lines.Text(), text) {
			problems = append(problems, fmt.Sprintf("request %s ended with %s, want %q in it", reply.ID, lines.Text(), text))
		}
		delete(want, reply.ID)
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil || len(want) > 0 {
		problems = append(problems, fmt.Sprintf("exit %v with %d requests unanswered\n%s", err, len(want), stderr.String()))
	}
	if len(problems) > 0 {
		return errors.New("provider-bridge: " + strings.Join(problems, "; "))
	}
	return nil
}

// checkExtension loads an extension that calls pi-ai's complete() for the same models as checkBridge and prints each outcome, and
// requires the kept API's text, the strip message for each stripped API, and a clean exit.
func checkExtension(binary string, env []string, scratch, serverURL string, stripped map[string]string) error {
	models := map[string]any{"kept": probeModel("strip-matrix", "openai-completions", serverURL+"/v1")}
	// want maps the start of each outcome line to a text the line holds.
	want := map[string]string{"STRIP-MATRIX kept stop=stop ": "text=DONE"}
	for api, provider := range stripped {
		models[api] = probeModel(provider, api, serverURL)
		want["STRIP-MATRIX "+api+" "] = stripMessage(provider, api)
	}
	encoded, err := json.Marshal(models)
	if err != nil {
		return err
	}
	source := `import { complete } from "@earendil-works/pi-ai";
const models: Record<string, any> = ` + string(encoded) + `;
export default async function () {
	for (const [id, model] of Object.entries(models)) {
		try {
			const message: any = await complete(model, { messages: [{ role: "user", content: "hi", timestamp: 0 }] }, { apiKey: "strip-matrix" });
			const text = message.content.map((block: any) => block.text ?? "").join("");
			console.error("STRIP-MATRIX " + id + " stop=" + message.stopReason + " text=" + text + (message.errorMessage ? " error=" + message.errorMessage : ""));
		} catch (error) {
			console.error("STRIP-MATRIX " + id + " threw " + error);
		}
	}
}
`
	extension := filepath.Join(scratch, "strip-matrix-extension.ts")
	if err := os.WriteFile(extension, []byte(source), 0o600); err != nil {
		return err
	}
	//nolint:gosec // G204 (class b): program is binary, the cmd/pig that smoke's go build wrote into the os.MkdirTemp scratch directory; args are literals but extension, the path of the TypeScript file this function wrote into that directory from a constant source and the generated API names.
	cmd := exec.Command(binary, "-e", extension, "--no-session", "--model", "test-faux/faux-1", "-p", "What is 20+22?")
	cmd.Dir = filepath.Join(scratch, "home")
	cmd.Env = append(slices.Clone(env), "PIG_TEST_FAUX=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("extension complete(): %w\n%s", err, output)
	}
	for start, text := range want {
		if !slices.ContainsFunc(strings.Split(string(output), "\n"), func(line string) bool {
			return strings.HasPrefix(line, start) && strings.Contains(line, text)
		}) {
			return fmt.Errorf("extension complete(): no line %q with %q in\n%s", start, text, output)
		}
	}
	return nil
}

// smoke builds cmd/pig with the set's tags and runs it with every home under scratch. --list-models: with a stored key for each
// catalog provider, a build lists the models of every provider it keeps; strippable are the providers some set strips. provider-bridge and, in a build with the
// Node runtime, an extension's complete(): the two callers of the pi-ai compat streams (checkBridge, checkExtension).
func smoke(set tagSet, scratch string, providers, strippable, stripped []string, apiProviders map[string]string) error {
	defer func() { _ = os.RemoveAll(scratch) }()
	home := filepath.Join(scratch, "home")
	agent := filepath.Join(home, "agent")
	if err := os.MkdirAll(agent, 0o700); err != nil {
		return err
	}
	binary := filepath.Join(scratch, "pig")
	if err := run([]string{"CGO_ENABLED=0"}, "go", "build", "-tags", set.String(), "-o", binary, "./cmd/pig"); err != nil {
		return fmt.Errorf("build: %w", err)
	}
	keys := map[string]any{}
	for _, provider := range providers {
		keys[provider] = map[string]string{"type": "api_key", "key": "strip-matrix"}
	}
	auth, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(agent, "auth.json"), auth, 0o600); err != nil {
		return err
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "PIG_HOME=" + home, "PI_HOME=" + home, "PIG_CODING_AGENT_DIR=" + agent, "PI_CODING_AGENT_DIR=" + agent, "PIG_OFFLINE=1"}
	//nolint:gosec // G204 (class b): program is binary, the cmd/pig that the go build above wrote into the os.MkdirTemp scratch directory; the only arg is the literal --list-models.
	cmd := exec.Command(binary, "--list-models")
	cmd.Dir = home
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("--list-models: %w\n%s", err, stderr.String())
	}
	kept := slices.DeleteFunc(slices.Clone(strippable), func(provider string) bool { return slices.Contains(stripped, provider) })
	if err := checkList(stdout.String(), stripped, kept); err != nil {
		return err
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"DONE\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	strippedAPIs := map[string]string{}
	for _, api := range set.apis {
		strippedAPIs[api] = apiProviders[api]
	}
	if err := checkBridge(binary, env, server.URL, strippedAPIs); err != nil {
		return err
	}
	if slices.Contains(set.tags, pigstrip.Tag(pigstrip.NodeExtensions)) {
		return nil
	}
	return checkExtension(binary, env, scratch, server.URL, strippedAPIs)
}

// step is one check of a tag set.
type step struct {
	name string
	do   func() error
}

type setFlags []string

func (f *setFlags) String() string     { return strings.Join(*f, " ") }
func (f *setFlags) Set(v string) error { *f = append(*f, v); return nil }

func main() {
	single := flag.String("sets", "api", "the single tags checked before all tags: api (a tag that strips a model API) or all")
	tests := flag.String("tests", "api", "the sets whose provider table tests run: api (a set that strips a model API) or all")
	guard := flag.Bool("guard", false, "vet only "+guardPackages+" under each set, and run no other step (make piglet-strip-guard)")
	coding := flag.Bool("coding", false, "run only go test of "+strings.Join(codingPackages, " ")+" under each set, and no other step (make piglet-strip-tests)")
	printSets := flag.Bool("print-sets", false, "print the tag sets that -sets and -set pick as a JSON array of -set values, and exit (the job matrix of make piglet-strip-tests)")
	var only, onlySteps setFlags
	flag.Var(&only, "set", "check only this tag set (comma-separated tags, as printed); repeatable")
	flag.Var(&onlySteps, "step", "run only this step (vet, test or smoke); repeatable")
	flag.Parse()
	if !slices.Contains([]string{"api", "all"}, *single) || !slices.Contains([]string{"api", "all"}, *tests) {
		fmt.Fprintln(os.Stderr, "stripmatrix: -sets and -tests are api or all")
		os.Exit(2)
	}
	apis := pigstrip.Known(pigstrip.ListAPIs)
	apiTags := map[string]string{}
	for _, api := range apis {
		apiTags[pigstrip.Tag(api)] = api
	}
	providers := ai.ListProviders()
	strippable := strippedProviders(ai.GeneratedModels, providers, apis)
	// apiProviders names, for each strippable API, the provider its strip leaves without a model: the provider of the probe model.
	apiProviders := map[string]string{}
	for _, api := range apis {
		if alone := strippedProviders(ai.GeneratedModels, providers, []string{api}); len(alone) > 0 {
			apiProviders[api] = alone[0]
		}
	}
	picked := func(set tagSet) bool { return pickSet(set, only, *single) }
	if *printSets {
		names := []string{}
		for _, set := range tagSets(piglet.StripBuildTags(), apiTags) {
			if picked(set) {
				names = append(names, set.String())
			}
		}
		encoded, err := json.Marshal(names)
		if err != nil {
			fmt.Fprintln(os.Stderr, "stripmatrix:", err)
			os.Exit(1)
		}
		fmt.Println(string(encoded))
		return
	}
	scratch, err := os.MkdirTemp("", "pig-strip-matrix-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "stripmatrix:", err)
		os.Exit(1)
	}
	failed := 0
	for i, set := range tagSets(piglet.StripBuildTags(), apiTags) {
		if !picked(set) {
			continue
		}
		stripped := strippedProviders(ai.GeneratedModels, providers, set.apis)
		steps := []step{{"vet", func() error { return run(nil, "./automation/ci/go-vet.sh", "-tags", set.String()+",live") }}}
		if *tests == "all" || len(set.apis) > 0 {
			steps = append(steps, step{"test", func() error {
				if err := testAI(set, filepath.Join(scratch, fmt.Sprint(i)+"-test")); err != nil {
					return err
				}
				return run(nil, "go", wholeTestArgs(set)...)
			}})
		}
		steps = append(steps, step{"smoke", func() error {
			return smoke(set, filepath.Join(scratch, fmt.Sprint(i)), providers, strippable, stripped, apiProviders)
		}})
		if *guard {
			steps = []step{{"vet " + guardPackages, func() error { return run(nil, "go", "vet", "-tags", set.String()+",live", guardPackages) }}}
		}
		if *coding {
			steps = []step{{"coding", func() error {
				return run(nil, "go", append([]string{"test", "-tags", set.String(), "-count=1", "-timeout", codingTestTimeout}, codingPackages...)...)
			}}}
		}
		for _, step := range steps {
			if len(onlySteps) > 0 && !slices.Contains(onlySteps, step.name) {
				continue
			}
			start := time.Now()
			err := step.do()
			verdict := "ok"
			if err != nil {
				verdict = "FAIL: " + err.Error()
				failed++
			}
			fmt.Printf("== strip matrix %s %s (%s): %s\n", step.name, set, time.Since(start).Round(time.Second), verdict)
		}
	}
	if err := os.RemoveAll(scratch); err != nil {
		fmt.Fprintln(os.Stderr, "stripmatrix:", err)
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "stripmatrix: %d steps failed\n", failed)
		os.Exit(1)
	}
}
