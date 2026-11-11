### Fixed

- p2: a worker restarted after it was killed mid-call resumes its code-mode script again. Its Harness read the models plugin's configuration (the scheduler's restart limit among it) as a section of the host's loop while the scheduler held its own lock, and a batch of the loop that woke the scheduler waited for that lock: a lock-order deadlock (the script hung until its 30 s wait). As in Pi, which reads the configuration synchronously, the Harness reads a view that a synchronous observer records in the batches that compose it. <!-- fix: 2d625ae724a7c85a63b768696594fb0fbdf21b29 -->
