Differential-test oracle for pi_width_diff_test.go.

- utils.ts: verbatim copy of upstream pi-mono v0.87.1 packages/tui/src/utils.ts (MIT, LICENSE.pi).
- components/{text,truncated-text,markdown}.ts, latex.ts, terminal-image.ts: verbatim upstream v0.87.1 (used by tui/component_width_sweep_test.go).
- marked/: verbatim marked 18.0.11 (MIT, marked/LICENSE*), the version upstream pins.
- get-east-asian-width/: verbatim get-east-asian-width 1.6.0 (MIT), the version upstream pins.

Node runs utils.ts directly (type stripping); the test links get-east-asian-width
into a temp node_modules. Refresh both when the upstream pin moves.
