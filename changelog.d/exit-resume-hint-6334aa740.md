### Fixed

- A Piglet frontend that draws the run no longer has the session resume line (`To resume this session: ...`) written over or under its exit screen: the host writes nothing to the terminal after the frontend closes. The ANSI renderer prints the hint exactly as before, and a run that fell back from the frontend to the ANSI renderer still prints it. <!-- fix: 6334aa7405e70486993af326459936fd79940785 -->
