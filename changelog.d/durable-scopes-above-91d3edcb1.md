### Fixed

- Durable: a task change deep in a long ownership chain no longer costs time proportional to the chain's depth: finding the conversations whose idleness a change can affect starts at the task's own conversation, as Pi does (Pi 628d8ab52ce2), instead of walking every owner task up to the conversation. <!-- fix: 91d3edcb1c07b12434de3b63d20da1be412d0dad -->
