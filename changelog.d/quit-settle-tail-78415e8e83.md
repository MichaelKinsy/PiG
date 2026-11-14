### Changed

- The interactive quit (Ctrl+D, Ctrl+C twice, `/quit`) in the settle tail of a run no longer waits for the run's `agent_settled` handlers: the Session is idle then, and Pi's dispose, which awaits the idle Session, goes on to `session_shutdown` at once (a handler on a 3 s timer held the quit for 2.4 s here, 94 ms in Pi). A quit while a run is still streaming still waits for the run and its `agent_settled` handlers, as Pi does.
