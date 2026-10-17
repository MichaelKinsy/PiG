### Fixed

- Selecting a `go.work` member module that is itself an exact factory package resolves that module's factory and keeps the workspace as the compiler closure (#212). Before, the selection scanned every member module and reported all of their factories as ambiguous, so a monorepo with one module per extension and a root `go.work` could not install any member. The workspace root itself stays ambiguous when several members hold factories. An exactly selected member that is a `package main` standalone resolves to that member instead of "spans N modules".
