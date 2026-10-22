### Added

- `HarnessOptions.Telemetry` emits the content-free `harness.DurableTelemetrySchema` (Pi 2ec55d80): public submission admission spans (`pi.durable.submit`), task phase spans (`pi.durable.phase`, including extension tasks), committed entry and submission events, running phase-change checkpoint events, nested-tool replay events, report events and counters, and the `pi.durable.phase.duration` and `pi.durable.tasks.active` metrics. Failed phases record the fixed categories `task_faulted`, `task_failed` and `task_orphaned` with a diagnostic capped at 500 characters; an aborted outcome keeps status ok.
- `durable.TaskOptions.Trace` (a carrier, or `durable.TaskTrace{Root: true}`) and `TaskOptions.Links` choose a task's stored trace carrier and creation-time links; absence inherits the creator's carrier.

### Changed

- Stored submission records gain an optional `trace` member, and stored task records optional `trace` and `links` members, written only when the telemetry adapter supports propagation. Records written before this change decode without them and no storage migration is needed; a record with the members is read by an earlier PiG as a record with unknown members.
- `session.Hooks.Report` and `SessionImpl.Fail` take the context whose telemetry span the report belongs to, and `harness.AdmitSubmission` takes the admission span and returns an `Admission` (the submission ID and how it was admitted).
