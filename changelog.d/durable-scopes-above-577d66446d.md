### Fixed

- Durable: a task change deep in a long ownership chain no longer costs time proportional to the chain's depth: finding the conversations whose idleness a change can affect starts at the task's own conversation, as Pi does (Pi 628d8ab52ce2), instead of walking every owner task up to the conversation. <!-- fix: 577d66446d30694aab1c06fc85bb2814fc60da4c -->
