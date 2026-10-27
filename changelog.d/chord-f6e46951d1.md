### Fixed

- Schemas decoded from a wire description (a Node child's contracts, a hold token) keep their declared property order, so a failed args, result, state or input check lists its errors in that order as Pi does, and a description is written back in the declared order; they were sorted by name. <!-- fix: f6e46951d1 -->
