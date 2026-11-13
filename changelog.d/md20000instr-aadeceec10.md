### Changed

- The context estimate counts the arguments of a tool call whose members are ASCII strings (a write or an edit) from their bytes instead of encoding them. A turn of a session that wrote files, with a provider that reports no usage, no longer encodes the text of every earlier write three times: counting the arguments of a 200 KB write takes 0.07 ms instead of 0.48 ms and allocates nothing instead of 238 KB. <!-- change: aadeceec10 -->
