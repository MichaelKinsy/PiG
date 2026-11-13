### Fixed

- Resource reload: an extension load hook or project-trust callback that reloads the resources with the context it received no longer deadlocks on the loader's operation lock; the nested reload runs inside the outer one, as Pi's unlocked reload does. <!-- fix: 2dc09ec10c05368947da2869f0d20a9e2f6d3a85 -->
