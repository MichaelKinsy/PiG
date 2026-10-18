### Fixed

- Durable: `Submission.Wait`, `WaitForIdle` and `WaitForTask` return only after every event-stream listener has been given the batches committed before the wait, as in Pi. Stopping an event stream right after one of these waits no longer drops the end of a run.
