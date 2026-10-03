# Windows status wording, both outcomes

Neither patch is applied. The owner chooses. Both apply cleanly to `team/smc1/win-ga` with `git apply` and touch the same four files:

- `windows-supported.patch`: Windows is supported on amd64.
- `windows-preview.patch`: Windows stays a preview, with the remaining gaps stated.

| File | Change in both |
|---|---|
| `README.md:30` | the preview sentence becomes a status sentence that links `windows.md` |
| `docs/site/docs/index.md:5`, `quickstart.md:3` | the preview sentence is removed (supported) or links `windows.md` (preview) |
| `docs/site/docs/windows.md` | drops "Do not treat Windows as release-supported"; adds an Install section with `install.ps1` (the page omitted it); adds a Shell section (Git for Windows, `bash.exe` on `PATH`, `shellPath`, the `powershell` tool); adds "Differences from Pi on Windows" (D39, D68, D67, D69, suspend, extension transports) in place of the Verification section |

## Check each claim before applying

Supported:

1. "amd64": needs W1 to W6 to pass on amd64. Without native arm64 evidence the draft says arm64 is unverified. If an arm64 run passes, replace that sentence with the supported architecture list.
2. The terminal and install paths the page names must each have a recorded native pass. The draft names `install.ps1`, npm, `go install` and the zip.
3. Add to the 0.4.0 notes: "Windows is supported on amd64. A standalone `pig.exe` still does not update itself (D39); rerun the installer."

Preview:

1. The four bullets are the gaps the tree shows (D39, arm64 without a smoke, CI scope, conhost and VS Code terminal). Edit them against W1 to W7 results before applying: delete a bullet whose gap closed, add each failure the PowerShell session reports.
2. Add to the 0.4.0 notes: "Windows remains a preview. See Windows setup for what is not yet covered."

The public-claims gate (`go test ./test/docs-drift`) was run with each patch applied. It reports the same two findings it reports on `agg-100` without either patch, both in `docs/plan/progress/*/PROGRESS.md` and unrelated to this change.
