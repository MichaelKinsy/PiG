package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// trialDirs are the trial pod's directories; tests point them at a temporary tree.
type trialDirs struct {
	root    string // the arm's runtime and the task's instruction (prepare init container)
	request string // the trial's request to the verifier gate (agent-done)
	answer  string // the gate's answer and the tests; read-only in the trial container
	logs    string // Harbor's /logs: agent/ and verifier/
	tests   string // where test.sh expects its files (Harbor's /tests)
}

var podTrialDirs = trialDirs{root: trialRoot, request: requestDir, answer: answerDir, logs: logsDir, tests: "/tests"}

// verifiedMarker is the file the fetch Job writes last into a model's directory: the digest of the pin it verified.
const verifiedMarker = ".k8sbench-verified"

// pinDigest is the hex sha256 of a model pin as K8SBENCH_MODEL carries it.
func pinDigest(pin string) string {
	sum := sha256.Sum256([]byte(pin))
	return hex.EncodeToString(sum[:])
}

// trialUsage sums the usage of every assistant message the agent finished.
type trialUsage struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CacheRead   int64 `json:"cacheRead"`
	CacheWrite  int64 `json:"cacheWrite"`
	TotalTokens int64 `json:"totalTokens"`
}

// trialResult is the one line a trial pod prints.
type trialResult struct {
	T                 string     `json:"t"`
	Task              string     `json:"task"`
	Arm               string     `json:"arm"`
	Attempt           int        `json:"attempt"`
	Model             string     `json:"model"`
	Image             string     `json:"image"`
	Reward            *float64   `json:"reward"`
	AgentExit         int        `json:"agentExit"`
	AgentTimedOut     bool       `json:"agentTimedOut,omitempty"`
	AgentWallMs       int64      `json:"agentWallMs"`
	AgentCPUMs        int64      `json:"agentCpuMs"`
	AgentError        string     `json:"agentError,omitempty"`
	AssistantMessages int        `json:"assistantMessages"`
	Usage             trialUsage `json:"usage"`
	VerifierExit      int        `json:"verifierExit"`
	VerifierTimedOut  bool       `json:"verifierTimedOut,omitempty"`
	VerifierWallMs    int64      `json:"verifierWallMs"`
	Error             string     `json:"error,omitempty"`
}

func cmdTrial(ctx context.Context, stdout io.Writer, getenv func(string) string) error {
	var tp trialPlan
	if err := json.Unmarshal([]byte(getenv("K8SBENCH_TRIAL")), &tp); err != nil {
		return fmt.Errorf("K8SBENCH_TRIAL: %w", err)
	}
	res := runTrial(ctx, tp, podTrialDirs)
	data, err := json.Marshal(res)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\n", data)
	return err
}

// runTrial runs the agent on the task's instruction, then the task's tests, as Harbor does: the tests arrive only
// after the agent has exited. Every failure is recorded in the result; the gate is always released.
//
// The gate answers one request, in a directory the trial container cannot write. A gate answer before the trial's own
// request means the agent asked for the tests, and the trial is not scored. The request carries a nonce chosen after
// the agent exited, so an answer the agent asked for cannot pass for the trial's.
func runTrial(ctx context.Context, tp trialPlan, d trialDirs) trialResult {
	res := trialResult{T: "trial", Task: tp.Task.Name, Arm: tp.Arm, Attempt: tp.Attempt, Model: tp.Model, Image: tp.Task.Image, AgentExit: -1, VerifierExit: -1}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		res.Error = err.Error()
		return res
	}
	request := "abort " + hex.EncodeToString(nonce)
	released := false
	release := func() {
		if !released {
			released = true
			if err := writeAtomic(filepath.Join(d.request, "agent-done"), []byte(request)); err != nil && res.Error == "" {
				res.Error = "release the verifier gate: " + err.Error()
			}
		}
	}
	defer release()
	// Anything in the gate's channel before the agent has run is a stale volume: the trial cannot be scored.
	for _, dir := range []string{d.request, d.answer} {
		if err := dirEmpty(dir); err != nil {
			res.Error = "before the agent ran: " + err.Error()
			return res
		}
	}
	if err := runTrialAgent(ctx, tp, d, &res); err != nil {
		res.Error = err.Error()
		return res
	}
	if err := dirEmpty(d.answer); err != nil {
		res.Error = "the agent asked the verifier gate for the tests: " + err.Error()
		return res
	}
	request = "run " + hex.EncodeToString(nonce)
	release()
	if res.Error != "" {
		return res
	}
	// A failed or timed-out agent run is still verified: Harbor scores whatever state the agent left.
	if err := runTrialVerifier(ctx, tp, d, request, &res); err != nil {
		res.Error = err.Error()
	}
	return res
}

// writeAtomic writes a file the gate reads whole: a reader never sees it created but not yet written.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func dirEmpty(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s holds %s", dir, entries[0].Name())
	}
	return nil
}

func trialModelsJSON(tp trialPlan) ([]byte, error) {
	model := map[string]any{
		"id": tp.Model, "name": tp.Model, "reasoning": false, "input": []string{"text"},
		"contextWindow": tp.ContextWindow, "maxTokens": tp.MaxTokens,
		"cost":   map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
		"compat": map[string]any{"supportsDeveloperRole": false, "supportsStore": false, "maxTokensField": "max_tokens"},
	}
	//portlint:allow jsonescape models.json for the agent under test; its JSON parser decodes \u003c and < alike
	return json.MarshalIndent(map[string]any{"providers": map[string]any{"k8sbench": map[string]any{ //portlint:allow mapkeyorder models.json for the agent under test, whose model registry reads members by name; Go-only bench tool
		"baseUrl": tp.BaseURL, "api": "openai-completions", "apiKey": "k8sbench-no-key", "models": []any{model},
	}}}, "", "  ")
}

func runTrialAgent(ctx context.Context, tp trialPlan, d trialDirs, res *trialResult) error {
	agentLogs := filepath.Join(d.logs, "agent")
	home := filepath.Join(agentLogs, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	env := append(os.Environ(), "PIG_CODING_AGENT_DIR="+home, "PI_CODING_AGENT_DIR="+home,
		"PI_OFFLINE=1", "PIG_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "PI_TELEMETRY=0")
	models, err := trialModelsJSON(tp)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(home, "models.json"), models, 0o644); err != nil {
		return err
	}
	instruction, err := os.ReadFile(filepath.Join(d.root, "task", "instruction.md"))
	if err != nil {
		return err
	}
	var argv []string
	switch tp.Arm {
	case armPig:
		argv = []string{filepath.Join(d.root, "bin", "pig")}
	case armPi:
		argv = []string{filepath.Join(d.root, "node", "bin", "node"), filepath.Join(d.root, filepath.FromSlash(piAgentCLI))}
	default:
		return fmt.Errorf("unknown arm %q", tp.Arm)
	}
	// An arm whose runtime does not start on the task's image (a C library the bundle's Node.js needs) is not scored:
	// its agent would fail every task for a reason the other arm does not share.
	versionErr := filepath.Join(agentLogs, "version.stderr")
	exit, _, _, _, err := runBounded(ctx, append(slices.Clone(argv), "--version"), env, "", io.Discard, versionErr, time.Minute)
	if err != nil {
		return fmt.Errorf("the %s arm does not start on this image: %w", tp.Arm, err)
	}
	if exit != 0 {
		return fmt.Errorf("the %s arm does not start on this image (exit %d): %s", tp.Arm, exit, tail(versionErr, 1024))
	}
	argv = append(argv, "--print", "--mode", "json", "--session-dir", filepath.Join(agentLogs, "sessions"),
		"--provider", "k8sbench", "--model", tp.Model, string(instruction))
	events, err := os.Create(filepath.Join(agentLogs, "events.jsonl"))
	if err != nil {
		return err
	}
	defer func() { _ = events.Close() }()
	var usage usageWriter
	stderrPath := filepath.Join(agentLogs, "stderr.log")
	exit, timedOut, wall, cpu, err := runBounded(ctx, argv, env, "", io.MultiWriter(events, &usage), stderrPath, time.Duration(tp.Task.AgentTimeoutSec*float64(time.Second)))
	res.AgentExit, res.AgentTimedOut, res.AgentWallMs, res.AgentCPUMs = exit, timedOut, wall.Milliseconds(), cpu.Milliseconds()
	usage.flush()
	res.Usage, res.AssistantMessages = usage.total, usage.messages
	if err != nil {
		return err
	}
	if exit != 0 && !timedOut {
		res.AgentError = strings.TrimSpace("agent exited " + strconv.Itoa(exit) + ": " + tail(stderrPath, 2048))
	}
	return nil
}

func runTrialVerifier(ctx context.Context, tp trialPlan, d trialDirs, request string, res *trialResult) error {
	wait := time.Duration(tp.TestsTimeoutSeconds) * time.Second
	//portlint:allow clock Go-only bench tool (no Pi counterpart): a wall-clock deadline for a real verifier pod; the now seam stamps pod records
	end := time.Now().Add(wait)
	for {
		if answer, err := os.ReadFile(filepath.Join(d.answer, "answer")); err == nil {
			if strings.TrimSpace(string(answer)) != request {
				return errors.New("the verifier gate answered another request than the trial's")
			}
			break
		}
		//portlint:allow clock Go-only bench tool (no Pi counterpart): the wall-clock deadline set above
		if time.Now().After(end) {
			return fmt.Errorf("the verifier gate did not hand over the tests within %s", wait)
		}
		if err := sleepCtx(ctx, 200*time.Millisecond); err != nil {
			return err
		}
	}
	// The tests must be exactly the locked ones.
	check, err := os.MkdirTemp("", "k8sbench-tests")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(check) }()
	if err := copyTree(filepath.Join(d.answer, "tests"), filepath.Join(check, "tests")); err != nil {
		return fmt.Errorf("the verifier gate handed over no tests: %w", err)
	}
	if err := copyFile(filepath.Join(d.root, "task", "instruction.md"), filepath.Join(check, "instruction.md")); err != nil {
		return err
	}
	got, err := taskFilesDigest(check)
	if err != nil {
		return err
	}
	if got != tp.Task.Files {
		return fmt.Errorf("the task's files have digest %s, the lock pins %s", got, tp.Task.Files)
	}
	// The agent ran in this container and could write anywhere it can: the tests directory and the verifier's logs
	// start empty, so only the locked tests run and only their reward is read.
	verifierLogs := filepath.Join(d.logs, "verifier")
	for _, dir := range []string{d.tests, verifierLogs} {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("clear %s before the tests: %w", dir, err)
		}
	}
	if err := copyTree(filepath.Join(check, "tests"), d.tests); err != nil {
		return err
	}
	if err := os.MkdirAll(verifierLogs, 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(verifierLogs, "test-output.log")
	log, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	exit, timedOut, wall, _, err := runBounded(ctx, []string{"bash", filepath.Join(d.tests, "test.sh")}, os.Environ(), "", log, logPath+".stderr",
		time.Duration(tp.Task.VerifierTimeoutSec*float64(time.Second)))
	res.VerifierExit, res.VerifierTimedOut, res.VerifierWallMs = exit, timedOut, wall.Milliseconds()
	if err != nil {
		return err
	}
	reward, err := readReward(verifierLogs)
	if err != nil {
		return err
	}
	res.Reward = &reward
	return nil
}

// readReward reads Harbor's reward file: reward.txt (one number) or reward.json ({"reward": n}).
func readReward(dir string) (float64, error) {
	if data, err := os.ReadFile(filepath.Join(dir, "reward.txt")); err == nil {
		return strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	}
	data, err := os.ReadFile(filepath.Join(dir, "reward.json"))
	if err != nil {
		return 0, errors.New("the tests wrote no reward.txt or reward.json")
	}
	var r struct{ Reward *float64 }
	if err := json.Unmarshal(data, &r); err != nil {
		return 0, fmt.Errorf("reward.json: %w", err)
	}
	if r.Reward == nil {
		return 0, errors.New("reward.json has no reward")
	}
	return *r.Reward, nil
}

// runBounded runs argv in its own process group, writing stdout to out and stderr to errPath, and kills the whole
// group at the timeout. exit is -1 when the process did not exit normally.
func runBounded(ctx context.Context, argv, env []string, dir string, out io.Writer, errPath string, timeout time.Duration) (exit int, timedOut bool, wall, cpu time.Duration, err error) {
	errFile, err := os.Create(errPath)
	if err != nil {
		return -1, false, 0, 0, err
	}
	defer func() { _ = errFile.Close() }()
	//portlint:allow ctxmapping ctx is mapped by hand below: its Done kills the whole process group, which CommandContext (leader only) would not
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = env, dir, out, errFile
	cmd.SysProcAttr = groupAttr()
	//portlint:allow clock Go-only bench tool (no Pi counterpart): measures a real process's wall time
	started := time.Now()
	if err := cmd.Start(); err != nil {
		return -1, false, 0, 0, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-timer.C:
		timedOut = true
		killGroup(cmd.Process)
		waitErr = <-done
	case <-ctx.Done():
		killGroup(cmd.Process)
		<-done
		return -1, false, time.Since(started), 0, ctx.Err()
	}
	wall = time.Since(started)
	if st := cmd.ProcessState; st != nil {
		cpu = st.UserTime() + st.SystemTime()
		exit = st.ExitCode()
	}
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return exit, timedOut, wall, cpu, waitErr
	}
	return exit, timedOut, wall, cpu, nil
}

// usageWriter reads the agent's JSON event stream as it is written and sums the usage of finished assistant
// messages. A line longer than its buffer is skipped.
type usageWriter struct {
	line     []byte
	total    trialUsage
	messages int
}

const maxEventLine = 16 << 20

func (u *usageWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			if len(u.line)+len(p) <= maxEventLine {
				u.line = append(u.line, p...)
			} else {
				u.line = append(u.line[:0], 0) // poisoned; dropped at the newline
			}
			break
		}
		u.line = append(u.line, p[:i]...)
		u.event(u.line)
		u.line = u.line[:0]
		p = p[i+1:]
	}
	return n, nil
}

func (u *usageWriter) flush() {
	if len(u.line) > 0 {
		u.event(u.line)
		u.line = u.line[:0]
	}
}

func (u *usageWriter) event(line []byte) {
	var e struct {
		Type    string `json:"type"`
		Message struct {
			Role  string      `json:"role"`
			Usage *trialUsage `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &e) != nil || e.Type != "message_end" || e.Message.Role != "assistant" || e.Message.Usage == nil {
		return
	}
	u.messages++
	u.total.Input += e.Message.Usage.Input
	u.total.Output += e.Message.Usage.Output
	u.total.CacheRead += e.Message.Usage.CacheRead
	u.total.CacheWrite += e.Message.Usage.CacheWrite
	u.total.TotalTokens += e.Message.Usage.TotalTokens
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// copyTree copies regular files and directories; anything else is refused.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case info.IsDir():
			return os.MkdirAll(target, 0o755)
		case info.Mode().IsRegular():
			return copyFile(p, target)
		default:
			return fmt.Errorf("%s is not a regular file", p)
		}
	})
}

// cmdModelFetch downloads a locked model's files into K8SBENCH_MODELS_DIR/<id>, checking each file's size and
// sha256, keeps files that already match, and marks the directory verified last.
func cmdModelFetch(ctx context.Context, stdout io.Writer, getenv func(string) string) error {
	var pin struct {
		ID string `json:"id"`
		modelLock
	}
	if err := json.Unmarshal([]byte(getenv("K8SBENCH_MODEL")), &pin); err != nil {
		return fmt.Errorf("K8SBENCH_MODEL: %w", err)
	}
	engines := map[string]engineLock{pin.Engine: {}}
	if err := pin.validate(pin.ID, engines); err != nil {
		return err
	}
	base := getenv("K8SBENCH_MODEL_BASE_URL")
	if base == "" {
		base = "https://huggingface.co"
	}
	return fetchModel(ctx, &http.Client{}, base, filepath.Join(getenv("K8SBENCH_MODELS_DIR"), pin.ID), pin.modelLock, pinDigest(getenv("K8SBENCH_MODEL")), stdout)
}

// fetchModel verifies m's files into dir and then writes the marker the server checks: sum, the digest of the pin.
func fetchModel(ctx context.Context, client *http.Client, base, dir string, m modelLock, sum string, progress io.Writer) error {
	marker := filepath.Join(dir, verifiedMarker)
	if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, f := range m.Files {
		dst := filepath.Join(dir, filepath.FromSlash(f.Path))
		if info, err := os.Stat(dst); err == nil && info.Size() == f.Size {
			if sum, err := fileSHA256(dst); err == nil && sum == f.SHA256 {
				_, _ = fmt.Fprintf(progress, "{\"t\":\"model-file\",\"path\":%q,\"kept\":true}\n", f.Path)
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		//portlint:allow clock Go-only bench tool (no Pi counterpart): measures a real download's wall time
		started := time.Now()
		url := strings.TrimSuffix(base, "/") + "/" + m.Repo + "/resolve/" + m.Revision + "/" + f.Path
		if err := fetchPinned(ctx, client, pinnedArchive{url: url, size: f.Size, sha256: f.SHA256}, "", dst+".part"); err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
		if err := os.Rename(dst+".part", dst); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(progress, "{\"t\":\"model-file\",\"path\":%q,\"bytes\":%d,\"ms\":%d}\n", f.Path, f.Size, time.Since(started).Milliseconds())
	}
	return os.WriteFile(marker, []byte(sum+"\n"), 0o644)
}
