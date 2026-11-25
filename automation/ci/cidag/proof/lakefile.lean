-- SPDX-License-Identifier: MIT
import Lake
open Lake DSL

package cidag_proof where
  -- Standalone Lean 4 proof package (pure Lean core, zero external dependencies).

@[default_target]
lean_lib SelectionSoundness where
  roots := #[`SelectionSoundness]
