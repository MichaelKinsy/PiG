### Security

- p2: loopback alone is no longer the owner. A request on 127.0.0.1 without a credential is nobody; the local owner is a signed session cookie that the TUI, p2 owner-link and the desktop app obtain with the data directory owner key through a one-time challenge, and the server prints a one-time sign-in token at startup. Owner cookies are SameSite=Lax, GET /owner-token takes only same-origin requests and the data directory is made 0700 (Pi 7d073328).
