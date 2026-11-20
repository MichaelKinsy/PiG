### Fixed

- A session whose model has no known context window (a model an extension registers without a `contextWindow`, or one an embedding program builds without `ContextWindow`) no longer compacts its history after every reply: a window of 0 is unknown, not used up, so the threshold check is skipped for it and a known window is checked as before. PiG had compacted after every reply for such a model since 0.2.0, which cost a summariser request and the history of each turn; Pi still does (D660). Fix from #548 by @Cpt-Falcon. <!-- fix: 87ea4f926e95b30091295ba946c1716e3d8cb260 -->
