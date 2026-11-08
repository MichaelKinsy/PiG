### Fixed

- A Go extension's footer branch callback (`FooterData.OnBranchChange`) that made a host call, including the SDK's own report of a panicking callback, stopped the extension from serving any further request, because the callback ran on the message loop inside the state update that changed the branch and the call waited for that update. The callbacks now run on a worker, one change at a time in the order the branch changed, so a callback can call the host as it can in Pi. <!-- fix: ec1e0b68ca -->
