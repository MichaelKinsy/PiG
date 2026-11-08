### Fixed

- A host call that read a change of the model, session id or name between the change and its report to the extensions, and the first `watchSessionLog` read of a replaced Session, could reply ahead of the state that names it. The Session now brackets each such change, and the host flushes its state before every reply while one is in flight, and under the cursor lock for `watchSessionLog`. <!-- fix: 6e789bbd91 -->
