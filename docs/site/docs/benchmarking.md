# Running PiG benchmarks on Kubernetes

`bench/k8s` runs PiG's Durable benchmarks on a Kubernetes cluster instead of a shared host. Each session is one Job with one pod. The pod measures PiG and the reference implementation, pi-durable, in the same pod, one process at a time, pinned to the same cores. Both targets therefore run beside the same neighbours. The runner writes durable-report's tracker format (`bench/durable/report`), so the tracker's rolling history and baseline deltas read its output unchanged.

The runner is generic. It needs `kubectl`, a namespace, and either a benchmark image or a local artifact bundle. Cluster-specific values come only from flags, environment variables, or a private values file.

## Requirements

- `kubectl` on the machine that runs the runner, with credentials for the cluster.
- A namespace in which your account can create and delete Jobs, and get and list pods and pod logs. Bundle mode also copies into the pod, which needs `pods/exec`. Node picking (`-pick-node`, `-spread`) works best with a cluster-wide read permission, and falls back to what the namespace can read; see [Choose nodes](#choose-nodes).
- Image mode: a container registry that the nodes can pull from, and Docker with Buildx on the machine that builds the image.
- Bundle mode: no registry. The nodes pull the official Node image from Docker Hub. The machine that builds the bundle needs Go, TinyGo and npm, or Docker with Buildx.

This Role grants the namespace permissions. An administrator binds it to your account:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: k8sbench
  namespace: <namespace>
rules:
  - apiGroups: ["batch"]
    resources: ["jobs"]
    verbs: ["create", "get", "patch", "delete"]
  - apiGroups: [""]
    resources: ["pods", "pods/log"]
    verbs: ["get", "list"]
  - apiGroups: [""]
    resources: ["pods/exec"]
    verbs: ["create"]
```

`kubectl apply` needs `get` and `patch` on Jobs in addition to `create`. `kubectl cp`, which bundle mode uses, needs `create` on `pods/exec`. Image mode does not need the `pods/exec` rule.

These cluster properties improve the measurement:

| property | effect | required |
|---|---|---|
| Guaranteed QoS | The runner always requests whole CPUs and sets requests equal to limits for CPU and memory. | Yes. The runner sets it. |
| Static CPU manager policy | A Guaranteed pod with whole CPUs gets exclusive cores. No other pod runs on them. | No, but recommended. |
| Dedicated node pool | The benchmark node runs nothing else. Select it with `nodeSelector` and `tolerations`. | No, but recommended. |
| Fixed CPU clock | The kernel does not scale the measured cores' clock with load: the cores have no cpufreq governor, or the `performance` governor. | Yes. The session refuses other governors. |
| One node type | Every node of a comparison has the same CPU model and the same vector features (for example AVX-512). | Yes for a spread run. The analysis refuses a median over node types. |

Without the static policy, the pod shares the node's cores and its CPU limit is only a CFS quota. The pod then reads, for example, `Cpus_allowed 0-63` and `cpu.max 200000 100000`. The session handles this case. It samples `/proc/stat` for one second, pins every measured process with `taskset` to the quietest allowed cores (as many as the pod's CPUs), and records CFS throttling from the cgroup's `cpu.stat` around every process. Use calibration to decide whether the noise on such a node is acceptable.

Go programs choose code by CPU and by processor count. The Go runtime's Green Tea garbage collector scans with AVX-512 where the CPU has it, and a Go runtime sizes its scheduler from `GOMAXPROCS`, which it otherwise derives from the node's CPUs or the cgroup's CPU quota. The same Go benchmark therefore spends a different CPU time on another node type, or under another quota. The session sets `GOMAXPROCS` explicitly for every process it runs, whatever the pod's environment says: to the number of measured cores (the pinned cores, or every allowed core without a pin), at most the pod's CPUs. Without a pin on a node without the static policy, the allowed cores are every core of the node, but the pod's CPU limit is still its CPUs. The facts line records the CPU model and its vector features (`cpuFeatures`: of `avx`, `avx2`, `avx512*`, `gfni`, `bmi2`, `popcnt`, `asimd`, `sve`, `sve2`, those the first core lists, with the kernel's names such as `avx512_bitalg`), the pin line records `gomaxprocs`, and every seed and sample line carries all three. The analysis refuses a pod whose samples name two node types, and a spread run whose nodes differ in any of them. Pin one node type, for example with `-node`, `-nodes` or a `nodeSelector` on a node-type label.

The measured cores must keep a fixed clock. A cpufreq governor such as `schedutil`, `ondemand` or `powersave` moves the clock with load, and a CPU time measured in nanoseconds moves with it: the same work then takes a different CPU time from one process to the next. The session reads the governor of each measured core (the pinned cores, or every allowed core without a pin) before it measures anything, and refuses such a node with the cores and governors in the error. A governor file that exists but cannot be read counts as `unreadable`, and the session refuses it the same way. A spread session then stops on all nodes. Choose nodes whose cores have no governor or `performance`; the runner does not change kernel settings. Neither setting stops the hardware's own boost (turbo) from moving the clock. With `performance` the cpu lines record the clock (see below); without a cpufreq driver the clock is not visible, and calibration is the evidence. `-allow-frequency-scaling` runs such a node anyway, for example on a laptop or a test cluster: the analysis reports the session but never judges it, so a calibration is not judged and a comparison writes no tracker line.

While each process runs, the session samples the measured cores' current clock (`scaling_cur_freq`) every 50 ms when cpufreq reports it, and records the mean, lowest and highest of the highest core's clock in the cpu line (`mhz`, `mhzMin`, `mhzMax`). Cores without cpufreq report no current clock, so their cpu lines have no `mhz`. Each calibration node entry has a `clock` object: the governors, the mean clock, its CV, and the correlation of each run's CPU time with its clock. A strongly negative correlation means the clock, not the work, moved the CPU time.

## Build the image

The image holds PiG's Durable builds (the TinyGo and Go Wasm cores and the native Go Durable benchmark), Node, pi-durable at PiG's pinned Pi version from the lockfile in `bench/k8s/image/pi-durable`, and the Pi reference checkout at the commit in `durable/contract/PIN`. A unit test fails when the lockfile's version differs from the pinned Pi version. Base images are pinned by digest. Downloads are pinned by version and SHA-256.

```sh
IMAGE=<registry>/<repository> bench/k8s/image/build.sh
```

The script builds the current commit for `linux/amd64`, pushes it, and prints the digest-pinned reference, for example `<registry>/<repository>@sha256:<digest>`. Use that reference as the image. The script refuses a tree with uncommitted or untracked files unless `K8SBENCH_ALLOW_DIRTY=1` is set. The image then records the commit with a `-dirty` suffix.

| variable | meaning |
|---|---|
| `PLATFORM` | The node architecture, `linux/amd64` (default) or `linux/arm64`. |
| `PUSH=0` | Build into the local image store and do not push. |
| `PI_REFERENCE=0` | Leave out the Pi reference checkout. The built-in profile does not use it. |

The image records its commit in `/opt/pig/COMMIT`, tool versions in `/opt/pig/VERSIONS`, and the SHA-256 of every binary and Wasm module in `/opt/pig/ARTIFACTS.sha256`. Every session copies these into its results.

## Run without a registry (bundle mode)

In bundle mode no benchmark image is built or pushed. The pod runs the official Node image, pinned by digest (`node:24.19.0-bookworm-slim@sha256:...`; `-base-image` or `baseImage` replaces it). The runner copies a prebuilt artifact bundle into the pod:

1. The runner checks the bundle directory. Every file in `ARTIFACTS.sha256` must have its listed SHA-256. The list must include the bench harness (`bin/k8sbench`, `bin/durableperf`), the Durable builds (`bin/durableperf` is the native one; `dist/core-go.wasm` and `dist/core-tinygo.wasm` are the Wasm cores), and the pi-durable reference bundle (`pi-durable/bench.mjs`, `pi-durable/package-lock.json` and its installed `node_modules`). The binaries must be Linux ELF executables of one architecture.
2. The runner packs the directory into one `bundle.tar.gz` and writes its SHA-256 into the Job.
3. The pod starts a small shell script. It checks that the node has the bundle's architecture, and then waits up to 30 minutes for the bundle.
4. When the pod runs, the runner copies `bundle.tar.gz` into the pod's `/bundle` emptyDir with `kubectl cp`, and then an empty `READY` file.
5. The pod checks the bundle's SHA-256 against the Job's, unpacks it, and checks every artifact against `ARTIFACTS.sha256`. Any mismatch ends the session with an error, and the runner reports the pod's message. Then the session starts as in image mode.

Check the copy permission before the first run: `kubectl auth can-i create pods/exec -n <namespace>` must answer `yes`.

The session records the delivery mode, the pod image, the bundle's SHA-256 and every artifact's SHA-256 in `ENV.txt`, `cluster.json` and `calibration.json`.

Build the bundle on the machine that runs the runner:

```sh
go run ./bench/k8s/cmd/k8sbench bundle -out <dir>               # Go, TinyGo and npm on this machine; -arch arm64 for arm64 nodes
BUNDLE=<dir> bench/k8s/image/build.sh                           # or in Docker, from the image's build stage
```

Both refuse a tree with uncommitted or untracked files (`-allow-dirty`, `K8SBENCH_ALLOW_DIRTY=1`). `k8sbench bundle` finds TinyGo through `-tinygo`, else `$TINYGO`, else `PATH`, and stops before building anything when it finds none. It cross-compiles the Go binaries for Linux and installs the reference packages for Linux with `npm ci --os=linux --cpu=<arch>`. Then point the runner at the directory with `-bundle <dir>` or `K8SBENCH_BUNDLE`, and leave the image unset. The runner refuses a session that sets both. `-commit` refuses a bundle built from another commit before anything is submitted.

## Point the runner at a cluster

The runner reads its configuration in this order. A later source overrides an earlier one.

1. Defaults: 2 CPUs, 8 GiB of memory, a 6-hour deadline.
2. A values file: `-values FILE`, else `$K8SBENCH_VALUES`, else `bench/k8s/values.local.yaml` when it exists.
3. Environment variables.
4. Flags.

Copy `bench/k8s/values.example.yaml` to `bench/k8s/values.local.yaml` and fill in your values. Git ignores `values.local.yaml`, and the image build leaves it out. Do not commit cluster names, namespaces, node names, or kubeconfig paths.

| setting | flag | environment variable |
|---|---|---|
| kubeconfig | `-kubeconfig` | `KUBECONFIG` (read by kubectl) |
| kubectl context | `-context` | `K8SBENCH_CONTEXT` |
| namespace | `-namespace` | `K8SBENCH_NAMESPACE` |
| image | `-image` | `K8SBENCH_IMAGE` |
| bundle directory (bundle mode) | `-bundle` | `K8SBENCH_BUNDLE` |
| base image (bundle mode) | `-base-image` | `K8SBENCH_BASE_IMAGE` |
| CPUs | `-cpus` | `K8SBENCH_CPUS` |
| memory | `-memory` | `K8SBENCH_MEMORY` |
| node name | `-node` | `K8SBENCH_NODE` |
| node selector | `-node-selector key=value,...` | none |
| results volume (`/results/<run>/pod.jsonl`) | `-pvc` | `K8SBENCH_PVC` |
| Job deadline in seconds | `-deadline` | none |

`tolerations`, `imagePullSecrets`, `serviceAccountName`, `scratchSize` and `ttlSecondsAfterFinished` come only from the values file.

The runner refuses an image or a base image that is not pinned by digest. Both images run Node 24.19.0, which the bundle records in `VERSIONS` (`node=v24.19.0`); the pod refuses to start under another Node release, or without `node`, because the programs were built and checked on this release and Node's APIs move between releases (Node 26.11.0 renames `node:sqlite`'s `DatabaseSync` and `StatementSync` to `Database` and `Statement` and keeps the old names as deprecated aliases). A base image given with `-base-image` must therefore run the same release. A tracker line must identify the image it ran on. `-allow-unpinned-image` accepts a tag, for example for an image loaded into a local test cluster.

`-node` pins the pod to one node by name. The runner uses a required node affinity on `metadata.name`, so the scheduler still checks taints and resources.

### Choose nodes

Without a node, the scheduler places the pod. These flags choose the nodes instead:

| flag | effect |
|---|---|
| `-node NAME` | One Job on that node. |
| `-pick-node` | One Job on the node with the most free CPU. |
| `-spread N` | One Job on each of the N nodes with the most free CPU, at the same time. |
| `-nodes a,b,c` | One Job on each named node, at the same time. Each node may appear once. |
| `-nodes a,b,c -spread N` | One Job on each of the N candidates with the most free CPU, at the same time. `-pick-node` picks one candidate. |

Free CPU is the node's allocatable CPU minus the CPU requests of the pods bound to it, counted as the scheduler counts them (sidecar containers, init containers, pod-level requests and overhead included). It is not the node's load: a pod that requests little can still use a lot of CPU. The runner considers only nodes that are Ready, not cordoned, match the node selector, have every `NoSchedule` and `NoExecute` taint tolerated, and have room for the pod's CPU and memory. It prints the ranking and writes it to `nodes.json` in the run directory.

Picking reads as much as the account may, and `nodes.json` records the basis of the ranking:

| the account can list | basis | ranking |
|---|---|---|
| nodes, and pods in all namespaces | `cluster` | Free CPU as the scheduler counts it. |
| nodes, and pods in its namespace | `namespace` | Allocatable CPU minus the requests of the namespace's own pods. Other namespaces' pods are invisible, so free CPU is an upper bound and a busy node can rank first. The runner prints a warning. |
| pods in its namespace only | `candidates` | Needs `-nodes`. The candidates rank by the CPU the namespace's own pods request on them, then in the order given. Allocatable CPU, readiness and taints are unknown, so the scheduler checks them when the Job starts. |

Only a Forbidden answer from the API server narrows the basis. Any other failure of a list, such as a lost connection or a timeout, stops the run before it submits a Job.

The full ranking needs this ClusterRole in addition to the Role above:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: k8sbench-pick-node
rules:
  - apiGroups: [""]
    resources: ["nodes", "pods"]
    verbs: ["list"]
```

With namespace-scoped permissions only, choose the candidates yourself, for example from your cluster's monitoring, and pass them with `-nodes`. Add `-spread N` to run on N of them, or pass exactly the nodes to run on:

```sh
go run ./bench/k8s/cmd/k8sbench calibrate -turns 3500 -runs 12 -nodes <node-1>,<node-2>,<node-3> -spread 3
```

A spread session writes one run directory per node under its own run directory, named after it with `-n1`, `-n2` and so on, and `spread.json`, which lists them. The analysis takes, for each value, the median over the nodes. The tracker summary names the nodes. A spread session reports only when every node's Job succeeds, so the first Job that fails stops and deletes the others.

Print the manifest before you submit anything:

```sh
go run ./bench/k8s/cmd/k8sbench render
go run ./bench/k8s/cmd/k8sbench compare -dry-run
```

`render` and `-dry-run` do not contact the cluster.

## Calibrate

Calibration runs pi-durable against itself in one pod and reports the coefficient of variation (CV) of each metric over the runs:

```sh
go run ./bench/k8s/cmd/k8sbench calibrate -turns 3500 -runs 12 -pick-node
```

| metric | per run | gate |
|---|---|---|
| `warm` | wall time: median of turns 2 to 10 | wall |
| `cold` | wall time: open plus the first turn | wall |
| `cpu` | CPU time of the pod's cgroup during the run (`cpu.stat` `usage_usec`) | wall, cpu |
| `cpuWarm` | process CPU time: median of turns 2 to 10, when the workload reports it | wall, cpu |
| `cpuCold` | process CPU time: open plus the first turn, when the workload reports it | wall, cpu |
| `bytes` | the store size after the run, a deterministic count | wall, cpu |
| `peakRss` | peak resident set, MB | none |
| `cpuSeconds` | CPU time of the process | none |

`-gate` selects the gated metrics. `wall` gates wall and CPU times. `cpu` gates CPU times and counts, and reports wall times without gating them. `auto`, the default, uses `wall` when the pod has exclusive cores and `cpu` when it shares them. On shared cores, a busy neighbour can double a run's wall time without changing its CPU time. The CPU time still measures the work, so the wall times are informational there. The tracker summary names the gate. A gated metric that is zero in every sample was not measured, and the gate refuses it. The `cpu` metric needs the cgroup's CPU usage: `usage_usec` in `cpu.stat` (cgroup v2) or `cpuacct.usage` (cgroup v1).

The command prints a table and writes `calibration.json` in the run directory. For each size, the file holds the CV of every metric and, per node, `warmDrift` (the warm median of the second half of the runs divided by that of the first half) and the CFS throttling of the runs.

A calibration judges a size only from 12 runs. With fewer, the command reports the table, marks the size `"judged": false`, and exits with status 0: such a run is a smoke test of the setup. Small sizes are dominated by process start and tiny cold times, so judge the noise at the size you compare, for example 3,500 turns. With 12 runs or more, the command exits with status 3 when a gated CV exceeds `-max-cv` (default 0.05, that is 5%).

`-max-cv` must be a positive, finite number. The noise gate needs at least 3 runs per size, so `calibrate` and `compare` refuse `-runs` or `-rounds` below 3 before they submit a Job.

A calibration measures the noise of the nodes it ran on. `calibration.json` lists them, and `compare` refuses a calibration that does not list every node it ran on. Pin `calibrate` and `compare` to the same nodes with `-node` or `-nodes`. A spread calibration (`-spread N`) reports the median CV over its nodes, and each node's own. It prints each node's gated CVs and warm drift, records each node's verdict in `calibration.json`, and names a node that is noisy on its own even when the median passes. Leave such a node out of `-nodes` for the comparison, or recalibrate it. `compare` judges the calibration by the noise of the nodes it runs on: the median over those nodes' own entries. A quiet median therefore does not vouch for a noisy node.

`compare -calibration` checks the calibration before it submits a Job. It refuses a session that is not pinned to nodes (`-node`, `-nodes`, or picked with `-pick-node` or `-spread`), a node the calibration did not measure, a calibration that names no node, a size that the calibration did not judge from 12 runs, and a node the calibration measured on cores that scale their clock. The analysis checks the same again, together with the CVs.

The tracker calls a difference below 3% a tie, so a noise floor near 5% already limits what a single comparison can show. If calibration fails, use a dedicated node, the static CPU manager policy, or more runs.

## Compare

```sh
go run ./bench/k8s/cmd/k8sbench compare -turns 3500 -rounds 12 \
  -calibration bench/k8s/out/<calibration run>/calibration.json \
  -baseline <baseline.json>
```

The session runs in one pod:

1. It records the node facts: kernel, CPU model, allowed CPUs, `cpu.max`, `memory.max`, `cpu.stat`, load, and the image's versions and artifact hashes.
2. It pins the measured processes to the session's cores.
3. It seeds each target's history from zero, one invocation per size, and times it.
4. It runs `-rounds` rounds. Each round runs every size, and every target within a size. Each round rotates the target order by one, so no target always runs first. Each sample is a fresh process.

The runner then checks that the targets ran the same history. Each seed must reproduce durable-bench's published fingerprint for its size, and all targets' seeds and measured transcripts must agree. Next it checks the noise floor. It computes the CV of the reference's gated per-sample metrics in this run (the median over the nodes of a spread session), and reads the calibration file when one is given. It refuses to report a comparison when either exceeds `-max-cv`, when the calibration has fewer than 12 runs at a size, or when the calibration did not run on this run's nodes. It then writes `noise.json`, writes no tracker line, and exits with status 3.

Otherwise the runner appends one tracker line per size and pig target to `-track` (default `bench/k8s/out/track.jsonl`) and prints it.

The runner also reads what the API server reports about the node, when the account may read it: the Node object (kernel, OS image, kubelet version, CPU capacity) and the kubelet's `configz` (CPU manager policy). A namespace-scoped account usually may read neither. The run then records them as unreadable and infers the CPU manager policy from the pod's cpuset. Exclusive cores mean the static policy.

## Output

Each session writes a run directory under `-out` (default `bench/k8s/out`, ignored by Git):

| file | content |
|---|---|
| `manifest.yaml` | the Job as submitted |
| `pod.log` | the pod's output: one JSON object per line |
| `results.jsonl` | the sample lines, as the workload printed them, with `t: "sample"` and the profile's target name |
| `seeds.jsonl` | one seed line per target and size: `seedMs`, `fingerprint`, `bytes`, `from: "empty"` |
| `ENV.txt` | where the session ran, as text |
| `cluster.json` | the API server's node facts |
| `noise.json` | compare: the noise floor and any refusal reasons |
| `calibration.json` | calibrate: the CV of every metric, and each node's |
| `nodes.json` | `-pick-node`, `-spread`: the node ranking and its basis |
| `spread.json` | `-spread`, `-nodes`: the per-node run directories |

A sample line keeps every field the workload wrote. The workload's own target name moves to `bench_target`, and the PiG target's `version` is the image commit. These are the sample and seed line shapes that durable-report's `assemble.mjs` reads.

`report -run DIR` recomputes a run directory offline with other analysis flags, for example another `-baseline`:

```sh
go run ./bench/k8s/cmd/k8sbench report -run bench/k8s/out/<run> -baseline <baseline.json>
```

`report` appends the recomputed line to the tracker file. The rolling reference median counts each run once, by the name of its run directory: it uses the newest line of each run and never the run being reported.

### Tracker line

The tracker line has the fields and field order of durable-report's `runner/track-row.sh`: `at`, `rule`, `warmTurns`, `core`, `turns`, `fingerprint`, `run`, `baseline`, `values`, `delta`, `compare`, `disagree` and `summary`. The statistics are the same. Warm pools every turn after the first of every sample; cold is open plus the first turn. Both are reported as p50, p95 and max, net of the floor target's p50 when the profile has one. `delta` compares each value with `-baseline`, a JSON object of the same value names and a `core`. With several pig targets, give one baseline file per line, comma-separated, each with the `rule` of its line. `compare` judges each PiG metric against the reference of this run and against the median of the reference over the last five runs of the same rule and size in `-history` (default: the tracker file). A metric is a loss when PiG is more than 3% worse and a win from 1.3x.

`values` holds only the metrics the workload measures. The built-in profile measures latency, seed time and the process CPU time of each turn, so it reports `cpuWarm` and `cpuCold` (and the reference's `piCpuWarm` and `piCpuCold`) as track-row does: the median CPU time of a warm turn over every sample, and the median over the samples of the CPU time of the open plus the first turn, in milliseconds. It also reports `peakRssMB` and `piPeakRssMB`, the median over the samples of each process's peak resident memory, compared like the times (lower wins). Sample lines report memory in MiB, like `rss`; the tracker converts it to track-row's MB (10^6 bytes). A workload whose sample lines carry `retained` (MiB the open store keeps after a forced garbage collection after the last measured turn) gets `retainedMB` and `piRetainedMB`, compared the same way, in track-row's position after `cpuCold`. One whose sample lines carry `growthPerTurn` (bytes the heap grows per measured turn) gets `growthPerTurn` and `piGrowthPerTurn` and a `heap growth/turn` part in the summary, without a verdict: memory bounded by the context window means zero growth, whatever the reference does. Each memory measure appears only when every sample of both targets reports it. A workload that does not report `openCpu` and `turnCpu[]` has no CPU values. The built-in profile has no `retainedMB`, `rowsRead` or other probe values, so those fields are absent rather than zero.

## Profiles

A profile names the targets a session measures and the commands that run them in the image. `-profile` takes a built-in name or a JSON file. The built-in profile is `durable-native`:

| target | role | command |
|---|---|---|
| `pig` | `pig` | `bench/durableperf`: PiG's Go Durable as a native process, with durable-bench's walk model and faux provider |
| `pi` | `reference` | `durable/interop/bench.mjs`: pi-durable at PiG's pinned Pi version on Node with `node:sqlite` |

Its `rule` is `native`. These are not the deck's Rule A numbers, which come from durable-bench's JavaScript model in a Durable Object. The rule also keys the tracker's rolling history, so `native` lines never mix with Rule A lines.

A profile has these fields:

| field | meaning |
|---|---|
| `name`, `rule`, `about` | The name, the tracker rule, and a description. |
| `fingerprints` | The history fingerprint every seed must reproduce, by size. |
| `targets[].name`, `targets[].role` | One or more `pig` targets, exactly one `reference` target, and at most one `floor` target. All of them run interleaved in one pod. |
| `targets[].rule` | A pig target's tracker rule. With one pig target it defaults to the profile's `rule`; with several, to `<rule>-<target name>`. Each pig target writes its own line per size, so the rules must differ and their rolling histories never mix. |
| `targets[].seed`, `targets[].seedMeta` | The command that builds the target's history from zero, and the JSON file it writes (`seedMs`, `fingerprint`, `bytes`). A floor target has no seed. |
| `targets[].sample` | The command that measures one sample and prints one result line with `open` and `turn[]` on stdout, and optionally `openCpu`, `turnCpu[]` (process CPU ms) and `bytes`. |

Commands are argument lists. The pod replaces `{root}` (the image's install root, `/opt/pig`), `{dir}` (the target's own fixture directory for one size), `{turns}` and `{sample}` (the round). In bundle mode the runner checks, before it submits a Job, that `ARTIFACTS.sha256` lists every program the session's targets run: each `{root}/` path, also inside an argument such as `-wasm={root}/dist/a.wasm` (a directory passes when the list holds a file in it), and each bare command other than the base image's `node`, which resolves to the bundle's `bin/`.

## Limits

- A session measures what the node gives it. On shared cores, neighbours change during a session. Interleaving gives both targets the same neighbours, but it does not remove the noise. Calibration measures it. The default gate judges CPU time there, but wall-time values in the tracker line still carry the neighbours' noise.
- With the CPU manager policy `none`, `taskset` pins processes to cores that other pods may also use. Pinning keeps both targets on the same cores. It does not reserve them.
- The CFS quota throttles the pod only when its processes use more than its CPUs. Pinning to as many cores as the pod's CPUs prevents that for the measured processes, and `cpu.stat` shows whether any throttling occurred.
- Tracker lines are a private record between sessions. Publishable numbers need a full session as durable-report's runner specification describes.
