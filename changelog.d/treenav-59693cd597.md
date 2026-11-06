### Fixed

- Interactive mode: after choosing an entry in `/tree` the chat first redrew at the old position and, about a second later on a large session, snapped to the fork point. The frames of the choice that starts the navigation are held until the transcript is swapped, so the prompt stays up and the first frame after it is the fork point. <!-- fix: 59693cd597bafac9b94662ce0d8d342583295849 -->
