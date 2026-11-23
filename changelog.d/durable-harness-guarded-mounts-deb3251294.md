### Fixed

- durable/harness: a host embedding `durable/harness` could be ended by Go's `concurrent map read and map write` (or report a data race) when a commit was published while a task graph or conversation view observer attached or a registry change refreshed blocked reasons, and a task graph observer could show a task blocked for a definition that had just been installed, until the next registry change. Present since 0.7.0. <!-- fix: d1a4d01571 --> <!-- fix: 388aed919e --> <!-- fix: deb3251294 -->
