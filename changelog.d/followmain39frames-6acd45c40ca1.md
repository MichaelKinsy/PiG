### Fixed

- chord: a watched derive that was read between two changes of its source in one commit published two frames, the first diffed from the value read in between; over a link that was the protocol error `member twice in one state`, which closed the link (Pi f6600b58f347). It publishes one frame per commit now, diffed from the value before the commit. A failure read in between and again at the end now reaches its watches. <!-- fix: 6acd45c40ca1 -->
