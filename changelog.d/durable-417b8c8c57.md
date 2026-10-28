### Changed

- The Durable core holds about a quarter less memory for a large task graph: U-harness-scheduler-retention's 20,000-task chain now fits a TinyGo heap of 80 MiB (it needed about 102 MiB, and ran out of memory at the 96 MiB the core is built with). A queued scheduler step or reconcile pass, an invocation and a run are each one smaller allocation, tasks a host creates keep the objects its operation parsed and share equal checkpoints and outcomes, and the encoder lets go of each statement's parameters as it writes it.
