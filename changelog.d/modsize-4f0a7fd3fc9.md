### Changed

- The root module zip that `go install` downloads is about 2.8 MB smaller: the SDK test trees (`extensions/sdk-py/tests`, `extensions/sdk-lisp/tests`, `extensions/sdk-rs/tests`) and the benchmark data trees (`bench/evals`, `bench/bolt-aot`, `bench/compare`, `bench/k8s/image`) now sit in data-only nested modules. The files stay in the repository and are read by path; the `pig` binary is unchanged.
