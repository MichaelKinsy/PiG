;;;; SPDX-License-Identifier: MIT
;;;; Pi's ProjectTrustStore and hasTrustRequiringProjectResources (core/trust-manager.ts). The host owns the store, so a decision saved here is the
;;;; one the project trust gate reads. A decision is T, :FALSE or NIL (undecided).

(in-package #:pig)

(defstruct (project-trust-store (:include graph-object) (:conc-name trust-store-) (:constructor %make-trust-store (ctx handle))))

(defun make-project-trust-store (ctx agent-dir)
  "new ProjectTrustStore(agentDir): the decisions saved in AGENT-DIR/trust.json."
  (%make-trust-store ctx (graph-new ctx "ProjectTrustStore" "new" agent-dir)))

(def-graph-methods "trust-store"
  (get (cwd)) (get-entry (cwd)) (set (cwd decision)) (set-many (decisions)))

(defun has-trust-requiring-project-resources (ctx cwd)
  "Whether CWD holds project-local resources that project trust gates (entries of its configuration directory, or .agents/skills in CWD or an ancestor other than the user's)."
  (graph-bool (graph-static ctx "ProjectTrustStore" "hasTrustRequiringProjectResources" cwd)))
