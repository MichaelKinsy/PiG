### Changed

- The find tool's path patterns are relative to the search directory, like grep's `glob`: `src/*.ts` finds `src/a.ts` but no longer `pkg/src/a.ts`; use `**/src/*.ts` to match at any depth. Patterns without `/` still match file names at any depth (Pi #12116).

### Fixed

- The find tool matched path patterns through folders above the search path: with the project in `~/src/app`, `src/**/*.ts` returned every `.ts` file in the project (Pi #12066). <!-- fix: f0c8b9662f -->
