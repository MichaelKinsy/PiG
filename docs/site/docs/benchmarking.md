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

Go programs choose code by CPU and by processor count. The Go runtime's Green Tea garbage collector scans with AVX-512 where the CPU has it, and a Go runtime sizes its scheduler from `GOMAXPROCS`, which it otherwise derives from the node's CPUs or the cgroup's CPU quota. The same Go benchmark therefore spends a different CPU time on another node type, or under another quota. The session sets `GOMAXPROCS` explicitly for every process it runs, whatever the pod's environment says: to the number of measured cores (the pinned cores, or every allowed core without a pin), at most the pod's CPUs. Without a pin on a node without the static policy, the allowed cores are every core of the node, but the pod's CPU limit is still its CPUs. The facts line records the CPU model and its vector features (`cpuFeatures`: of `avx`, `avx2`, `avx512*`, `gfni`, `bmi2`, `popcnt`, `asimd`, `sve`, `sve2`, those the first core lists, with the kernel's names such as `avx512_bitalg`), the pin line records `gomaxprocs`, and every seed and sample line carries all three. The analysis refuses a pod whose samples name two node types, and a spread run whose nodes differ in any of them. The tracker summary names the node type after the cpuset, as `<cpuModel> [<cpuFeatures>] GOMAXPROCS <n>`. The rolling median and `-baseline` are not filtered by node type, so check that name when a line compares runs from different dates. Pin one node type, for example with `-node`, `-nodes` or a `nodeSelector` on a node-type label.

The measured cores must keep a fixed clock. A cpufreq governor such as `schedutil`, `ondemand` or `powersave` moves the clock with load, and a CPU time measured in nanoseconds moves with it: the same work then takes a different CPU time from one process to the next. The session reads the governor of each measured core (the pinned cores, or every allowed core without a pin) before it measures anything, and refuses such a node with the cores and governors in the error. A governor file that exists but cannot be read counts as `unreadable`, and the session refuses it the same way. A spread session then stops on all nodes. Choose nodes whose cores have no governor or `performance`; the runner does not change kernel settings. Neither setting stops the hardware's own boost (turbo) from moving the clock. With `performance` the cpu lines record the clock (see below); without a cpufreq driver the clock is not visible, and calibration is the evidence. `-allow-frequency-scaling` runs such a node anyway, for example on a laptop or a test cluster: the analysis reports the session but never judges it, so a calibration is not judged and a comparison writes no tracker line.

While each process runs, the session samples the measured cores' current clock (`scaling_cur_freq`) every 50 ms when cpufreq reports it, and records the mean, lowest and highest of the highest core's clock in the cpu line (`mhz`, `mhzMin`, `mhzMax`). Cores without cpufreq report no current clock, so their cpu lines have no `mhz`. Each calibration node entry has a `clock` object: the governors, the mean clock, its CV, and the correlation of each run's CPU time with its clock. A strongly negative correlation means the clock, not the work, moved the CPU time.

## Build the image

The image holds PiG's Durable builds (the TinyGo and Go Wasm cores and the native Go Durable benchmark), Node, pi-durable and Pi's coding agent at PiG's Pi pin (`.upstream/pin`), and the Pi reference checkout at the commit in `durable/contract/PIN`. When the pin is a release, both come from the published packages that the lockfiles in `bench/k8s/image/pi-durable` and `bench/k8s/image/pi-agent` lock; a unit test fails when a lockfile's version differs from the pinned version. When the pin is a commit, npm publishes no build of it: Pi's own packages (pi-durable, pi-ai, chord, the coding agent, pi-tui and the rest) are the packs that `python3 automation/pin/pin.py install-oracle` builds from the pinned source, installed with the frozen dependency closures `automation/pin/frozen/<commit>.durable-closure.json.gz` and `<commit>.closure.json.gz`. Each lock checks each pack's SHA-512, so a pack that is not the bytes the closure was frozen with fails the install. Run `install-oracle` before `build.sh` or `k8sbench bundle`. Both read the packs from its build store (`PIG_PI_BUILD_STORE`, else `$PIG_DEV_HOME/pi-builds`, else `~/.cache/pig-dev/pi-builds`), and `k8sbench bundle -pi-builds DIR` names another store. `build.sh` writes both install projects with `k8sbench pi-packages` and passes them to the image build as the build context `pi`. The image build then checks the installed coding agent with `k8sbench pi-packages -check`, against the same pins as a bundle. `VERSIONS` records the pin's version, such as `pi-durable=1.1.0+42a3497d` and `pi-agent=1.1.0+42a3497d`, and `ARTIFACTS.sha256` lists the packs. Base images are pinned by digest. Downloads are pinned by version and SHA-256.

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

Both refuse a tree with uncommitted or untracked files (`-allow-dirty`, `K8SBENCH_ALLOW_DIRTY=1`). `k8sbench bundle` finds TinyGo through `-tinygo`, else `$TINYGO`, else `PATH`, and stops before building anything when it finds none. When the tree has `.go-version`, `k8sbench bundle` also stops before building anything unless `go env GOVERSION` is that release; set `GOTOOLCHAIN` to it. `go.mod`'s toolchain line is only the minimum. Every Go program in the bundle, the Wasm cores included, builds with that release and through the repository's `go.work`, as the development gates build. It cross-compiles the Go binaries for Linux and installs the reference packages for Linux with `npm ci --os=linux --cpu=<arch>`. Then point the runner at the directory with `-bundle <dir>` or `K8SBENCH_BUNDLE`, and leave the image unset. The runner refuses a session that sets both. `-commit` refuses a bundle built from another commit before anything is submitted.

Agent profiles run the coding agents themselves, driven by a Python harness. `k8sbench bundle -agents` adds three things:

- PiG built from the commit (`bin/pig`).
- Pi's coding agent at PiG's Pi pin (`pi-agent/`, about 165 MB). At a release pin, `npm ci --ignore-scripts` installs it from Pi's release-published install pair, which `bench/k8s/image/pi-agent` keeps unmodified. The bundle checks the pair and the installed CLI against their published SHA-256. Pi's install lockfile records no integrity for Pi's own packages (`@earendil-works/*`), so `npm ci` does not check them. The bundle therefore also pins their installed content: the SHA-256 of the `sha256sum` lines of every file under `pi-agent/node_modules/@earendil-works` except nested `node_modules`, in bytewise path order. This digest equals the digest of the registry tarballs at the pinned Pi version. The lockfile's integrity covers the nested third-party packages. At a commit pin, the install project is the frozen closure with the pinned source's packs (see the image above), and its lockfile records the SHA-512 of every pack. The bundle pins that project's `package.json` and lockfile, the CLI and the same content digest, for each pinned commit. A commit without pins is refused.
- A standalone CPython (`python/`, about 105 MB): python-build-standalone's stripped install-only build, pinned by release, size and SHA-256 for each architecture. It runs from any directory and needs nothing from the base image. `-python-archive FILE` takes an offline copy of the pinned archive instead of downloading it; the copy must match the same size and SHA-256. `-python-archive` without `-agents` is refused.

`-arms codex` (with `-agents`) also installs other agents from `bench/evals/arms.lock`, the pins the local benches use, each under `arms/<id>/`. The bundle installs an npm arm as `pigbench` does: `npm ci --ignore-scripts` from the arm's lockfile, after it checks the lockfile's SHA-256 and its record of the package's version and integrity against `arms.lock`. The lockfile's integrity covers every downloaded package. The installed executable must then have the SHA-256, and the size when pinned, that `arms.lock` gives for `linux-<arch>`. A release arm is its release archive for `linux-<arch>` (a `.tar.gz`, from an `https` base), which must have the size and SHA-256 `arms.lock` pins; only the executable member stays, and it must be a Linux executable of the bundle's architecture. An arm with nothing pinned for that platform is refused, and so is any other source. PiG and Pi are not arms here: the bundle always carries them. `ARTIFACTS.sha256` lists each arm's lockfile, when it has one, and executable, and `VERSIONS` records `arm-<id>=<version>`. The bundle also carries `bench/evals`' `arms.toml` and `arms.lock` under `evals/`, so a sample can name an arm (`pigbench k8s overhead-sample --arm <id>`) and get its harness settings, such as another agent directory variable. The image installs the arms its `ARMS` build argument names (`ARMS=codex bench/k8s/image/build.sh`) with `k8sbench arms`, the same code.

The pod installs nothing from the network. `ARTIFACTS.sha256` lists `bin/pig`, `pi-agent/package-lock.json`, the agent's CLI (`pi-agent/node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js`) and the interpreter (`python/bin/python3.12`). `VERSIONS` records `pi-agent=<version>` and `python=<version>+<release>`. The runner refuses an agent bundle that lacks one of these, has unpinned agent files, or has an interpreter of another architecture. The benchmark image always carries all three. A bundle without `-agents` cannot run an agent profile, and the runner says which program is missing before it submits a Job.

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
2. It pins the measured processes to the session's cores and runs each of them with its own `GOMAXPROCS`, the number of measured cores (at most the pod's CPUs), so an inherited `GOMAXPROCS`, for example a local shell's, never reaches a measured process. `ENV.txt` names the value.
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

A sample line keeps every field the workload wrote. The workload's own target name moves to `bench_target`, and the PiG target's `version` is the image commit. The session writes its own `gomaxprocs`, `cpuModel` and `cpuFeatures`; a workload that reports any of them keeps its value as `bench_gomaxprocs`, `bench_cpuModel` or `bench_cpuFeatures`. These are the sample and seed line shapes that durable-report's `assemble.mjs` reads.

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

The built-in profile `longhistory` runs `bench/evals`' long-history bench (`make bench-longhistory`) in the pod. It needs an agent bundle (`k8sbench bundle -agents`) or the benchmark image.

| target | role | command |
|---|---|---|
| `pig` | `pig` | `pigbench k8s sample` on PiG built from the commit (`bin/pig`) |
| `pi` | `reference` | `pigbench k8s sample` on Pi's coding agent at PiG's pinned Pi version (`pi-agent/`) |

Its sizes are history tokens: `-turns 1000000,3000000`. `1000000` is a session of about 1M tokens with no compaction. `3000000` is about 3M tokens across three compactions. Each target's seed generates the same session from seed 20261009 with `pigbench k8s generate`, and the profile pins both sessions' fingerprints. A fingerprint is the session's sha256 with `/tmp/pigbench-history-project` as the working directory in its header, which is the digest `make bench-longhistory` reports on a host whose `TMPDIR` is `/tmp`. The pod writes the session under its own `TMPDIR` (`/data/tmp`), so only that header field differs, and the sample checks the file's own sha256. One sample runs three fresh processes on copies of the session, against pigbench's local mock model:

- RPC: the open, a resume from a fresh process, and five appended turns. The line's `open` is the RPC open and `turn[]` the appended turns, so cold is the open plus the first turn and warm is the median of the other four.
- TUI: the first output, the settled screen, keystrokes while idle and while a reply streams, the frames of the stream, and a width and a height change, each a full redraw in Pi.
- TUI `/resume`.

Both TUI phases run with `tuiMode` set to `fullscreen`. `make bench-longhistory` also measures `regular`; `pigbench k8s sample --mode regular` measures it in a profile of your own.

The profile declares their times, CPU times and memory as metrics. It gates none of them yet: a calibration on the cluster measures their noise first. A pod's cgroup is read-only, so pigbench measures each agent's process tree from `/proc`: CPU time from each process's CPU-time clock (nanoseconds, not 10 ms clock ticks), peak and current memory as the sum of the processes' `VmHWM` and `VmRSS`, and the sockets every 25 ms. A connection to anything but the mock fails the sample. The analysis's own `cpu` is the pod cgroup's usage over the whole sample: the three agent processes, pigbench and its mock.

The built-in profiles `overhead-pi`, `overhead-codex` and `overhead-rpi` run `bench/evals`' overhead bench (`make bench-overhead`) in the pod: what each agent's harness costs on its own, against pigbench's local mock model, which answers at once. They compare PiG on the same node with another agent, one reference per profile, so their tracker histories never mix.

| profile | `pig` target | `reference` target | scenarios | needs |
|---|---|---|---|---|
| `overhead-pi` | PiG built from the commit | `pi`: Pi's coding agent at PiG's pinned Pi version | `version`, `print`, `print-tools`, `rpc`, `pty` | `k8sbench bundle -agents` or the image |
| `overhead-codex` | PiG built from the commit | `codex`: Codex CLI at the version `arms.lock` pins | `version`, `print`, `print-tools` | `k8sbench bundle -agents -arms codex` or the image built with `ARMS=codex` |
| `overhead-rpi` | PiG built from the commit | `rpi`: the Rust port of Pi, the release binary `arms.lock` pins, measured as that arm (`--arm rpi`: its own agent directory variable and RPC prompt field) | all five | `k8sbench bundle -agents -arms rpi`; the profile's `baseImage` is `node:24.19.0-trixie-slim` pinned by digest, because rpi needs glibc 2.39 |

A size is a number of tool rounds N: `-turns 8` is the local bench's `print-tools`. One sample runs each scenario as a fresh process: `version` is exec to exit; `print` is one prompt and one reply; `print-tools` is one prompt, N rounds of one tool call that reads a file, then the reply; `rpc` is ready (spawn to the first `get_state` answer) and then N prompts of one tool call each; `pty` is the interactive TUI's first frame, settled screen, and one prompt from the keyboard to the mock and to the screen. The line's `open` and `turn[]` are `print-tools`', the scenario every agent has: `open` is spawn to the first model request, and `turn[]` the N gaps between the end of a reply and the agent's next request, which is what the harness adds per turn, its tool run included. `openCpu` and `turnCpu[]` are the process tree's CPU time to the first request and between requests, and `peakRss` is `print-tools`' peak RSS. The profiles declare the other scenarios' times, CPU times and memory as metrics and gate none of them until a calibration has measured their noise. Codex has no Pi RPC mode, and its TUI does not reliably send the scripted prompt to the mock, so `overhead-codex` leaves out `rpc` and `pty`.

Each target's seed (`pigbench k8s overhead-seed`) builds no fixture. Its fingerprint is the digest of the workload's definition: the scenarios, N, the prompt, the file the tool reads, the reply and the model. The profiles pin it for `-turns 8`, and a sample refuses scenarios or a size other than its seed's, so every target of a session ran one workload. A connection to anything but the mock fails the sample.

The built-in profiles `session-scale-context` and `session-scale-entries` run `bench/sessionscale` (`sessionscale run`) in the pod: PiG built from the commit and Pi's coding agent at PiG's pinned Pi version resume one long-lived session file each and answer prompts against sessionscale's local scripted model. They need an agent bundle (`k8sbench bundle -agents`, which builds `bin/sessionscale`) or the benchmark image.

| profile | sizes (`-turns`) | fixture |
|---|---|---|
| `session-scale-context` | `1000000`, `2000000`, `4000000`: the active context in tokens | a 299 MiB file of about 45,000 entries, 63 compactions and 17 leaves that resumes to that context, with a model context window the context fills to 85% (rounded up to 1,000 tokens) |
| `session-scale-entries` | `7`, `65`, `299`: the file size in MiB | about 880, 9,900 and 45,700 entries; `299` has the shape of the reported 46,100-entry, five-day session; each resumes to a 150,000-token context |

Each target's seed (`sessionscale k8s seed`) generates the fixture from seed 1 in its own directory. The fingerprint is the file's sha256 with `"cwd": "/sessionscale"` in its header, so the copies of two targets agree, and the profiles pin it for every size. A sample (`sessionscale k8s sample`) checks the file's own sha256, then starts the agent in a pseudo-terminal on a copy with `--session` and `-ne`, and sends five prompts. `open` and `openCpu` are the time and the process tree's CPU from the start to the resumed screen: the ready check waits for one quiet second and the settle check for half a second of idle CPU, the same for both targets. `turn[]` and `turnCpu[]` are each prompt's time until the answer is shown and the tree is idle again. The tree's CPU comes from `/proc` in clock ticks of 10 ms, so the per-prompt CPU of a small fixture is coarse; the cgroup's `cpu` is exact. `peakRss` is the tree's peak RSS in MiB. The metrics, none gated until a calibration has measured their noise, are the tree's RSS, PSS and anonymous memory after the resume and after the prompts, the quit time, and the first prompt's request size and message count. A 4M-token context needs a few GiB per agent: the default `memory`, 8Gi, holds it.

The built-in profile `durable-core` measures PiG's Durable core, the one the conformance gate checks, against the gate's own reference:

| target | role | command |
|---|---|---|
| `pig-tinygo` | `pig` (rule `core-tinygo`) | `core/bench-pig.mjs`: `durable/interop/bench.mjs` on the core built with TinyGo, through `durable/core/host/cf`'s pi-durable-compatible facade on `node:sqlite` |
| `pig-native` | `pig` (rule `core-native`) | `sqlhost-bench seed` and `sample`: the core built natively, with the Go port of `bench.mjs`'s scripted model |
| `pi` | `reference` | `core/bench-pi.mjs`: `durable/interop/bench.mjs` on pi-durable, pi-ai and chord built from the Pi sources at `durable/contract/PIN` main |

`durable/core/host/cf/bench/build.mjs` builds the two Node programs, each one self-contained file, so the reference is the commit the core's corpus is captured from, not an npm release. `k8sbench bundle` builds them when the tree has that builder; it needs the Pi checkout at that commit with its dependencies installed (`-pi-source DIR` or `CONTRACT_PI_SOURCE`). The profile runs from a bundle only: the image does not carry the programs. Before it submits a Job, the runner checks that the bundle lists, in `ARTIFACTS.sha256`, every program the session's targets run: each `{root}/` argument and each bare command other than the base image's `node`, which resolves to the bundle's `bin/`. All three targets run the same scripted model and must reproduce the same seed and measured transcripts. These are Node and native process numbers, not Durable Object numbers.

Every `durable-core` sample also reports `retained`: the memory the open store keeps after a forced collection, in MiB, measured after the last timed turn. On Node (run with `--expose-gc`) it is the JS heap plus external memory, which holds the TinyGo core's Wasm linear memory; for the native core it is the Go heap. SQLite's own allocations are outside both. The tracker line carries it as `retainedMB` and the reference's `piRetainedMB`, in track-row's MB, with a verdict like the times; it is reported, not gated. A target that does not report it, such as `durable-native`'s, leaves both out.

A profile has these fields:

| field | meaning |
|---|---|
| `name`, `rule`, `about` | The name, the tracker rule, and a description. |
| `fingerprints` | The history fingerprint every seed must reproduce, by size. |
| `targets[].name`, `targets[].role` | One or more `pig` targets, exactly one `reference` target, and at most one `floor` target. All of them run interleaved in one pod. |
| `targets[].rule` | A pig target's tracker rule. With one pig target it defaults to the profile's `rule`; with several, to `<rule>-<target name>`. Each pig target writes its own line per size, so the rules must differ and their rolling histories never mix. |
| `targets[].seed`, `targets[].seedMeta` | The command that builds the target's history from zero, and the JSON file it writes (`seedMs`, `fingerprint`, `bytes`). A floor target has no seed. |
| `targets[].sample` | The command that measures one sample and prints one result line with `open` and `turn[]` on stdout, and optionally `openCpu`, `turnCpu[]` (process CPU ms) and `bytes`. The pod writes the size as `turns` on every seed and sample line and the round as `sample` on every sample line; a workload's own different value of either stays as `bench_turns` or `bench_sample`. |
| `baseImage` | Optional. The image a session of the profile runs on, pinned by digest, when its programs need another than the default `node:24.19.0-bookworm-slim` (for example a newer glibc). The profile then runs from a bundle only; the runner uses it when `-base-image` is unset and refuses another one. |
| `metrics[]` | Optional. The workload's own lower-is-better sample fields, each with a `name` (a camelCase field of the result line that neither the analysis nor the pod already uses, so not `version`), a `label` for the tracker summary and an optional `gate`. Calibration reports the CV of each. The tracker line carries the median over the samples of the pig target as `<name>` and of the reference as `pi<Name>`, with a verdict, when every sample of both has the field. The floor target's value is not subtracted. A gated metric joins the noise gate in every mode, and a reference sample without it is a noise problem. |

Commands are argument lists. The pod replaces `{root}` (the image's install root, `/opt/pig`), `{dir}` (the target's own fixture directory for one size), `{turns}` and `{sample}` (the round). In bundle mode the runner checks, before it submits a Job, that `ARTIFACTS.sha256` lists every program the session's targets run: each `{root}/` path, also inside an argument such as `-wasm={root}/dist/a.wasm` (a directory passes when the list holds a file in it), and each bare command other than the base image's `node`, which resolves to the bundle's `bin/`.

## GPU task completion (prepared, not run)

`k8sbench stage`, `k8sbench model` and `k8sbench trials` print the manifests of a task-completion run against a self-hosted model. They apply nothing. The operator applies each step with `kubectl` and reads the results with `kubectl logs`.

The run has three parts:

1. **Stage.** `k8sbench bundle -agents -tasks DIR -out B` builds a trial bundle. `DIR` is the dataset's `tasks/` directory, checked out at the commit `bench/k8s/tasks.lock` pins. The bundle carries PiG built from the commit, the pinned Pi, the pinned official Node.js (`node/`), and each locked task's `instruction.md` and `tests/` (never `solution/`). The command refuses a task whose files do not match the lock's digest. `k8sbench stage -bundle B` packs the bundle, prints the stage Job and the `kubectl cp` commands, and prints the archive's sha256. The stage Job checks the archive and every artifact, then unpacks the bundle on the trial claim (`gpu.trialPVC`) as `bundles/<sha256>`.
2. **Model.** `k8sbench model -model ID -staged SHA` prints four objects for one model of `bench/k8s/models.lock`. A fetch Job downloads every pinned weight file into the model claim (`gpu.modelPVC`) and checks its size and sha256. It is the only pod with network access for its own work, and it keeps files that already match. A Deployment runs the engine image, pinned by digest, with `HF_HUB_OFFLINE=1`. It requests `gpu.count` of `gpu.resource` on nodes matching `gpu.nodeSelector` and `gpu.tolerations`. It starts only when the fetch Job marked the weights verified for this pin (the marker holds the pin's sha256). A Service gives the in-cluster URL `http://k8sbench-model-<id>.<namespace>.svc:8000/v1`. A NetworkPolicy admits only trial pods of that model.
3. **Trials.** `k8sbench trials -model ID -staged SHA [-trial-arms pig,pi] [-attempts 5] [-tasks a,b]` prints one Job per task, arm and attempt. Each pod runs the task's own prebuilt image, pinned by digest. An init container copies the arm's runtime and the task's instruction from the trial claim. The trial container runs the arm's `--print --mode json` command against the model's Service with a `models.json` provider `k8sbench` (API `openai-completions`), in the image's working directory. After the agent exits or reaches the task's agent timeout, a second container (the verifier gate) copies the task's tests into the pod. The trial container never mounts the claim that holds them. The trial asks the gate through a directory it can write and reads the answer from a directory it mounts read-only. The gate answers one request. An answer that exists before the trial's own request means the agent asked for the tests, and the trial is not scored. The trial's request carries a random nonce chosen after the agent exited, and the trial accepts only an answer that echoes it. Before the agent runs, the trial starts the arm with `--version`. An arm that does not start on the task's image is not scored. No pod of the workload mounts a service account token or gets service environment variables. The trial checks the tests against the locked digest, runs `tests/test.sh` with the task's verifier timeout, and reads `/logs/verifier/reward.txt` or `reward.json`.

Each trial prints one line:

```json
{"t":"trial","task":"write-compressor","arm":"pig","attempt":1,"model":"qwen3-coder-30b-a3b-fp8","image":"docker.io/<task image>@sha256:<digest>","reward":1,"agentExit":0,"agentWallMs":412530,"agentCpuMs":9120,"assistantMessages":38,"usage":{"input":1204410,"output":18230,"cacheRead":0,"cacheWrite":0,"totalTokens":1222640},"verifierExit":0,"verifierWallMs":20410}
```

`reward` is null when the trial could not be scored, and `error` says why. A failed or timed-out agent run is still verified, as Harbor does. `agentError` carries the tail of a failed agent's stderr.

The pins:

- `bench/k8s/models.lock` pins each engine image by digest and each model by Hugging Face repository, 40-hex revision and every file's size and sha256. `k8sbench model` refuses a pin published less than seven days before the run. The lock pins vLLM with a model that fits one 80 GB GPU, and llama.cpp with a GGUF quantization for smaller GPUs.
- `bench/k8s/tasks.lock` pins the dataset's commit and each task's image by digest and its files by digest. `k8sbench tasks-lock -src DIR -dataset NAME -repo-url URL -commit SHA -published DATE` writes it from a checkout. It resolves each image tag to its registry digest and leaves out tasks without a prebuilt image or with GPUs.

Terminal-Bench 2.1 tasks declare `allow_internet = true`, and their `test.sh` installs its test tools from the network. Trial pods therefore keep the cluster's egress rules. Only the model server's ingress is restricted.

The values file's `gpu:` section holds `resource` (default `nvidia.com/gpu`), `count` (default 1), `nodeSelector`, `tolerations`, `serverCpus`, `serverMemory`, `modelPVC` and `trialPVC`. Trial pods use the top-level `nodeSelector` and `tolerations`.

## Limits

- A session measures what the node gives it. On shared cores, neighbours change during a session. Interleaving gives both targets the same neighbours, but it does not remove the noise. Calibration measures it. The default gate judges CPU time there, but wall-time values in the tracker line still carry the neighbours' noise.
- With the CPU manager policy `none`, `taskset` pins processes to cores that other pods may also use. Pinning keeps both targets on the same cores. It does not reserve them.
- The CFS quota throttles the pod only when its processes use more than its CPUs. Pinning to as many cores as the pod's CPUs prevents that for the measured processes, and `cpu.stat` shows whether any throttling occurred.
- Tracker lines are a private record between sessions. Publishable numbers need a full session as durable-report's runner specification describes.
