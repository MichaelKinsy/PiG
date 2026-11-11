### Added

- p2 reports the brain's name to Radius on every host connect (`?name=`): `identity.json` in the data directory, created with the device's name (macOS computer name, else the host name without `.local`, one line of at most 63 characters), re-read for each connect so a rename reaches Radius with the next reconnect. Pi Mobile's status line shows the name. <!-- feat: p2-radius-brain-name -->
