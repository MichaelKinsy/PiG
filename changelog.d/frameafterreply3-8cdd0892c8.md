### Changed

- Interactive mode keeps one `message_update` for each part of the message while it collects the events that Pi's event loop handles in one task, where it kept every update until the task ended: a response that arrives in one piece no longer holds hundreds of partial messages or sets the message block hundreds of times for one frame. With it, drawing a reply and the end of its run in one frame costs no CPU: a prompt of a long session takes 2 % to 3 % fewer user instructions than before that fix, where the fix alone took 3 % more.
