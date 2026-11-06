### Fixed

- In RPC mode, `exit.log` no longer says "received SIGTERM" for a single signal that arrived during a shutdown that stdin's end or `ctx.shutdown()` had started and that finished with exit status 0. The line now reads "received SIGTERM during shutdown, which completed (exit 0)", and a repeated signal's line reads "received SIGTERM again during shutdown (exit 143)". The cleanup, the log line and the process exit of an RPC exit now run once, so a repeated signal that arrives while another exit is already past its stdout wait no longer repeats them (D234). <!-- fix: caae28ae1c -->
