### Fixed

- Registering a native provider did not refresh the Session's active model from the registry, as Pi does after `registerNativeProvider`; the registry's observers are now told, and the Node runtime refreshes its model view too. <!-- fix: c1290084cb -->
