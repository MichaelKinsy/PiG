### Fixed

- A `/reload` that arrived while the supervisor was restarting a crashed isolated extension could end the process the reloaded extension runs in, so its next call failed with a reset or closed connection. The restart now runs under the same lock as load and reload: a reload waits for the restart to register, then retains the restarted process; a restart that a reload or load already replaced does nothing. <!-- fix: ab1324df34 -->
