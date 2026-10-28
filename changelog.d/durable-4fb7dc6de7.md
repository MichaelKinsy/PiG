### Fixed

- The Durable core's conversation view follows Pi f3255de7's mount: a second observer of a conversation only loads its own window and takes the one `docs` object of the mount, so attaching to a mounted view spends Pi's microtask passes and the observers of one conversation share their documents; a window the core rejects (a bound that is not an integer, `max` below `min`) reaches the host as the `RangeError` Pi throws. <!-- fix: 4fb7dc6de718961fbde2ea69cee2efba70436323 -->
- The Cloudflare host's report spans record the reason, extension and task of a `task_interrupted` report, as Pi b3a29f11's telemetry does (`task_parked` no longer exists). <!-- fix: 970c503b0533196a563bcf40698b192c720de3ee -->
