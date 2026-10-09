### Changed

- `tui.NewBranchSummaryComponent(summary)` is `tui.NewBranchSummaryMessageComponent(message, markdownTheme, outputPad)`, Pi's `BranchSummaryMessageComponent` constructor. The expanded summary now renders with the given markdown theme (it ignored one before), and `outputPad` is the Box's horizontal padding.

- `env.SshArgumentsWith(target, strictHostKeys, knownHostsFile)` is Pi's positional `sshArguments`; `SshArguments(target, options...)` keeps its options form and applies Pi's defaults.
