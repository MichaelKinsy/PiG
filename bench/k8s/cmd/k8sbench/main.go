// Command k8sbench runs PiG benchmarks on Kubernetes: one Job per session, PiG and the reference interleaved in the
// same pod on the same pinned cores, results in durable-report's tracker format. See
// docs/site/docs/benchmarking.md.
//
//	k8sbench render    [flags]          print the Job manifest; touches no cluster
//	k8sbench calibrate [flags]          the reference against itself; CV of every metric
//	k8sbench compare   [flags]          PiG and the reference interleaved; one tracker line per size
//	k8sbench report    -run DIR [flags] recompute a collected session's result offline
//	k8sbench pod                        the in-pod session (reads K8SBENCH_PLAN)
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// errNoisy is returned when the noise floor is above the threshold; the command exits 3.
var errNoisy = errors.New("noise floor above threshold")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv)
	stop()
	os.Exit(code)
}

const usage = `usage: k8sbench <command> [flags]

  render      print the Job manifest for a session (dry run; no cluster access)
  calibrate   run the reference against itself N times; report each metric's coefficient of variation
  compare     run PiG and the reference interleaved in one pod; write one tracker line per size
  report      recompute a collected session (-run DIR) offline
  bundle      build the artifact bundle for -bundle (no image, no registry)
  pod         run a session inside the benchmark pod (reads K8SBENCH_PLAN)

Run "k8sbench <command> -h" for the flags. Cluster values come from flags, K8SBENCH_* variables,
and a values file (-values, K8SBENCH_VALUES, or bench/k8s/values.local.yaml).
`

func run(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "render", "calibrate", "compare":
		err = cmdSession(ctx, args[0], args[1:], stdout, stderr, getenv)
	case "report":
		err = cmdReport(args[1:], stdout, stderr)
	case "bundle":
		err = cmdBundle(ctx, args[1:], stderr, getenv)
	case "pod":
		err = cmdPod(ctx, stdout, getenv)
	case "-h", "-help", "--help", "help":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	default:
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, errNoisy):
		_, _ = fmt.Fprintln(stderr, "k8sbench:", err)
		return 3
	default:
		_, _ = fmt.Fprintln(stderr, "k8sbench:", err)
		return 1
	}
}

// analysisFlags are the flags that decide how a collected session is judged.
type analysisFlags struct {
	baseline    *string
	history     *string
	track       *string
	calibration *string
	maxCV       *float64
	gate        *string
}

func addAnalysisFlags(fs *flag.FlagSet) analysisFlags {
	return analysisFlags{
		baseline:    fs.String("baseline", "", "JSON objects of tracker values to report deltas against (track-row's BASELINE), comma-separated; with several pig targets each names its line's \"rule\""),
		history:     fs.String("history", "", "comma-separated tracker files for the rolling reference median (default: the -track file)"),
		track:       fs.String("track", "", "tracker file the comparison line is appended to (default <out>/track.jsonl)"),
		calibration: fs.String("calibration", "", "calibration.json from k8sbench calibrate; refuse when its noise floor exceeds -max-cv"),
		maxCV:       fs.Float64("max-cv", 0.05, "largest accepted coefficient of variation of each gated per-sample metric of the reference"),
		gate:        fs.String("gate", gateAuto, "gated metrics: auto (wall and CPU times on exclusive cores, CPU times and counts on shared ones), wall, or cpu"),
	}
}

func cmdSession(ctx context.Context, mode string, args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet(mode, flag.ContinueOnError)
	fs.SetOutput(stderr)
	valuesPath := fs.String("values", "", "values file (default $K8SBENCH_VALUES, else "+defaultValuesFile+" when present)")
	applyFlags := valueFlags(fs)
	profileName := fs.String("profile", "durable-native", "built-in profile name or profile JSON file")
	turnsText := fs.String("turns", "3500", "comma-separated history sizes")
	rounds := fs.Int("rounds", 12, "measured rounds (compare) or runs (calibrate) per size")
	fs.IntVar(rounds, "runs", 12, "same as -rounds")
	pin := fs.String("pin", "auto", "cores to pin every measured process to: auto, none, or a CPU list")
	allowScaling := fs.Bool("allow-frequency-scaling", false, "run on cores whose clock scales with load (a cpufreq governor other than performance); the session is reported but never judged")
	commit := fs.String("commit", "", "refuse unless the image was built from this commit")
	stepTimeout := fs.Duration("step-timeout", time.Hour, "timeout of one seed or sample process")
	out := fs.String("out", "bench/k8s/out", "directory for run directories and the tracker file")
	keep := fs.Bool("keep", false, "keep the Job after collecting it")
	poll := fs.Duration("poll", 15*time.Second, "Job status poll interval")
	dryRun := fs.Bool("dry-run", false, "print the manifest and the kubectl commands; touch no cluster")
	kubectlBin := fs.String("kubectl", "kubectl", "kubectl binary")
	analysis := addAnalysisFlags(fs)
	modeFlag := fs.String("mode", modeCompare, "render only: the session mode to render (compare or calibrate)")
	pickNode := fs.Bool("pick-node", false, "run on the node with the most free CPU (allocatable minus requests), among the -nodes candidates when given")
	spreadFlag := fs.Int("spread", 0, "run on the N nodes with the most free CPU at once, among the -nodes candidates when given, and report the median over them")
	nodesFlag := fs.String("nodes", "", "run on these nodes at once (comma-separated) and report the median over them; with -spread or -pick-node, the candidates to pick from")
	if err := fs.Parse(args); err != nil {
		return err
	}
	now := time.Now
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	sessionMode := mode
	if mode == "render" {
		sessionMode = *modeFlag
	}
	// Refuse settings whose session the noise gate would refuse anyway, before it occupies a node for hours.
	if err := checkMaxCV(*analysis.maxCV); err != nil {
		return err
	}
	if _, err := gatesFor(*analysis.gate, "shared"); err != nil {
		return err
	}
	if *spreadFlag < 0 {
		return fmt.Errorf("-spread %d: want a node count", *spreadFlag)
	}
	if *rounds < minNoiseSamples {
		return fmt.Errorf("-rounds %d: the noise gate needs at least %d samples of the reference per size", *rounds, minNoiseSamples)
	}
	v, err := resolveValues(*valuesPath, applyFlags, getenv)
	if err != nil {
		return fmt.Errorf("configuration:\n  %w", err)
	}
	prof, err := loadProfile(*profileName)
	if err != nil {
		return err
	}
	turns, err := parseTurns(*turnsText)
	if err != nil {
		return err
	}
	// In bundle mode the bundle is checked and packed first: its sha256 goes into the Job, and its commit names the run.
	var bundle *bundleInfo
	var bundleTar, bundleSum string
	var bundleSize int64
	if v.delivery() == deliveryBundle {
		if bundle, err = inspectBundle(v.Bundle); err != nil {
			return err
		}
		if *commit != "" && !sameCommit(bundle.commit, *commit) {
			return fmt.Errorf("the bundle was built from %s, not the requested commit %s", bundle.commit, *commit)
		}
		if err := checkBundlePrograms(prof, sessionTargets(prof, sessionMode), bundle); err != nil {
			return err
		}
		tmp, err := os.MkdirTemp("", "k8sbench-bundle-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		bundleTar = filepath.Join(tmp, "bundle.tar.gz")
		if bundleSum, bundleSize, err = packBundle(bundle.dir, bundleTar); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stderr, "k8sbench: bundle %s: %s (linux/%s), %.1f MB, sha256 %s\n", bundle.dir, bundle.commit, bundle.arch, float64(bundleSize)/1e6, bundleSum)
	}
	runCommit := *commit
	if runCommit == "" && bundle != nil {
		runCommit, _, _ = strings.Cut(bundle.commit, "-")
	}
	pl := &plan{
		Mode: sessionMode, Profile: prof, Targets: sessionTargets(prof, sessionMode), Turns: turns, Rounds: *rounds,
		CPUs: v.CPUs, Pin: *pin, Commit: *commit, StepTimeoutSeconds: int(stepTimeout.Seconds()), AllowFrequencyScaling: *allowScaling,
	}
	baseRun := runID(sessionMode, runCommit, now())
	ref := bundleRef{sha256: bundleSum}
	if bundle != nil {
		ref.arch = bundle.arch
	}
	k := execKubectl{bin: *kubectlBin, kubeconfig: v.Kubeconfig, context: v.KubeContext, namespace: v.Namespace}
	dry := mode == "render" || *dryRun

	// Nodes: one named node (-node), several (-nodes), or the ones with the most free CPU (-pick-node, -spread N),
	// among the -nodes candidates when both are given.
	var nodes []string
	var ranking *nodeRanking
	if *nodesFlag != "" {
		for n := range strings.SplitSeq(*nodesFlag, ",") {
			if n = strings.TrimSpace(n); n != "" {
				nodes = append(nodes, n)
			}
		}
	}
	want := *spreadFlag
	if *pickNode && want < 1 {
		want = 1
	}
	switch {
	case v.Node != "" && (len(nodes) > 0 || want > 0):
		return errors.New("-node names the node; it does not combine with -nodes, -pick-node or -spread")
	case len(nodes) > 0 && want > len(nodes):
		return fmt.Errorf("-spread %d: -nodes names only %d candidates", want, len(nodes))
	}
	for i, n := range nodes {
		if !dnsSubdomain.MatchString(n) {
			return fmt.Errorf("node %q is not a node name", n)
		}
		// Two Jobs on one node would measure each other, and the median would count that node twice.
		if slices.Contains(nodes[:i], n) {
			return fmt.Errorf("-nodes names %s twice", n)
		}
	}
	switch {
	case want > 0 && !dry:
		picked, r, err := pickNodes(ctx, k, v, want, nodes, stderr)
		if err != nil {
			return err
		}
		nodes, ranking = picked, &r
	case want > 0 && len(nodes) > 0:
		_, _ = fmt.Fprintf(stderr, "dry run: the run picks %d of the -nodes candidates by free CPU; the manifests below use the first %d\n", want, want)
		nodes = nodes[:want]
	case want > 0:
		_, _ = fmt.Fprintf(stderr, "dry run: the run picks %d node(s) by free CPU (kubectl get nodes, get pods); the manifest below has no node yet\n", want)
	}
	// A calibration that cannot vouch for this comparison is refused now, not after the session has held its nodes for
	// hours; the analysis checks the same again.
	if sessionMode == modeCompare && *analysis.calibration != "" {
		pinned := nodes
		if len(pinned) == 0 && v.Node != "" {
			pinned = []string{v.Node}
		}
		calib, problems, err := readCalibration(*analysis.calibration, prof, turns, pinned)
		if err != nil {
			return err
		}
		if len(pinned) == 0 && want == 0 {
			var calibNodes []string
			calib.get("nodes", &calibNodes)
			return fmt.Errorf("-calibration measured nodes %s: pin the session to calibrated nodes with -node or -nodes; the scheduler may place it on another node", strings.Join(calibNodes, ", "))
		}
		if len(problems) > 0 {
			return fmt.Errorf("-calibration cannot vouch for this comparison:\n  %s", strings.Join(problems, "\n  "))
		}
	}

	// One Job per node; a single Job keeps the run's own name, a spread names each Job after its node's index.
	var jobs []sessionJob
	if len(nodes) <= 1 {
		j := sessionJob{v: v, pl: pl}
		j.pl.Run = baseRun
		if len(nodes) == 1 {
			j.v.Node = nodes[0]
		}
		jobs = append(jobs, j)
	} else {
		for i, n := range nodes {
			p := *pl
			p.Run = fmt.Sprintf("%s-n%d", baseRun, i+1)
			j := sessionJob{v: v, pl: &p}
			j.v.Node = n
			jobs = append(jobs, j)
		}
	}
	for i := range jobs {
		if jobs[i].manifest, err = renderJob(jobs[i].v, jobs[i].pl, ref); err != nil {
			return err
		}
	}
	if dry {
		for i, j := range jobs {
			if i > 0 {
				_, _ = io.WriteString(stdout, "---\n")
			}
			if _, err := stdout.Write(j.manifest); err != nil {
				return err
			}
		}
		if *dryRun {
			kc := *kubectlBin + " " + strings.Join(k.args(nil), " ")
			_, _ = fmt.Fprintf(stderr, "dry run; would run, per Job:\n  %s apply -f -\n  %s get job <job> -o json   (every %s)\n", kc, kc, *poll)
			if bundle != nil {
				_, _ = fmt.Fprintf(stderr, "  %s cp <bundle.tar.gz> <pod>:%s/bundle.tar.gz -c bench   (once the pod runs)\n  %s cp <empty file> <pod>:%s/READY -c bench\n", kc, bundleDir, kc, bundleDir)
			}
			_, _ = fmt.Fprintf(stderr, "  %s logs pod/<pod> -c bench\n", kc)
		}
		return nil
	}

	runDir, err := filepath.Abs(filepath.Join(*out, baseRun))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}
	if ranking != nil {
		if err := writeJSON(filepath.Join(runDir, "nodes.json"), ranking); err != nil {
			return err
		}
	}
	if len(jobs) > 1 {
		var names []string
		for _, j := range jobs {
			names = append(names, j.pl.Run)
		}
		if err := writeJSON(filepath.Join(runDir, spreadFile), map[string]any{"runs": names, "nodes": nodes}); err != nil {
			return err
		}
	}
	progress := &lockedWriter{w: stderr}
	timeout := time.Duration(v.DeadlineSeconds)*time.Second + 10*time.Minute
	errs := make([]error, len(jobs))
	// A spread session reports only the median over all its nodes, so the first failed Job stops the others; runJob
	// deletes each stopped Job.
	sessions, stopSessions := context.WithCancel(ctx)
	defer stopSessions()
	var wg sync.WaitGroup
	for i, j := range jobs {
		dir := runDir
		if len(jobs) > 1 {
			dir = filepath.Join(runDir, j.pl.Run)
		}
		wg.Go(func() {
			err := runSession(sessions, k, j, dir, sessionMode, bundle, bundleTar, bundleSum, bundleSize, *poll, timeout, *keep, progress, now)
			switch {
			case err == nil:
			case ctx.Err() == nil && sessions.Err() != nil:
				err = fmt.Errorf("job %s stopped because another node's Job failed", j.pl.Run)
			default:
				stopSessions()
			}
			errs[i] = err
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}
	return analyze(runDir, *out, analysis, stdout, now())
}

// sessionJob is one Job of a session: its values (with its node), plan and manifest.
type sessionJob struct {
	v        values
	pl       *plan
	manifest []byte
}

// lockedWriter serialises progress lines from the Jobs of a spread session.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// runSession submits one Job, waits for it, copies the bundle in when there is one, and collects the pod log and
// the cluster's node facts into dir.
func runSession(ctx context.Context, k kubectl, j sessionJob, dir, mode string, bundle *bundleInfo, bundleTar, bundleSum string, bundleSize int64, poll, timeout time.Duration, keep bool, progress io.Writer, now func() time.Time) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), j.manifest, 0o644); err != nil {
		return err
	}
	where := "any node"
	if j.v.Node != "" {
		where = "node " + j.v.Node
	}
	_, _ = fmt.Fprintf(progress, "k8sbench: %s: job %s in namespace %s on %s; output in %s\n", mode, j.pl.Run, j.v.Namespace, where, dir)
	var deliver func(context.Context, string) error
	if bundle != nil {
		deliver = func(ctx context.Context, pod string) error { return copyBundle(ctx, k, pod, bundleTar, progress, now) }
	}
	outcome, err := runJob(ctx, k, j.manifest, j.pl.Run, poll, timeout, keep, progress, now, deliver)
	if len(outcome.log) > 0 {
		if werr := os.WriteFile(filepath.Join(dir, "pod.log"), outcome.log, 0o644); werr != nil && err == nil {
			err = werr
		}
	}
	if err != nil {
		return err
	}
	cluster := clusterFacts(ctx, k, outcome.node)
	cluster.set("image", j.v.podImage())
	cluster.set("delivery", j.v.delivery())
	if bundle != nil {
		cluster.set("bundleSha256", bundleSum)
		cluster.set("bundleBytes", bundleSize)
		cluster.set("bundleCommit", bundle.commit)
		artifacts := newObject()
		for _, a := range bundle.artifacts {
			artifacts.set(a[0], a[1])
		}
		cluster.set("bundleArtifacts", artifacts)
	}
	if err := writeJSON(filepath.Join(dir, "cluster.json"), cluster); err != nil {
		return err
	}
	if !outcome.succeeded {
		if log, perr := parsePodLog(bytes.NewReader(outcome.log)); perr == nil && len(log.errors) > 0 {
			return fmt.Errorf("job %s failed: %s; pod log in %s", j.pl.Run, strings.Join(log.errors, "; "), filepath.Join(dir, "pod.log"))
		}
		return fmt.Errorf("job %s failed (%s); pod log in %s", j.pl.Run, outcome.reason, filepath.Join(dir, "pod.log"))
	}
	return nil
}

func parseTurns(text string) ([]int, error) {
	var out []int
	for part := range strings.SplitSeq(text, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("-turns %q: want positive turn counts separated by commas", text)
		}
		out = append(out, n)
	}
	return out, nil
}

func cmdReport(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	runDir := fs.String("run", "", "run directory holding pod.log")
	out := fs.String("out", "", "directory of the default tracker file (default: the run directory's parent)")
	analysis := addAnalysisFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runDir == "" {
		return errors.New("report needs -run DIR")
	}
	now := time.Now
	dir, err := filepath.Abs(*runDir)
	if err != nil {
		return err
	}
	if *out == "" {
		*out = filepath.Dir(dir)
	}
	return analyze(dir, *out, analysis, stdout, now())
}

func cmdPod(ctx context.Context, stdout io.Writer, getenv func(string) string) error {
	var pl plan
	if err := json.Unmarshal([]byte(getenv("K8SBENCH_PLAN")), &pl); err != nil {
		return fmt.Errorf("K8SBENCH_PLAN: %w", err)
	}
	err := runPod(ctx, &pl, podEnvFromOS(), stdout)
	if err != nil {
		o := newObject()
		o.set("t", "error")
		o.set("message", err.Error())
		line, _ := o.MarshalJSON()
		_, _ = fmt.Fprintln(stdout, string(line))
	}
	return err
}

// writeJSON writes value indented, without HTML escaping.
func writeJSON(path string, value any) error {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}
