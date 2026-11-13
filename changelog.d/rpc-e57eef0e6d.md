### Fixed

- The RPC usage probe that checks Pi's stdout ordering no longer dies on an unhandled `write EPIPE` when the CLI under test stops reading its stdin before the probe sends the prompt; it reports that the CLI wrote no frame, and any other failure to write the prompt still fails it. <!-- fix: e57eef0e6d -->
