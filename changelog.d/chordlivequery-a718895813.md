### Added

- Chord: live queries over an object's own tables (Pi main `2439933cf`): `self.Query(table, q, limit)` is a watchable of up to `limit` rows, run before the object goes live and again in the commit step of every commit of the object that writes the table, publishing only the diff in that commit's batch. A failing run makes the query a failed member and is reported once as `query_failed`. <!-- feat: 2439933cf -->
