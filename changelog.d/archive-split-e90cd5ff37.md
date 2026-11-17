### Fixed

- Importing an archive that Pi made now keeps the file index: a worker's files of an archive Pi exported arrive with their versions, folders and summary, where only the file bytes came before and the Go `Files` facet listed nothing. `CopyFiles` writes its copy's index where the `Files` facet reads it. <!-- fix: e90cd5ff37f4478cc01cd64acc70ef8fb1c79838 --> <!-- cause: PiG keeps the kit's Files tables in a second store beside the plugins' but imported Pi's single store archive whole into the plugins' store, and no test ran an archive Pi itself made through the import -->
