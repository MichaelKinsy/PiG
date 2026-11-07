### Fixed

- On macOS and the BSDs, a chord worker that found a lock file left by a crashed holder waited for it forever instead of failing with the "stale lock file" error that names the file to remove. Connecting to a plain file fails with ENOTSOCK there (ECONNREFUSED on Linux), and the waiter took that for a holder that was still releasing and retried without end. <!-- fix: ee0017ae81495ad4bbf1ab69fe2c181fe356f7c1 -->
- `pi.exec` reports `killed: true` for a command whose timeout fell due before the call settled, as Pi does, when the process was kept off the CPU past both the timer and the child's end; the result used to depend on which of two ready channels the watcher picked. <!-- fix: 1caec6e3a589500f6a2f37260ff04f3bccaf924d -->
