package aotruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// buildOptions are the inputs of one compiled-host build.
type buildOptions struct {
	// Compiler is the Pi-Bolt runtime executable that runs `build --compile`.
	Compiler string
	Platform string
	// CPU is "native" for the machine's instruction set or "baseline" for any x86-64 CPU.
	CPU     string
	Request Request
	WorkDir string
	Version string
	// Log receives each phase's output; nil discards it.
	Log io.Writer
	// Dynamic builds a host that loads extensions from source with jiti and compiles none in: the host for the conformance suite.
	Dynamic bool
	// Training bounds one training run.
	TrainTimeout time.Duration
}

// buildMode names how the image holds the bundle's bytecode.
type buildMode string

const (
	// modeLean omits bytecode from the static heap: the compiler inlines across functions and the executable is smaller.
	modeLean buildMode = "lean"
	// modeBytecodeKept keeps the bytecode of every function in the heap and lets the compiler decline a function that is too large to compile.
	modeBytecodeKept buildMode = "bytecode-kept"
)

// buildResult is what a build leaves in its work directory.
type buildResult struct {
	// Executable is the compiled host; Dir holds it and the assets it reads beside itself.
	Executable string
	Dir        string
	Mode       buildMode
	// Inputs are the extension files the image was built from, with the stat the host compares to find the image stale.
	Inputs []inputFile
	Phases map[string]time.Duration
	Bytes  int64
}

type inputFile struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	MtimeNs int64  `json:"mtimeNs"`
}

// processWaitDelay bounds how long a build waits, after its process group ended, for output pipes that a process outside the group still holds.
const processWaitDelay = 5 * time.Second

// errTooLarge is a build that the compiler declined because a function exceeds its size limit.
var errTooLarge = errors.New("the compiler declined a function larger than its limit")

// errPathSensitive is a set the build refuses because a file would find nothing where it looks.
type errPathSensitive struct{ finding pathFinding }

func (e errPathSensitive) Error() string { return "not compiled in: " + e.finding.String() }

// build compiles the request's extension set and the runtime tree into one executable in opts.WorkDir/out. It stages the tree, bundles a bytecode
// build, runs the host once per extension to record the order its functions first run in, and compiles the ahead-of-time build with that order.
func build(ctx context.Context, opts buildOptions) (*buildResult, error) {
	log := opts.Log
	if log == nil {
		log = io.Discard
	}
	phases := map[string]time.Duration{}
	timed := func(name string, since time.Time) { phases[name] = time.Since(since) }
	stageDir := filepath.Join(opts.WorkDir, "stage")
	outDir := filepath.Join(opts.WorkDir, "out")
	for _, dir := range []string{stageDir, outDir, filepath.Join(opts.WorkDir, "home"), filepath.Join(opts.WorkDir, "tmp")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	started := time.Now()
	st, err := stageRuntime(opts.Request.RuntimeTree, stageDir)
	if err != nil {
		return nil, err
	}
	wrapped, err := st.splitLargeLiterals()
	if err != nil {
		return nil, fmt.Errorf("stage the runtime tree: %w", err)
	}
	patched, err := st.patchHostExecutable()
	if err != nil {
		return nil, fmt.Errorf("stage the runtime tree: %w", err)
	}
	config, err := st.writeConfig()
	if err != nil {
		return nil, fmt.Errorf("write the build configuration: %w", err)
	}
	if opts.Dynamic {
		if err := st.patchVirtualModules(); err != nil {
			return nil, fmt.Errorf("stage the runtime tree: %w", err)
		}
	}
	if err := st.writeEntry(opts.Request.Members, opts.Version, opts.Dynamic); err != nil {
		return nil, fmt.Errorf("write the host entry: %w", err)
	}
	_, _ = fmt.Fprintf(log, "stage: wrapped %d large literals, patched %d host executable reads\n", wrapped, patched)
	timed("stage", started)

	mode := modeLean
	bytecodeHost := filepath.Join(opts.WorkDir, "host-bytecode")
	meta := filepath.Join(opts.WorkDir, "meta.json")
	started = time.Now()
	if err := runCompiler(ctx, opts, stageDir, config, meta, "", bytecodeHost, nil, log); err != nil {
		return nil, fmt.Errorf("bytecode build: %w", err)
	}
	timed("bytecode", started)

	inputs, err := metafileInputs(meta, stageDir, filepath.Join(stageDir, stageTree), opts.Request.Members)
	if err != nil {
		return nil, fmt.Errorf("read the build's input list: %w", err)
	}
	files := make([]string, len(inputs))
	for i, input := range inputs {
		files[i] = input.Path
	}
	if opts.Dynamic {
		files = nil
	}
	if finding, found, err := scanPathSensitive(files); err != nil {
		return nil, err
	} else if found {
		return nil, errPathSensitive{finding}
	}

	started = time.Now()
	orderFile := filepath.Join(opts.WorkDir, "bytecode.order")
	if err := train(ctx, opts, bytecodeHost, orderFile, log); err != nil {
		return nil, fmt.Errorf("training: %w", err)
	}
	timed("train", started)

	started = time.Now()
	out := filepath.Join(outDir, "pig-ts-host")
	err = runCompiler(ctx, opts, stageDir, config, "", orderFile, out, aotEnv(opts, modeLean), log)
	if errors.Is(err, errTooLarge) {
		// A function of the extension's own packages is too large to compile. Keep the bytecode of every function in the heap and let the compiler
		// decline that one, as the compiler's own guidance says; the image is larger and the code that stays bytecode is slower.
		mode = modeBytecodeKept
		_, _ = fmt.Fprintf(log, "aot build: %v; building with the bytecode kept in the heap\n", err)
		err = runCompiler(ctx, opts, stageDir, config, "", orderFile, out, aotEnv(opts, modeBytecodeKept), log)
	}
	if err != nil {
		return nil, fmt.Errorf("aot build: %w", err)
	}
	timed("aot", started)

	started = time.Now()
	if err := st.layoutAssets(outDir, opts.Platform); err != nil {
		return nil, fmt.Errorf("lay out the host's assets: %w", err)
	}
	if err := copyNotices(filepath.Dir(opts.Compiler), filepath.Join(outDir, "licenses")); err != nil {
		return nil, fmt.Errorf("copy the compiler's notices: %w", err)
	}
	if err := checkImage(ctx, opts, out, log); err != nil {
		return nil, err
	}
	timed("check", started)
	info, err := os.Stat(out)
	if err != nil {
		return nil, err
	}
	return &buildResult{Executable: out, Dir: outDir, Mode: mode, Inputs: inputs, Phases: phases, Bytes: info.Size()}, nil
}

// compilerEnv is the environment of a compiler or host process: the machine's PATH, an empty home and the variables for the build.
func compilerEnv(opts buildOptions, extra ...string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Join(opts.WorkDir, "home"),
		"TMPDIR=" + filepath.Join(opts.WorkDir, "tmp"),
		"BUN_INSTALL_CACHE_DIR=" + filepath.Join(opts.WorkDir, "home", ".bun-cache"),
	}
	return append(env, extra...)
}

// aotEnv is the environment of the ahead-of-time build: static heap, ahead-of-time code, no JIT in the build, loop splitting as Pi-Bolt's build uses it,
// and mutable built-ins: the host's D83 clone hooks cannot install on frozen ones. Frozen built-ins and a build with the JIT on measured the same /words hot
// loop (about 133 ms against Bun's 97 ms) on the five-extension benchmark, so neither is worth the loss.
func aotEnv(opts buildOptions, mode buildMode) []string {
	env := []string{
		"BUN_JSC_useJIT=0", "BUN_STATIC_HEAP=1", "BUN_AOT=1",
		"BUN_JSC_useAOTLoopSplitting=1", "BUN_JSC_aotLoopSplittingPolicy=5", "BUN_AOT_JIT=0", "BUN_JSC_useImmutableIntrinsics=0",
	}
	if opts.CPU == "baseline" {
		env = append(env, "BUN_AOT_CPU=baseline")
	}
	if mode == modeLean {
		return append(env, "BUN_JSC_omitBytecodeFromStaticHeap=1")
	}
	return append(env, "BUN_JSC_omitBytecodeFromStaticHeap=0", "BUN_JSC_allowAOTDeclinedFunctions=1")
}

// runCompiler runs `bun build --compile` in the stage. An empty orderFile builds the bytecode build that training runs.
func runCompiler(ctx context.Context, opts buildOptions, stageDir, config, meta, orderFile, out string, env []string, log io.Writer) error {
	args := []string{
		"build", "--compile", "--no-compile-autoload-bunfig", "--no-compile-autoload-dotenv",
		"--target=bun-" + opts.Platform, "--bytecode", "--format=esm", "--compile-exec-argv=--smol",
		"--tsconfig-override=" + config,
	}
	if meta != "" {
		args = append(args, "--metafile="+meta)
	}
	if orderFile != "" {
		args = append(args, "--bytecode-order="+orderFile)
	}
	args = append(args, "./"+stageEntry, "./"+stageTree+"/provider-socket.mjs", "--outfile", out)
	cmd := exec.CommandContext(ctx, opts.Compiler, args...)
	ownProcessGroup(cmd)
	cmd.Dir = stageDir
	cmd.Env = compilerEnv(opts, env...)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	endProcessGroup(cmd)
	text := filterCompilerOutput(output.String())
	_, _ = io.WriteString(log, text)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if strings.Contains(output.String(), "maximumAOTCandidateBytecodeSize") {
			return fmt.Errorf("%w: %s", errTooLarge, firstLine(output.String(), "bigger than"))
		}
		return fmt.Errorf("%w: %s", err, lastLines(text, 6))
	}
	return nil
}

// filterCompilerOutput drops the compiler's per-function progress lines and a known harmless diagnostic about the configuration path.
func filterCompilerOutput(text string) string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "AOT: ") || strings.HasPrefix(line, "Internal error: directory mismatch for directory") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func firstLine(text, containing string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, containing) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func lastLines(text string, n int) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}

// metafileInputs lists the files outside the runtime tree that the bundle read, plus each member's nearest package.json, with their stat.
func metafileInputs(meta, stageDir, treeDir string, members []Member) ([]inputFile, error) {
	data, err := os.ReadFile(meta)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Inputs map[string]json.RawMessage `json:"inputs"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var paths []string
	add := func(path string) {
		if path != "" && !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	for key := range parsed.Inputs {
		path := key
		if !filepath.IsAbs(path) {
			path = filepath.Join(stageDir, path)
		}
		path = filepath.Clean(path)
		if strings.HasPrefix(path, treeDir+string(filepath.Separator)) || filepath.Dir(path) == stageDir {
			continue
		}
		add(path)
	}
	slices.Sort(paths)
	for _, member := range members {
		add(nearestPackageJSON(member.Entry))
	}
	inputs := make([]inputFile, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, inputFile{Path: path, Size: info.Size(), MtimeNs: info.ModTime().UnixNano()})
	}
	return inputs, nil
}

func nearestPackageJSON(entry string) string {
	dir := filepath.Dir(entry)
	for range 16 {
		candidate := filepath.Join(dir, "package.json")
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// train runs the bytecode build once per extension, as PiG starts it, and merges the function orders the runs record. A run connects to a socket,
// as the host does, so the order covers module evaluation, the extension's factory and its registration. The host closes the extension's input and
// its socket to end the run; the runtime then exits normally, which is when it writes its order.
func train(ctx context.Context, opts buildOptions, bytecodeHost, orderFile string, log io.Writer) error {
	timeout := opts.TrainTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	orders := filepath.Join(opts.WorkDir, "orders")
	if err := os.MkdirAll(orders, 0o755); err != nil {
		return err
	}
	// The version run exercises the entry module alone and guarantees an order even when no extension registers.
	runs := [][]string{{"--version"}}
	members := opts.Request.Members
	if opts.Dynamic {
		probe := filepath.Join(opts.WorkDir, "train-probe.mjs")
		if err := os.WriteFile(probe, []byte("export default function (pi) { pi.registerCommand(\"probe\", { description: \"probe\", handler: async () => {} }); }\n"), 0o644); err != nil {
			return err
		}
		members = []Member{{Name: "train-probe", Entry: probe}}
	}
	for _, member := range members {
		runs = append(runs, []string{"cli", member.Entry})
	}
	for i, args := range runs {
		if err := trainRun(ctx, opts, bytecodeHost, filepath.Join(orders, fmt.Sprintf("order.%d", i)), args, timeout, log); err != nil {
			_, _ = fmt.Fprintf(log, "train %v: %v\n", args, err)
		}
	}
	seen := map[string]bool{}
	var merged []string
	files, _ := filepath.Glob(filepath.Join(orders, "order.*"))
	slices.Sort(files)
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if line != "" && !seen[line] {
				seen[line] = true
				merged = append(merged, line)
			}
		}
	}
	if len(merged) == 0 {
		return errors.New("no training run recorded a function order (a build without one silently drops its ahead-of-time image)")
	}
	return os.WriteFile(orderFile, []byte(strings.Join(merged, "\n")+"\n"), 0o644)
}

func trainRun(ctx context.Context, opts buildOptions, bytecodeHost, orderOut string, args []string, timeout time.Duration, log io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bytecodeHost, args...)
	// A training run executes the extensions' factories, which may start processes of their own: the whole group ends with the run.
	ownProcessGroup(cmd)
	cmd.Dir = opts.WorkDir
	cwd := opts.WorkDir
	env := compilerEnv(opts, "BUN_BYTECODE_ORDER_OUT="+orderOut, "PIG_EXT_NAME=train", "PIG_EXT_CWD="+cwd, "PIG_TS_RUNTIME_TREE="+opts.Request.RuntimeTree)
	var listener net.Listener
	if args[0] == "cli" {
		sock := filepath.Join(opts.WorkDir, "train.sock")
		_ = os.Remove(sock)
		var err error
		if listener, err = net.Listen("unix", sock); err != nil {
			return err
		}
		defer func() { _ = listener.Close() }()
		env = append(env, "PIG_EXT_SOCKET="+sock)
	}
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer endProcessGroup(cmd)
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	if listener != nil {
		var once sync.Once
		accepted := make(chan net.Conn, 1)
		go func() {
			if conn, err := listener.Accept(); err == nil {
				accepted <- conn
			}
		}()
		select {
		case conn := <-accepted:
			buffer := make([]byte, 64<<10)
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, _ = conn.Read(buffer)
			// A short beat lets the registration finish before the host ends the run.
			time.Sleep(200 * time.Millisecond)
			once.Do(func() { _ = conn.Close() })
		case err := <-exited:
			return fmt.Errorf("exited before it connected: %w: %s", err, lastLines(stderr.String(), 4))
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	_ = stdin.Close()
	select {
	case <-exited:
	case <-ctx.Done():
		return ctx.Err()
	}
	if listener != nil && stderr.Len() > 0 {
		_, _ = io.WriteString(log, "train stderr: "+lastLines(stderr.String(), 4)+"\n")
	}
	return nil
}

// checkImage verifies the executable maps its compiled image: a build whose image the executable cannot use silently runs from bytecode.
func checkImage(ctx context.Context, opts buildOptions, exe string, log io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--version")
	ownProcessGroup(cmd)
	cmd.Env = compilerEnv(opts, "BUN_STATIC_HEAP_VERBOSE=1")
	output, err := cmd.CombinedOutput()
	endProcessGroup(cmd)
	_, _ = log.Write(output)
	if err != nil {
		return fmt.Errorf("the compiled host does not start: %w: %s", err, lastLines(string(output), 4))
	}
	if !strings.Contains(string(output), "image registered: true") {
		return fmt.Errorf("the compiled host does not use its compiled image: %s", lastLines(string(output), 4))
	}
	return nil
}

// copyNotices puts the compiler's license files and a pointer to its corresponding source beside the executable it produced: the executable holds
// Bun, JavaScriptCore and ICU code under their licenses, and travels with those notices if the user copies it.
func copyNotices(compilerDir, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, kind := range toolchainFiles {
		if kind != kindNotice {
			continue
		}
		data, err := os.ReadFile(filepath.Join(compilerDir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return err
		}
	}
	text := "This executable was compiled on this machine by the Pi-Bolt ahead-of-time compiler " + toolchainVersion + ". It contains Bun (MIT), WebKit's JavaScriptCore (LGPL-2.0 and BSD-2-Clause) and ICU (Unicode-3.0) code.\nThe license texts are in this directory. The corresponding source of the compiler is at " + sourcesURL + "\n"
	return os.WriteFile(filepath.Join(dir, "NOTICE.txt"), []byte(text), 0o644)
}
