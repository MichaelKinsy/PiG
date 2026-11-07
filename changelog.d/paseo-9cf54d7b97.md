### Fixed

- A Node extension's `message_end` event now carries the same message object that `ctx.sessionManager.getEntries()` returns in the matching session entry, as in Pi, where the extensions see the event first and the entry then holds `event.message` itself. An extension that finds an entry with `entry.message === message` found none in PiG. Paseo's Pi integration depends on that lookup: through PiG its timeline did not show the user's prompt until the session was reopened, and a conversation rewind was refused. <!-- fix: 9cf54d7b97 -->
