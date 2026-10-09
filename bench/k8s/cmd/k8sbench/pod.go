package main

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// podEnv is what the in-pod session depends on outside the plan.
type podEnv struct {
	sys system
	// root is the image's install root ({root} in profile commands; /opt/pig in the image).
	root string
	// data is the writable scratch directory (the pod's emptyDir).
	data string
	// results, when it names a directory, receives a copy of every output line (the optional PVC).
	results string
	node    string
	pod     string
	// getenv reads the Job's environment (os.Getenv in a pod).
	getenv func(string) string
	// ensure lists directories the session creates before it runs anything (TMPDIR and HOME).
	ensure []string
	// pinWait waits between the two /proc/stat readings that find the quietest cores.
	pinWait func(context.Context) error
	now     func() time.Time
	// nodeRuntime reports the Node release the programs run on (`node --version`), or "" when there is no node.
	nodeRuntime func(context.Context) string
}

func nodeOnPath(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	v, err := exec.CommandContext(ctx, "node", "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(v))
}

func podEnvFromOS() podEnv {
	env := podEnv{sys: system{root: "/"}, root: "/opt/pig", data: "/data", results: "/results", node: os.Getenv("NODE_NAME"), pod: os.Getenv("POD_NAME"), now: time.Now, getenv: os.Getenv, nodeRuntime: nodeOnPath,
		pinWait: func(ctx context.Context) error { return sleepCtx(ctx, time.Second) }}
	if s := os.Getenv("K8SBENCH_ROOT"); s != "" {
		env.root = s
	}
	if s := os.Getenv("K8SBENCH_DATA"); s != "" {
		env.data = s
	}
	if s, ok := os.LookupEnv("K8SBENCH_RESULTS"); ok {
		env.results = s
	}
	for _, name := range []string{"TMPDIR", "HOME"} {
		if dir := os.Getenv(name); dir != "" {
			env.ensure = append(env.ensure, dir)
		}
	}
	return env
}

// session is one pod's run of a plan. Every output line is one JSON object with a "t" field; the runner reads the
// pod log and ignores any other line.
type session struct {
	plan   *plan
	env    podEnv
	out    io.Writer
	copy   io.Writer
	commit string
	pin    []int
	// taskset is the resolved taskset binary when the session pins.
	taskset string
	// gomaxprocs is the GOMAXPROCS every step runs with: the number of measured cores, at most the pod's CPUs (its CPU
	// limit). Set explicitly, because a Go runtime otherwise derives it from the node's CPUs or the cgroup quota, which
	// differ between node types.
	gomaxprocs int
	// cpuModel and cpuFeatures name the node type every seed and sample line carries.
	cpuModel    string
	cpuFeatures []string
}

func (s *session) emit(o *object) error {
	line, err := o.MarshalJSON()
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if s.copy != nil {
		_, _ = s.copy.Write(line)
	}
	_, err = s.out.Write(line)
	return err
}

// runPod runs the plan: facts, pinning, a timed from-zero seed per target and size, then the measured rounds. Rounds
// are outermost and each round rotates the target order by one, so every target sees the whole session's neighbours
// and no target always runs first.
func runPod(ctx context.Context, pl *plan, env podEnv, out io.Writer) error {
	if err := pl.validate(); err != nil {
		return err
	}
	s := &session{plan: pl, env: env, out: out}
	if env.results != "" {
		if info, err := os.Stat(env.results); err == nil && info.IsDir() {
			dir := filepath.Join(env.results, pl.Run)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			f, err := os.Create(filepath.Join(dir, "pod.jsonl"))
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			s.copy = f
		}
	}
	commit, _ := os.ReadFile(filepath.Join(env.root, "COMMIT"))
	s.commit = strings.TrimSpace(string(commit))
	if s.commit == "" {
		s.commit = "unknown"
	}
	if pl.Commit != "" && !sameCommit(s.commit, pl.Commit) {
		return fmt.Errorf("the image was built from %s, not the requested commit %s", s.commit, pl.Commit)
	}
	for _, dir := range []string{"logs", "fixtures"} {
		if err := os.MkdirAll(filepath.Join(env.data, pl.Run, dir), 0o755); err != nil {
			return err
		}
	}
	// The Job points TMPDIR and HOME into the scratch volume, because the root filesystem is read-only.
	for _, dir := range env.ensure {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := s.facts(); err != nil {
		return err
	}
	if err := s.choosePin(ctx); err != nil {
		return err
	}
	seeds, samples := 0, 0
	for i, n := range pl.Turns {
		for _, name := range rotate(pl.Targets, i) {
			t, _ := pl.Profile.byName(name)
			if len(t.Seed) == 0 {
				continue
			}
			if err := s.seed(ctx, t, n); err != nil {
				return err
			}
			seeds++
		}
	}
	for r := range pl.Rounds {
		for _, n := range pl.Turns {
			for _, name := range rotate(pl.Targets, r) {
				t, _ := pl.Profile.byName(name)
				if err := s.sample(ctx, t, n, r); err != nil {
					return err
				}
				samples++
			}
		}
	}
	done := newObject()
	done.set("t", "done")
	done.set("seeds", seeds)
	done.set("samples", samples)
	done.set("at", env.now().UTC().Format(time.RFC3339))
	return s.emit(done)
}

// sameCommit matches a full or abbreviated commit; an image built from a dirty tree matches only a request that
// names it dirty.
func sameCommit(image, want string) bool {
	imageSHA, imageDirty := strings.CutSuffix(image, "-dirty")
	wantSHA, wantDirty := strings.CutSuffix(want, "-dirty")
	if imageDirty != wantDirty || len(wantSHA) < 7 {
		return false
	}
	return strings.HasPrefix(imageSHA, wantSHA) || strings.HasPrefix(wantSHA, imageSHA)
}

func lookTaskset() (string, error) { return exec.LookPath("taskset") }

// rotate returns items rotated left by k modulo their length.
func rotate[T any](items []T, k int) []T {
	n := len(items)
	out := make([]T, n)
	for i := range n {
		out[i] = items[(i+k)%n]
	}
	return out
}

func (s *session) facts() error {
	sys := s.env.sys
	o := newObject()
	o.set("t", "facts")
	o.set("run", s.plan.Run)
	o.set("mode", s.plan.Mode)
	o.set("profile", s.plan.Profile.Name)
	o.set("rule", s.plan.Profile.Rule)
	o.set("profileSpec", s.plan.Profile)
	o.set("targets", s.plan.Targets)
	o.set("turns", s.plan.Turns)
	o.set("rounds", s.plan.Rounds)
	o.set("node", s.env.node)
	o.set("pod", s.env.pod)
	o.set("commit", s.commit)
	kernel, _ := sys.read("proc/sys/kernel/osrelease")
	o.set("kernel", kernel)
	s.cpuModel, s.cpuFeatures = sys.cpuModel(), sys.cpuFeatures()
	o.set("cpuModel", s.cpuModel)
	o.set("cpuFeatures", strings.Join(s.cpuFeatures, " "))
	online, _ := sys.read("sys/devices/system/cpu/online")
	o.set("cpusOnline", online)
	allowed, err := sys.cpusAllowed()
	if err != nil {
		return err
	}
	o.set("cpusAllowed", formatCPUList(allowed))
	o.set("requestedCpus", s.plan.CPUs)
	// With the static CPU manager policy a Guaranteed pod with integer CPUs gets exactly that many cores; any other
	// policy leaves it on the shared pool, limited only by its CFS quota.
	cpuSet := "shared"
	if len(allowed) == s.plan.CPUs {
		cpuSet = "exclusive"
	}
	o.set("cpuSet", cpuSet)
	o.set("cgroup", sys.cgroupVersion())
	o.set("cpuMax", sys.cpuMax())
	o.set("memoryMax", sys.memoryMax())
	if stat, ok := sys.cpuStat(); ok {
		o.set("cpuStat", stat)
	}
	load, _ := sys.read("proc/loadavg")
	o.set("loadavg", load)
	o.set("helperGo", runtime.Version())
	// How the artifacts arrived: in the benchmark image, or as a bundle copied into a base image.
	o.set("delivery", cmp.Or(s.env.getenv("K8SBENCH_DELIVERY"), deliveryImage))
	if ref := s.env.getenv("K8SBENCH_IMAGE_REF"); ref != "" {
		o.set("image", ref)
	}
	if sum := s.env.getenv("K8SBENCH_BUNDLE_SHA256"); sum != "" {
		o.set("bundleSha256", sum)
	}
	runtimeNode := s.env.nodeRuntime(context.Background())
	if runtimeNode != "" {
		o.set("nodeRuntime", runtimeNode)
	}
	if text, err := os.ReadFile(filepath.Join(s.env.root, "VERSIONS")); err == nil {
		versions := parseVersions(string(text))
		o.set("versions", versions)
		// The artifacts were built and checked against one Node release; another one runs different programs.
		if want := versions.str("node"); want != "" && runtimeNode != want {
			return fmt.Errorf("the artifacts pin Node %s but the pod runs Node %q; use the pinned base image", want, runtimeNode)
		}
	}
	if text, err := os.ReadFile(filepath.Join(s.env.root, "ARTIFACTS.sha256")); err == nil {
		o.set("artifacts", parseVersions(strings.ReplaceAll(string(text), "  ", "=")))
	}
	o.set("at", s.env.now().UTC().Format(time.RFC3339))
	return s.emit(o)
}

// parseVersions reads key=value lines; ARTIFACTS.sha256 lines ("<sha256>  <path>") arrive rewritten as sha256=path
// and are flipped to path=sha256.
func parseVersions(text string) *object {
	o := newObject()
	for line := range strings.Lines(text) {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		if len(k) == 64 && !strings.ContainsAny(k, "/ ") && strings.Contains(v, "/") {
			k, v = v, k
		}
		o.set(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	return o
}

func (s *session) choosePin(ctx context.Context) error {
	o := newObject()
	o.set("t", "pin")
	sys := s.env.sys
	allowed, err := sys.cpusAllowed()
	if err != nil {
		return err
	}
	method := s.plan.Pin
	switch {
	case s.plan.Pin == "none":
		s.pin = nil
	case s.plan.Pin != "auto":
		s.pin, err = parseCPUList(s.plan.Pin)
		if err != nil {
			return err
		}
		for _, c := range s.pin {
			if !slices.Contains(allowed, c) {
				return fmt.Errorf("pin list %s names CPU %d, which the pod may not use (allowed %s)", s.plan.Pin, c, formatCPUList(allowed))
			}
		}
		method = "list"
	case len(allowed) <= s.plan.CPUs:
		s.pin = allowed
		method = "all-allowed"
	default:
		before, err := sys.idleTicks()
		if err != nil {
			return err
		}
		if err := s.env.pinWait(ctx); err != nil {
			return err
		}
		after, err := sys.idleTicks()
		if err != nil {
			return err
		}
		var idle map[int]float64
		s.pin, idle = quietest(allowed, before, after, s.plan.CPUs)
		method = "quietest"
		shares := newObject()
		for _, c := range s.pin {
			shares.set(strconv.Itoa(c), roundTo(idle[c], 3))
		}
		o.set("idle", shares)
	}
	o.set("method", method)
	o.set("cpus", formatCPUList(s.pin))
	if len(s.pin) > 0 {
		s.taskset, err = lookTaskset()
		if err != nil {
			return fmt.Errorf("pinning needs taskset (util-linux): %w", err)
		}
	}
	// The measured cores: the pinned ones, else every allowed core the processes may run on.
	measured := s.pin
	if len(measured) == 0 {
		measured = allowed
	}
	// Without a pin on shared cores the processes may run on every core of the node, but the pod's CPU limit is
	// plan.CPUs: more Ps than that would only be throttled, and their number would follow the node's size.
	s.gomaxprocs = min(len(measured), s.plan.CPUs)
	o.set("gomaxprocs", s.gomaxprocs)
	governors := newObject()
	var scaling []string
	for _, c := range measured {
		g := sys.governor(c)
		if g != "" {
			governors.set(strconv.Itoa(c), g)
		}
		if !fixedClock(g) {
			scaling = append(scaling, fmt.Sprintf("%d (%s)", c, g))
		}
	}
	o.set("governors", governors)
	o.set("fixedClock", len(scaling) == 0)
	if err := s.emit(o); err != nil {
		return err
	}
	if len(scaling) > 0 && !s.plan.AllowFrequencyScaling {
		return fmt.Errorf("the measured cores scale their clock with load: CPU %s; CPU times would follow the clock, not the work, so this node is refused: run on a node whose cores have a fixed clock (no cpufreq governor, or performance), or pass -allow-frequency-scaling for a run that is reported but not judged", strings.Join(scaling, ", "))
	}
	return nil
}

// clockSampler samples the measured cores' clock while one step runs: every interval, the highest current clock
// among the cores (the busy core runs at it; an idle core's clock says nothing about the step).
type clockSampler struct {
	stop chan struct{}
	done chan struct{}
	mhz  []float64
}

func (s *session) sampleClock(interval time.Duration) *clockSampler {
	cores := s.pin
	if len(cores) == 0 {
		return nil
	}
	if _, ok := s.env.sys.curMHz(cores[0]); !ok {
		return nil
	}
	c := &clockSampler{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		//portlint:allow doubleclose done belongs to this goroutine, which closes it once on return
		defer close(c.done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			top := 0.0
			for _, cpu := range cores {
				if v, ok := s.env.sys.curMHz(cpu); ok {
					top = max(top, v)
				}
			}
			if top > 0 {
				c.mhz = append(c.mhz, top)
			}
			select {
			case <-c.stop:
				return
			case <-t.C:
			}
		}
	}()
	return c
}

// finish stops the sampler and returns the mean, lowest and highest sampled clock.
func (c *clockSampler) finish() (mean, lo, hi float64, ok bool) {
	if c == nil {
		return 0, 0, 0, false
	}
	//portlint:allow doubleclose step calls finish exactly once per sampler, after its process exits
	close(c.stop)
	<-c.done
	if len(c.mhz) == 0 {
		return 0, 0, 0, false
	}
	lo, hi = c.mhz[0], c.mhz[0]
	for _, v := range c.mhz {
		mean += v
		lo, hi = min(lo, v), max(hi, v)
	}
	return mean / float64(len(c.mhz)), lo, hi, true
}

func (s *session) vars(t target, turns, sample int) map[string]string {
	return map[string]string{
		"root":   s.env.root,
		"dir":    filepath.Join(s.env.data, s.plan.Run, "fixtures", fmt.Sprintf("%s-%d", t.Name, turns)),
		"turns":  strconv.Itoa(turns),
		"sample": strconv.Itoa(sample),
	}
}

// step runs one command pinned to the session's cores and returns its stdout. Its stderr goes to a log file under
// the scratch directory; a failure reports the end of it.
func (s *session) step(ctx context.Context, kind string, t target, turns, sample int, argv []string) ([]byte, error) {
	if len(s.pin) > 0 {
		argv = append([]string{s.taskset, "-c", formatCPUList(s.pin)}, argv...)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.plan.StepTimeoutSeconds)*time.Second)
	defer cancel()
	logPath := filepath.Join(s.env.data, s.plan.Run, "logs", fmt.Sprintf("%s-%s-%d-%d.log", kind, t.Name, turns, sample))
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = logFile.Close() }()
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = &limitedWriter{w: &stdout, n: 16 << 20}
	cmd.Stderr = logFile
	cmd.WaitDelay = 10 * time.Second
	if s.gomaxprocs > 0 {
		cmd.Env = append(os.Environ(), "GOMAXPROCS="+strconv.Itoa(s.gomaxprocs))
	}
	before, haveStat := s.env.sys.cpuStat()
	clock := s.sampleClock(50 * time.Millisecond)
	started := s.env.now()
	runErr := cmd.Run()
	wall := s.env.now().Sub(started)
	mhz, mhzLo, mhzHi, haveClock := clock.finish()
	after, _ := s.env.sys.cpuStat()
	line := newObject()
	line.set("t", "cpu")
	line.set("step", kind)
	line.set("target", t.Name)
	line.set("turns", turns)
	line.set("sample", sample)
	line.set("wallMs", roundTo(float64(wall.Microseconds())/1000, 3))
	if haveStat {
		d := after.minus(before)
		if d.HaveUsage {
			line.set("usageUsec", d.UsageUsec)
		}
		line.set("nrPeriods", d.NrPeriods)
		line.set("nrThrottled", d.NrThrottled)
		line.set("throttledUsec", d.ThrottledUsec)
	}
	if haveClock {
		line.set("mhz", roundTo(mhz, 1))
		line.set("mhzMin", roundTo(mhzLo, 1))
		line.set("mhzMax", roundTo(mhzHi, 1))
	}
	if err := s.emit(line); err != nil {
		return nil, err
	}
	if runErr != nil {
		return nil, fmt.Errorf("%s %s at %d turns (sample %d): %w; stderr ends: %s", kind, t.Name, turns, sample, runErr, tail(logPath, 2048))
	}
	return stdout.Bytes(), nil
}

type limitedWriter struct {
	w io.Writer
	n int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.n {
		return 0, errors.New("command output exceeds 16 MiB")
	}
	l.n -= int64(len(p))
	return l.w.Write(p)
}

func tail(path string, n int64) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if int64(len(data)) > n {
		data = data[int64(len(data))-n:]
	}
	return strings.TrimSpace(strings.ReplaceAll(string(data), "\r", "\n"))
}

func (s *session) seed(ctx context.Context, t target, turns int) error {
	vars := s.vars(t, turns, 0)
	if err := os.MkdirAll(vars["dir"], 0o755); err != nil {
		return err
	}
	if _, err := s.step(ctx, "seed", t, turns, 0, expand(t.Seed, vars)); err != nil {
		return err
	}
	metaPath := expand([]string{t.SeedMeta}, vars)[0]
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("seed %s at %d turns wrote no %s: %w", t.Name, turns, metaPath, err)
	}
	var meta object
	if err := json.Unmarshal(data, &meta); err != nil {
		return fmt.Errorf("seed %s at %d turns: %s: %w", t.Name, turns, metaPath, err)
	}
	o := s.retag("seed", t, &meta)
	o.set("from", "empty")
	return s.emit(o)
}

// retag copies a workload's line under the profile's target name, keeping the workload's own name as bench_target
// and, for the pig target, giving the image commit as version.
func (s *session) retag(kind string, t target, line *object) *object {
	o := newObject()
	o.set("t", kind)
	o.set("target", t.Name)
	if line.has("target") {
		o.set("bench_target", line.str("target"))
	}
	for _, k := range line.keys {
		switch k {
		case "t", "target":
			continue
		case "version":
			if t.Role == rolePig {
				o.set("version", s.commit)
				continue
			}
		}
		o.setRaw(k, line.values[k])
	}
	if t.Role == rolePig && !o.has("version") {
		o.set("version", s.commit)
	}
	// The node type and GOMAXPROCS the process ran under: Go programs' CPU time follows both.
	o.set("gomaxprocs", s.gomaxprocs)
	o.set("cpuModel", s.cpuModel)
	o.set("cpuFeatures", strings.Join(s.cpuFeatures, " "))
	return o
}

func (s *session) sample(ctx context.Context, t target, turns, round int) error {
	vars := s.vars(t, turns, round)
	if err := os.MkdirAll(vars["dir"], 0o755); err != nil {
		return err
	}
	stdout, err := s.step(ctx, "sample", t, turns, round, expand(t.Sample, vars))
	if err != nil {
		return err
	}
	line, err := resultLine(stdout)
	if err != nil {
		return fmt.Errorf("sample %s at %d turns (round %d): %w", t.Name, turns, round, err)
	}
	return s.emit(s.retag("sample", t, line))
}

// resultLine finds the last stdout line that is a durable-bench result: a JSON object with open and turn[].
func resultLine(stdout []byte) (*object, error) {
	var found *object
	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(text, "{") {
			continue
		}
		var o object
		if json.Unmarshal([]byte(text), &o) != nil {
			continue
		}
		var turns []float64
		var open float64
		if o.get("turn", &turns) && o.get("open", &open) && len(turns) > 0 {
			found = &o
		}
	}
	if found == nil {
		return nil, errors.New("no result line (a JSON object with open and turn[]) on stdout")
	}
	return found, nil
}
