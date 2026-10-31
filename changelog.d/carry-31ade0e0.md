### Fixed

- A saved "Do not trust" decision is no longer ignored for a project without trust-requiring resources, for example a folder with only `AGENTS.md`. `pig` auto-trusted such a project whatever `trust.json` held, so `ctx.isProjectTrusted()` returned true and the package commands treated it as trusted. A saved decision now takes precedence, and auto-trust applies only when none is saved; `pig status` and `pig diagnose` report the saved decision too. Carried forward from Pi 31ade0e0 (earendil-works/pi#10947), past the 1.4.0+b3a29f11 pin. <!-- fix: 31ade0e0 -->
