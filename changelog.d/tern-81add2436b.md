### Fixed

- A compiled-in extension factory, inline or built in, no longer panics "Extension runtime not initialized" when it calls `pi.sendMessage`, `appendEntry`, `setSessionName`, `getSessionName`, `setModel`, `setThinkingLevel` or `getThinkingLevel` under the interactive mode. The mode rebinds its in-process runner after the Session binds it and replaced the Session's actions with three of them; it now binds every action Pi's `_bindExtensionCore` binds, through the code the wire host actions of the same names run. <!-- fix: 81add2436b -->
