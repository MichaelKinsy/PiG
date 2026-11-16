### Fixed

- A Node extension whose file or package entry point is gone is now reported with the same error whether it runs in a process of its own or shares one with other Node extensions (`build "name": ...`, in the startup error and in the reload's issues); a member of a shared process was reported as `extension "name": stat ...` or `extension "name": package ... declares pi.extensions ... but none exist`. Present since 0.2.0. <!-- tests: coding/extension/host/subprocess/reload_member_unresolved_test.go::TestNodeCellLoadsItsOtherMembersWhenOneEntryPointIsGone --> <!-- fix: d13855f45b -->
