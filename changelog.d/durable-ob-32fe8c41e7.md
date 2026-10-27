### Fixed

- The Durable core's Session opens a document watch, a conversation view and the task graph as a job on the Session line, so an attachment made after a commit was queued starts at that commit's value; `watchDoc` and `observeDoc` reject on a closed Session with `Session is closed`; a watch keeps its Session alive while it runs and, once stopped or retired, no earlier incarnation's value; a Session subclass's `report(error, info)` receives the reports of its watches and observers. <!-- fix: 32fe8c41e7 -->
