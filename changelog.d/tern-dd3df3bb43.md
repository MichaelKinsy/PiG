### Changed

- The Durable harness orders the commits of a job that nothing awaits by goroutine scheduling, where Pi orders them by microtask hops (D181): the job's final state and every settled read are the same, commit sequence numbers can differ, and a read taken the moment an answer settles can still show the job running; await the job or `WaitForIdle` before reading its state.
