### Fixed

- durable/harness: a host embedding `durable/harness` could be ended by Go's `concurrent map read and map write` when a registry change refreshed blocked reasons concurrently with commit publication, could hit a data race when a task graph or conversation view observer attached during commit publication, and could see a task graph observer show a task blocked for a definition that had just been installed until the next registry change. Present since 0.7.0. <!-- fix: d1a4d01571 --> <!-- fix: 388aed919e --> <!-- fix: deb3251294 --> <!-- fix: 7f55f03889 -->
