### Fixed

- A reload that retries an object whose earlier restart is still parked behind a call running on its old attempt, in the same reload that adds the plugin serving the type the object waited for, reported the object as `parked` with the stale cause `waiting for <type>`; it reports it as `retried`, as Pi does. <!-- cause: the report read the earlier restart's readiness cause before the reload's publication woke the request of that readiness that waited for the type, where Pi reads it after; the restart readiness is new since v0.7.0 --> <!-- fix: a4f9a5907f -->
