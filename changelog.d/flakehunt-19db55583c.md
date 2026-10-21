### Fixed

- CI: `TestNodeCellAddedMembersStayWithinTheirOwnState` no longer fails under load. It compared the RSS of a one-member Node cell with the RSS of a five-member cell against 6 MiB per added member; one member's RSS read 131.8 to 152.2 MiB (6.4 to 27.5 MiB below its own peak) in 19 runs, so the figure ranged 0.8 to 7.2 MiB and failed once. The probe now reads the retained V8 heap plus external memory right after the forced collection, which gives 0.20 to 0.25 MiB per added member in every run, and the budget is 2 MiB. <!-- fix: 19db55583c -->
