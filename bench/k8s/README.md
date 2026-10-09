# bench/k8s: PiG benchmarks on Kubernetes

`go run ./bench/k8s/cmd/k8sbench` runs one benchmark session per Job: PiG and pi-durable interleaved in one Guaranteed-QoS pod, pinned to the same cores, with the node's facts and CFS throttling recorded. It writes durable-report's tracker format. [docs/site/docs/benchmarking.md](../../docs/site/docs/benchmarking.md) is the user guide.

| path | content |
|---|---|
| `cmd/k8sbench/` | the runner, the in-pod session (`k8sbench pod`) and the bundle builder (`k8sbench bundle`) |
| `cmd/k8sbench/templates/job.yaml` | the Job template; every value comes from the runner's configuration |
| `cmd/k8sbench/profiles/` | built-in workload profiles |
| `image/` | the benchmark image or, with `BUNDLE=DIR`, the artifact bundle for registry-free runs: `Dockerfile`, `build.sh` |
| `values.example.yaml` | every configuration field, with placeholders; copy it to the gitignored `values.local.yaml` |

Keep cluster-specific values out of the repository. They belong in `values.local.yaml`, environment variables, or flags.

The tests need no cluster: `go test ./bench/k8s/...`.
