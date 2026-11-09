### Fixed

- A failed extension load could report a less specific error than the extension's stderr gave. When a Go, Rust, Python or Lisp runner failed its only member and exited at once, the host read the stderr log before its copy had forwarded the runner's last line, so a factory that returned nothing after a host helper call was reported as a bare "connection closed before register", while the same factory in a shared cell reported "factory returned nil". The host now waits for that copy to end, within two seconds, before it reads the cause. <!-- fix: 536faf0fb -->
