### Fixed

- A Chord HTTP connection kept after a host, relay or socket handler it was admitted by was removed and then restored between its upgrade's router check and its keep stayed open, though Pi ends it when the removal arrives. Every router delivered after the check now judges the connection, from the router the check read, so the removal ends it. A router delivered between the check's count and its read no longer ends a connection the router it read admits once that router's delivery is listed. <!-- fix: 59e2efb01257af6ef172706ce726914ec9f58428 -->
