### Fixed

- The p2 provider login's cancel returns only once the login's end is shown to its client: a cancel that found the login already ended by its own goroutine returned before that goroutine's state reached the host, so the client still showed "working" when the call resolved. Every change of a login and its publication are one step, and a prompt registered after the login ended is no longer shown. <!-- fix: p2-login-publication -->
