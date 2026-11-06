### Fixed

- Messages queued during an overflow compaction now go through the input handlers and prompt template expansion when the retry turn takes them, and an extension command among them runs instead of being queued as text, as Pi does. <!-- fix: 2492caeaa0 -->
- Flushing the messages queued during compaction no longer moves the output of `!` commands that ran during a run into the chat; only a prompt you submit does that, as in Pi. <!-- fix: 2492caeaa0 -->
- Messages restored after a rejected flush stay queued until the next compaction ends instead of being sent with your next submission, as in Pi, and a flush that fails at one message no longer sends the messages behind it. <!-- fix: 2492caeaa0 -->
- Startup now paints the resumed transcript before the startup diagnostics, the models.json error, the model fallback message and the crash notice, and shows the changelog notice before the transcript, as Pi orders init() and run(). <!-- fix: 68457ed2d2 -->
