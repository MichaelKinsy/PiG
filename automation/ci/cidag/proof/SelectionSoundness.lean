-- SPDX-License-Identifier: MIT
--
-- Formal soundness proof for the PiG CI affected-package DAG selector and
-- tiering lattice (`automation/ci/cidag`).
--
-- Model:
--   * Pure Lean 4 core (no Mathlib dependency), zero `sorry`, zero axioms.
--   * Models file ownership (rule table + Go compile/test-only edges + unowned),
--     reverse transitive closure of package compile dependencies, global-invariant
--     gate lock-in, fail-closed unowned / max-file ceiling, two-level tiering
--     lattice (`pr ≤ full`), and `label` mode required-check semantics.
--
-- Five machine-checkable theorems:
--   1. `monotonicity`                : S₁ ⊆ S₂ → plan(S₁) ⊆ plan(S₂)
--   2. `transitive_closure_soundness`: compile-edge pkg p ∈ S ∧ Dep*(q, p) → q ∈ selected(S)
--   3. `fail_closed_completeness`    : (∃ f ∈ S, unowned f) ∨ |S| > maxFiles → plan(S) = Full
--   4. `global_invariant_lock_in`    : nonDocsOnly S → GlobalGates ⊆ plan(S)
--   5. `tiering_preservation`        : plan(S, pr) ⊆ plan(S, full) ∧
--                                      ciResultPasses(label, light) = false

namespace PiG.CIDAG

/-- Two-level tiering lattice: `pr ≤ full`. -/
inductive Tier where
  | pr
  | full
  deriving DecidableEq, Repr

/-- Lattice ordering on tiers (`pr ≤ pr`, `pr ≤ full`, `full ≤ full`). -/
def Tier.le : Tier → Tier → Prop
  | .pr,   _     => True
  | .full, .full => True
  | .full, .pr   => False

/-- Pull-request light-run mode (`PIG_PR_CI_LIGHT`). -/
inductive LightMode where
  | none
  | queue
  | label
  deriving DecidableEq, Repr

/-- Result of matching a repository file against the rule table + Go graph. -/
inductive FileOwnership (Pkg Lane : Type) where
  | docs                                   -- project prose / metadata only
  | forceFull                              -- CI definition, pins, go.mod, etc.
  | ruleLanes (lanes : Lane → Prop)        -- explicit rule-table lanes
  | compilePkg (p : Pkg)                   -- production .go / EmbedFiles / prod ref
  | testOnlyPkg (p : Pkg)                  -- _test.go / TestEmbedFiles / testdata / test ref
  | unowned                                -- no rule and no Go package owns this file

/-- Reflexive-transitive closure of direct package compile dependency `dep q p`
    ("package `q` directly imports package `p`"). -/
inductive Reach {Pkg : Type} (dep : Pkg → Pkg → Prop) : Pkg → Pkg → Prop where
  | refl (p : Pkg) : Reach dep p p
  | step {q r p : Pkg} : dep q r → Reach dep r p → Reach dep q p

theorem Reach.trans {Pkg : Type} {dep : Pkg → Pkg → Prop} {a b c : Pkg}
    (hab : Reach dep a b) (hbc : Reach dep b c) : Reach dep a c := by
  induction hab with
  | refl _ => exact hbc
  | step hqr _ ih => exact Reach.step hqr (ih hbc)

/-- Abstract CI DAG configuration parameterized by file, package, and lane types. -/
structure Config (File Pkg Lane : Type) where
  /-- Ownership classification of every repository path. -/
  owner : File → FileOwnership Pkg Lane
  /-- Direct compile dependency relation: `dep q p` means `q` depends on `p`. -/
  dep : Pkg → Pkg → Prop
  /-- Lanes triggered when a package is in the affected set. -/
  pkgLanes : Pkg → Lane → Prop
  /-- Global fail-fast gate lanes locked in for every non-docs-only change. -/
  globalGates : Lane → Prop
  /-- Tier assigned to each lane (`pr` or `full`). -/
  laneTier : Lane → Tier
  /-- Maximum number of changed files before failing closed to `Full`. -/
  maxFiles : Nat

/-- Predicate for a file being unowned under configuration `cfg`. -/
def isUnowned {File Pkg Lane : Type} (cfg : Config File Pkg Lane) (f : File) : Prop :=
  match cfg.owner f with
  | .unowned => True
  | _ => False

/-- Predicate for a file triggering a full plan via the rule table. -/
def isForceFull {File Pkg Lane : Type} (cfg : Config File Pkg Lane) (f : File) : Prop :=
  match cfg.owner f with
  | .forceFull => True
  | _ => False

/-- Predicate for a file being non-docs-only. -/
def isNonDocs {File Pkg Lane : Type} (cfg : Config File Pkg Lane) (f : File) : Prop :=
  match cfg.owner f with
  | .docs => False
  | _ => True

/-- Predicate for a change set `S` (represented as a finite list of files) being
    non-docs-only: at least one file in `S` is not a docs-only file. -/
def nonDocsOnly {File Pkg Lane : Type} (cfg : Config File Pkg Lane) (S : List File) : Prop :=
  ∃ f ∈ S, isNonDocs cfg f

/-- Direct compile-edge packages modified by `S`. -/
def compilePkgs {File Pkg Lane : Type} (cfg : Config File Pkg Lane) (S : List File) (p : Pkg) : Prop :=
  ∃ f ∈ S, cfg.owner f = .compilePkg p

/-- Direct test-only packages modified by `S`. -/
def testOnlyPkgs {File Pkg Lane : Type} (cfg : Config File Pkg Lane) (S : List File) (p : Pkg) : Prop :=
  ∃ f ∈ S, cfg.owner f = .testOnlyPkg p

/-- Affected package set: reverse transitive closure of compile-edge packages
    union test-only packages. -/
def selectedPkgs {File Pkg Lane : Type} (cfg : Config File Pkg Lane) (S : List File) (q : Pkg) : Prop :=
  (∃ p, compilePkgs cfg S p ∧ Reach cfg.dep q p) ∨ testOnlyPkgs cfg S q

/-- Fail-closed full-run condition: an unowned file, a force-full rule file, or
    a change set strictly larger than `cfg.maxFiles`. -/
def isFull {File Pkg Lane : Type} (cfg : Config File Pkg Lane) (S : List File) : Prop :=
  (∃ f ∈ S, isUnowned cfg f) ∨
  (∃ f ∈ S, isForceFull cfg f) ∨
  S.length > cfg.maxFiles

/-- Rule-selected lanes directly requested by matched files in `S`. -/
def ruleSelectedLanes {File Pkg Lane : Type} (cfg : Config File Pkg Lane) (S : List File) (l : Lane) : Prop :=
  ∃ f ∈ S, match cfg.owner f with
    | .ruleLanes ls => ls l
    | _ => False

/-- Graph-selected lanes triggered by affected packages of `S`. -/
def graphSelectedLanes {File Pkg Lane : Type} (cfg : Config File Pkg Lane) (S : List File) (l : Lane) : Prop :=
  ∃ q, selectedPkgs cfg S q ∧ cfg.pkgLanes q l

/-- Unpruned (full-tier) affected lane set before projecting onto `pr`. -/
def affectedLanes {File Pkg Lane : Type} (cfg : Config File Pkg Lane) (S : List File) (l : Lane) : Prop :=
  isFull cfg S ∨
  (nonDocsOnly cfg S ∧ cfg.globalGates l) ∨
  ruleSelectedLanes cfg S l ∨
  graphSelectedLanes cfg S l

/-- Full set of all lanes (top element of the lane-set lattice). -/
def FullPlan {Lane : Type} : Lane → Prop :=
  fun _ => True

/-- Tiered plan selector:
    * For `Tier.full`, runs every lane in `affectedLanes cfg S`.
    * For `Tier.pr`, runs every lane in `affectedLanes cfg S` whose tier is `pr`
      (or all lanes when `isFull cfg S`, plus locked-in global gates when non-docs). -/
def plan {File Pkg Lane : Type} (cfg : Config File Pkg Lane) (S : List File) (t : Tier) (l : Lane) : Prop :=
  match t with
  | .full => affectedLanes cfg S l
  | .pr   =>
      affectedLanes cfg S l ∧
      (isFull cfg S ∨ cfg.globalGates l ∨ cfg.laneTier l = .pr)

/-- List subset relation `S₁ ⊆ S₂` (every element of `S₁` appears in `S₂`), paired
    with cardinality monotonicity `S₁.length ≤ S₂.length` (automatically satisfied
    for deduplicated change sets where `S₁ ⊆ S₂`). -/
def ChangeSubset {File : Type} (S₁ S₂ : List File) : Prop :=
  (∀ f, f ∈ S₁ → f ∈ S₂) ∧ S₁.length ≤ S₂.length

/-- Lane-set inclusion `P₁ ⊆ P₂`. -/
def LaneSubset {Lane : Type} (P₁ P₂ : Lane → Prop) : Prop :=
  ∀ l, P₁ l → P₂ l

/-- Lane-set extensional equality. -/
def LaneEq {Lane : Type} (P₁ P₂ : Lane → Prop) : Prop :=
  ∀ l, P₁ l ↔ P₂ l

-- ============================================================================
-- Helper monotonicity lemmas
-- ============================================================================

theorem isFull_mono {File Pkg Lane : Type} (cfg : Config File Pkg Lane)
    {S₁ S₂ : List File} (hsub : ChangeSubset S₁ S₂)
    (h : isFull cfg S₁) : isFull cfg S₂ := by
  rcases h with ⟨f, hf, hu⟩ | ⟨f, hf, hff⟩ | hlen
  · exact Or.inl ⟨f, hsub.1 f hf, hu⟩
  · exact Or.inr (Or.inl ⟨f, hsub.1 f hf, hff⟩)
  · exact Or.inr (Or.inr (Nat.lt_of_lt_of_le hlen hsub.2))

theorem nonDocsOnly_mono {File Pkg Lane : Type} (cfg : Config File Pkg Lane)
    {S₁ S₂ : List File} (hsub : ChangeSubset S₁ S₂)
    (h : nonDocsOnly cfg S₁) : nonDocsOnly cfg S₂ := by
  rcases h with ⟨f, hf, hnd⟩
  exact ⟨f, hsub.1 f hf, hnd⟩

theorem selectedPkgs_mono {File Pkg Lane : Type} (cfg : Config File Pkg Lane)
    {S₁ S₂ : List File} (hsub : ChangeSubset S₁ S₂)
    {q : Pkg} (h : selectedPkgs cfg S₁ q) : selectedPkgs cfg S₂ q := by
  rcases h with ⟨p, ⟨f, hf, hown⟩, hreach⟩ | ⟨f, hf, hown⟩
  · exact Or.inl ⟨p, ⟨f, hsub.1 f hf, hown⟩, hreach⟩
  · exact Or.inr ⟨f, hsub.1 f hf, hown⟩

theorem affectedLanes_mono {File Pkg Lane : Type} (cfg : Config File Pkg Lane)
    {S₁ S₂ : List File} (hsub : ChangeSubset S₁ S₂) :
    LaneSubset (affectedLanes cfg S₁) (affectedLanes cfg S₂) := by
  intro l hl
  rcases hl with hfull | ⟨hnd, hg⟩ | ⟨f, hf, hr⟩ | ⟨q, hq, hpl⟩
  · exact Or.inl (isFull_mono cfg hsub hfull)
  · exact Or.inr (Or.inl ⟨nonDocsOnly_mono cfg hsub hnd, hg⟩)
  · exact Or.inr (Or.inr (Or.inl ⟨f, hsub.1 f hf, hr⟩))
  · exact Or.inr (Or.inr (Or.inr ⟨q, selectedPkgs_mono cfg hsub hq, hpl⟩))

-- ============================================================================
-- Theorem 1: Monotonicity
--   S₁ ⊆ S₂ → plan(S₁, t) ⊆ plan(S₂, t) for every tier t.
-- ============================================================================

theorem monotonicity {File Pkg Lane : Type} (cfg : Config File Pkg Lane)
    {S₁ S₂ : List File} (t : Tier)
    (hsub : ChangeSubset S₁ S₂) :
    LaneSubset (plan cfg S₁ t) (plan cfg S₂ t) := by
  intro l hl
  cases t with
  | full =>
    exact affectedLanes_mono cfg hsub l hl
  | pr =>
    rcases hl with ⟨haff, hcond⟩
    refine ⟨affectedLanes_mono cfg hsub l haff, ?_⟩
    rcases hcond with hfull | hg | hpr
    · exact Or.inl (isFull_mono cfg hsub hfull)
    · exact Or.inr (Or.inl hg)
    · exact Or.inr (Or.inr hpr)

-- ============================================================================
-- Theorem 2: Transitive Closure Soundness
--   If package `q` transitively depends on a modified compile-edge package `p`,
--   then `q ∈ selectedPkgs(S)`, and every lane of `q` is in `plan(S, full)`.
-- ============================================================================

theorem transitive_closure_soundness {File Pkg Lane : Type} (cfg : Config File Pkg Lane)
    (S : List File) (p q : Pkg)
    (hmod : compilePkgs cfg S p)
    (hdep : Reach cfg.dep q p) :
    selectedPkgs cfg S q :=
  Or.inl ⟨p, hmod, hdep⟩

theorem transitive_closure_lanes_soundness {File Pkg Lane : Type} (cfg : Config File Pkg Lane)
    (S : List File) (p q : Pkg) (l : Lane)
    (hmod : compilePkgs cfg S p)
    (hdep : Reach cfg.dep q p)
    (hlane : cfg.pkgLanes q l) :
    plan cfg S .full l := by
  exact Or.inr (Or.inr (Or.inr ⟨q, transitive_closure_soundness cfg S p q hmod hdep, hlane⟩))

-- ============================================================================
-- Theorem 3: Fail-Closed Completeness
--   If there exists an unowned file f ∈ S OR |S| > maxFiles, then
--   plan(S, t) = FullPlan for every tier t.
-- ============================================================================

theorem fail_closed_completeness {File Pkg Lane : Type} (cfg : Config File Pkg Lane)
    (S : List File) (t : Tier)
    (hfail : (∃ f ∈ S, isUnowned cfg f) ∨ S.length > cfg.maxFiles) :
    LaneEq (plan cfg S t) FullPlan := by
  have hfull : isFull cfg S := by
    rcases hfail with hu | hlen
    · exact Or.inl hu
    · exact Or.inr (Or.inr hlen)
  intro l
  constructor
  · intro _
    trivial
  · intro _
    cases t with
    | full =>
      exact Or.inl hfull
    | pr =>
      exact ⟨Or.inl hfull, Or.inl hfull⟩

-- ============================================================================
-- Theorem 4: Global-Invariant Lock-In
--   For every non-docs-only change S and every tier t,
--   GlobalGates ⊆ plan(S, t).
-- ============================================================================

theorem global_invariant_lock_in {File Pkg Lane : Type} (cfg : Config File Pkg Lane)
    (S : List File) (t : Tier)
    (hnd : nonDocsOnly cfg S) :
    LaneSubset cfg.globalGates (plan cfg S t) := by
  intro l hgate
  have haff : affectedLanes cfg S l := Or.inr (Or.inl ⟨hnd, hgate⟩)
  cases t with
  | full => exact haff
  | pr   => exact ⟨haff, Or.inr (Or.inl hgate)⟩

-- ============================================================================
-- Theorem 5: Tiering Preservation
--   (a) plan(S, pr) ⊆ plan(S, full) (and generally t₁ ≤ t₂ → plan(S, t₁) ⊆ plan(S, t₂)).
--   (b) In `label` mode, `ciResultPasses` rejects every non-docs light run.
-- ============================================================================

theorem tiering_subset {File Pkg Lane : Type} (cfg : Config File Pkg Lane)
    (S : List File) :
    LaneSubset (plan cfg S .pr) (plan cfg S .full) := by
  intro l hl
  exact hl.1

theorem tiering_lattice_mono {File Pkg Lane : Type} (cfg : Config File Pkg Lane)
    (S : List File) {t₁ t₂ : Tier} (hle : Tier.le t₁ t₂) :
    LaneSubset (plan cfg S t₁) (plan cfg S t₂) := by
  cases t₁ with
  | pr =>
    cases t₂ with
    | pr => intro l hl; exact hl
    | full => exact tiering_subset cfg S
  | full =>
    cases t₂ with
    | pr => exact False.elim hle
    | full => intro l hl; exact hl

/-- Outcome of a pull-request run as evaluated by the `CI result` aggregator job
    (`automation/ci/ci-plan.sh` + `.github/workflows/ci.yml` `result` job):
    * `ranTier` is `.pr` for a light run and `.full` for a full run.
    * `allPlannedGreen` records whether every planned job concluded `success`
      and every unplanned job concluded `skipped`.
    * `isProductChange` is `true` for non-docs changes. -/
def ciResultPasses (mode : LightMode) (ranTier : Tier) (isProductChange : Bool) (allPlannedGreen : Bool) : Bool :=
  if !allPlannedGreen then
    false
  else if !isProductChange then
    true
  else
    match mode with
    | .label => decide (ranTier = .full)
    | .queue => true
    | .none  => decide (ranTier = .full)

theorem label_mode_light_never_passes (allPlannedGreen : Bool) :
    ciResultPasses .label .pr true allPlannedGreen = false := by
  cases allPlannedGreen <;> rfl

theorem tiering_preservation {File Pkg Lane : Type} (cfg : Config File Pkg Lane)
    (S : List File) (allPlannedGreen : Bool) :
    LaneSubset (plan cfg S .pr) (plan cfg S .full) ∧
    ciResultPasses .label .pr true allPlannedGreen = false :=
  ⟨tiering_subset cfg S, label_mode_light_never_passes allPlannedGreen⟩

end PiG.CIDAG
