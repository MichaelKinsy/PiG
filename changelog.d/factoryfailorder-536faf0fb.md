### Fixed

- A load error no longer loses the cause a Go, Rust, Python or Lisp extension's runner wrote when it failed its only member and exited at once. A factory that returned nothing after a host helper call was reported as a bare "connection closed before register" when the host read the log before its copy had forwarded the runner's last line, while the same factory in a shared cell reported "factory returned nil". The host now waits for that copy to end, within two seconds, before it reads the cause. <!-- fix: 536faf0fb -->
