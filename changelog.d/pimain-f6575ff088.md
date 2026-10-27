### Fixed

- `pig install`, `pig remove` and `pig update --extensions` run npm with `--no-audit --no-fund`, wherever they already pass `--legacy-peer-deps`, so npm no longer prints audit advice that, followed in the managed package root, would install the host-provided pi packages a second time. bun and pnpm arguments are unchanged. <!-- fix: f6575ff088 -->
