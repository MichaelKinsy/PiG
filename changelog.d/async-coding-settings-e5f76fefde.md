### Fixed

- Settings: a change whose save failed (for example while another process held the settings lock) is now saved by the next successful save of the same file, as Pi does; before, it stayed in effect until the restart and was then lost. <!-- fix: e5f76fefdec454b13f8bfc1a44c72e4a1e166c3d -->
- Settings: an extension settings storage whose lock callback changes a setting no longer blocks forever; the change is saved after the save in progress, as in Pi. <!-- fix: e5f76fefdec454b13f8bfc1a44c72e4a1e166c3d -->
