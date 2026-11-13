### Fixed

- Resource reload: an override function (skills, prompt templates, themes, extensions, context files, system prompt) that calls the resource loader, and a project-trust callback or extension load that calls `ExtendResources` or `Close`, no longer block forever on the loader's operation lock; the call runs inside the reload in progress, as Pi's unlocked reload does. Reloads that overlap from outside still wait for each other (D632). <!-- fix: 68dfec5e1afa801b31ee0ad11a3304fc26800f22 -->
