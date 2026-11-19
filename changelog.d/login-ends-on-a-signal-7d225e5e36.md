### Fixed

- `pig login` and `pig logout` end on Ctrl+C, SIGTERM and SIGHUP at the provider menu, the logout menu and inside a provider's login. Ctrl+C and SIGTERM were ignored there before; the process now ends by the signal after its cleanup, as Pi's does. `pig auth login` restores the terminal before ending on SIGTERM and SIGHUP (it left the terminal raw), and Ctrl+C still prints "Login cancelled." and exits 1. Affects 0.5.0 through 0.7.0. <!-- fix: 7d225e5e36 -->
