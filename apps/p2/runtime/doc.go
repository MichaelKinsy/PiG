// Package runtime ports apps/p2/src/runtime of Pi's p2 (a self-hosted Pi Durable UI built on Chord v2). It is the code
// the server, the session workers and the plugins share: principals, contracts that every client reads, the p2-typed
// Chord helpers and configuration files. The TUI and browser parts of src/runtime (tui*.ts, web*.ts*) belong to
// apps/p2/tui and apps/p2/web.
//
// p2 is experimental upstream ("p2 is a prototype. It will be gone soon.") and PiG carries it as such (D477).
package runtime
