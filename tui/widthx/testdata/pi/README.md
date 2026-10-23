Differential-test oracle for pi_width_diff_test.go.

- utils.ts: verbatim copy of the pinned pi-mono packages/tui/src/utils.ts (automation/gen/refresh-pi-test-sources.sh) (MIT, LICENSE.pi).
- components/{text,truncated-text,markdown}.ts, latex.ts, terminal-image.ts: verbatim from the pinned pi-mono (used by tui/component_width_sweep_test.go).
- marked/: verbatim marked 18.0.11 (MIT, marked/LICENSE*), the version upstream pins.
- marked-cjk-friendly/: verbatim marked-cjk-friendly 0.1.2 (MIT, marked-cjk-friendly/LICENSE), the version upstream pins; components/markdown.ts imports it.
- get-east-asian-width/: verbatim get-east-asian-width 1.6.0 (MIT), the version upstream pins.

Node runs utils.ts directly (type stripping); the test links get-east-asian-width
into a temp node_modules. Refresh both when the upstream pin moves.
