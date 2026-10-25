### Fixed

- A failed install-receipt write during `pig update` or install, such as one rejected because the update CA file is not owner-only, no longer keeps its staged `.install-receipt-*` temp file open. The handle is closed before the temp file is removed, so no stray file is left on Windows. <!-- fix: 80b912a3e28ac5f240428eba73fb72f96c5557e1 -->
