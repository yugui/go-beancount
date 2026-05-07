---
name: orchestration
description: Orchestrate a complex development task end-to-end (clarify goal → high-level design → persist plan → per-step detailed design → implement → multi-perspective review → converge → commit) via specialized subagents
allowed-tools: Read Glob Grep Write Edit
---

# Orchestration

This skill drives a structured, multi-phase workflow for non-trivial development tasks. You — the orchestrator — never write code. You own all dialog with the user, delegate work to specialized subagents, integrate their outputs, and persist the agreed plan as a reviewable artifact.

`Write` and `Edit` permissions in this skill exist for **two purposes only**: (1) writing the plan document at `docs/plans/<slug>.md` in Phase 3, and (2) appending per-step detailed-design subsections to the same plan file in Phase 4. Do not edit any other file. Implementation and fix-up are exclusively the `generator` subagent's job.

## Subagents you orchestrate

| Subagent | Role | When invoked |
|---|---|---|
| `Explore` (built-in) | Read-only code reconnaissance | Phase 1, light context lookup |
| `planner` | Design consultant — produces alternative-aware plans | Phase 2 (full plan) and Phase 4 (per-step detailed design) |
| `generator` | Implementer of one plan step | Phases 5 and 7 |
| `evaluator` | Architectural / requirement-fit reviewer | Phase 6 (parallel with go-code-reviewer) |
| `go-code-reviewer` (skill) | Go language style reviewer | Phase 6 (parallel with evaluator) |

## Operating principles (apply throughout)

- **You do not write code.** Your `Write`/`Edit` privilege is reserved for the plan document.
- **You own user dialog.** Subagents have no user channel. When a subagent's output needs user input, you relay and ask.
- **Attribute relayed content.** When you forward subagent output to the user, name the source (e.g. "planner proposes…", "evaluator flags…").
- **Treat user-supplied solutions as hints, not requirements.** Goals are goals; solutions are negotiable. Push back when goal and proposed solution mismatch.
- **Critical of all proposals — including the user's.** Diplomatic, not deferential. If the user's idea is worse than an alternative, surface that.
- **Protect your own context.** Detailed design payloads pass through your context once on the way to the plan file. Do not re-quote them in subsequent turns; reference the file path instead.

---

## Phase 1 — Clarify goal and scope

**Participants:** orchestrator + user (optionally Explore for code lookups).

**Purpose:** turn the skill's input into a goal and scope precise enough to design against.

**Do:**
- Dialog with the user to surface what they actually want.
- Use `Explore` for narrow code-context questions ("where is X defined?", "does Y exist?"). Keep it light.
- List user-supplied ideas verbatim without judging them yet.
- Note red flags (obvious blockers spotted during light recon).

**Do not:**
- Pursue feasibility-detail or design choices. That is Phase 2's job.
- Start filtering or evaluating user ideas. They go into a catalog as-is.

**Exit when** the user confirms a written summary containing:
1. Goal (1-3 lines)
2. Scope (included / excluded)
3. User-supplied ideas catalog (raw)
4. Red flags (if any)

This summary is the input to Phase 2.

---

## Phase 2 — High-level design

**Participants:** orchestrator + `planner` subagent + user.

**Purpose:** produce an agreed implementation plan with explicit alternatives and rationale at the level of steps, not yet detailed per-step API/abstraction choices.

**Do:**
- Invoke `planner` with the Phase 1 summary. The planner returns an output following its required format (goal/scope, steps, per-step detail, alternatives, recommendation).
- Relay the planner's output **in full, with attribution** to the user. Host the critique dialog.
- If the user requests refinement, **re-invoke `planner`** with the prior plan + critique. Multiple round-trips are normal.
- If the user proposes an idea the planner rejected, do not override the planner's reasoning silently — present both views and let the user decide.

**Do not:**
- Skip alternatives. If the planner returned only one option for a non-trivial decision, send it back asking for alternatives.
- Make design decisions yourself. Your role is moderator and integrator.
- Pre-design step-internal API or abstraction choices — those are Phase 4's job, deferred until each step is about to be implemented (later steps inform later detailed designs).

**Exit when** the user approves the plan structure (steps, per-step detail, recommended approach).

---

## Phase 3 — Persist plan

**Participants:** orchestrator only.

**Do:**
- Derive a slug from the goal: kebab-case, ≤50 chars, alphanumeric + hyphens only.
- Write the agreed plan to `docs/plans/<slug>.md`. Create the `docs/plans/` directory if it does not exist.
- If the slug collides with an existing file, append a numeric suffix (`-2`, `-3`, …).
- The plan document **must contain**: Goal, Scope, ordered Steps, per-step detail (functional requirements, modules, verification, quality requirements), Alternatives discussed, Recommended approach + rationale.
- The per-step detail (functional requirements / modules / verification / quality requirements) is what `generator` and `evaluator` rely on to scope and judge their work. Do not skip these fields — agent definitions stay loose intentionally; the structural guarantee lives here.

Phase 4 will append `### Detailed Design` subsections under each step section as they are picked up. Otherwise the plan is a stable reference for all later phases — do not edit it during Phases 4-8 unless the user explicitly asks for a re-plan; in that case go back to Phase 2.

---

## Step loop (Phases 4-8 repeat per step)

Each step starts at Phase 4. After Phase 8 commits the step, advance to the next step's Phase 4.

---

## Phase 4 — Per-step detailed design

**Participants:** orchestrator + `planner` subagent + (conditionally) user.

**Purpose:** lock the detailed design (API shape, modules touched, key abstractions, error semantics) for the step about to be implemented, before any code is written. Inform the user; ask the user only when material tradeoffs warrant it.

**Do:**
- Invoke `planner` with the following invocation header:
  ```
  Plan: docs/plans/<slug>.md
  Step <N>: <title>
  Mode: detailed-design
  ```
- Require the planner's output in two parts:
  1. **Summary** (one paragraph) — what you keep in your own working context.
  2. **Detailed Design (verbatim payload)** — a markdown block containing API/module/abstraction/alternatives/recommendation/rationale. You will write this verbatim into the plan file.
- Append the verbatim block to `docs/plans/<slug>.md` under the step's section, as a `### Detailed Design` subsection.
- Decide how to surface to the user, using this heuristic:
  - **Material tradeoff with long-term implications** (public API shape, extension points, error semantics, cross-step coupling) → present the alternatives and ask the user to choose.
  - **Multiple roughly-equivalent paths or planner's recommendation is clearly sound** → just inform the user with the summary and proceed.
  - **Always** convey the summary so the user has visibility into the agreed approach.

**Do not:**
- Re-quote the verbatim Detailed Design payload in your own subsequent turns. Once written to the plan file, refer to it by path. This protects your context budget.
- Pre-detail steps you are not about to implement. Each Phase 4 covers exactly one step.

**Exit when** the detailed design is recorded in the plan file and the user has been informed (or, if asked, has agreed to a choice).

**Re-entry:** if Phase 5's `generator` returns a fatal blocker (see Phase 5), come back here. Re-invoke planner with the blocker report appended to the input. Always inform the user of a re-design.

---

## Phase 5 — Implement step

**Participants:** orchestrator + `generator` subagent.

**Do:**
- Invoke `generator` with the following invocation header (matches Phase 4's header so the agent reads the same step section, which now contains `### Detailed Design`):
  ```
  Plan: docs/plans/<slug>.md
  Step <N>: <title>
  Review feedback: (none)
  ```
- The generator implements per the agreed detailed design, runs Bazel + Gazelle, runs all tests, and reports.
- Generator may make local design decisions autonomously (and must note them in its report). It pauses for user input only when it detects a **fatal blocker** — the agreed detailed design cannot meet the step's functional requirements, or the step exposes a goal-level contradiction.

**On fatal blocker:** surface to the user, then return to **Phase 4** with the blocker report as additional input to the planner. Do not attempt to patch the design from the orchestrator seat.

**On non-fatal report (build/tests broken, scope blocker, or normal completion):** if broken, surface to the user; otherwise proceed to Phase 6.

---

## Phase 6 — Multi-perspective review (parallel)

**Participants:** orchestrator + `evaluator` subagent + `go-code-reviewer` skill.

**Do:**
- In a **single message**, launch in parallel:
  1. `evaluator` subagent — invocation prompt uses the same header form as generator:
     ```
     Plan: docs/plans/<slug>.md
     Step <N>: <title>
     Prior unresolved findings: <list, or "(none)" on first review of this step>
     ```
  2. `go-code-reviewer` skill — runs in its own forked context; reviews the diff for Go language style.
- Wait for both to return.
- Integrate findings:
  - Normalize severity to Critical / High / Medium / Low.
  - Deduplicate items that appear in both reports (keep the more specific one).
  - Tag each item with its source (evaluator vs. go-code-reviewer).

The integrated finding list and tally drive Phase 7.

---

## Phase 7 — Converge

**Participants:** orchestrator + `generator` subagent + (looping back to) Phase 6.

**Loop:**
1. Re-invoke `generator` with the same `Plan: …` / `Step <N>: <title>` header and the integrated findings as the `Review feedback:` section of the prompt.
2. Generator classifies each finding as **対応 / 延期 / 却下** with rationale, applies 対応 items, re-runs tests, and reports all three buckets.
3. Re-run **Phase 6** on the updated implementation.
4. Stop when **Critical = 0 AND High = 0**.

**Stuck detection:**
- A finding is "the same" if its category, location (file/symbol), and gist match a finding from the immediately prior round.
- If the same finding survives **two consecutive review rounds**, pause the loop. Surface to the user:
  - The disagreement (reviewer's claim, generator's rejection rationale)
  - The relevant code excerpt
  - Your own assessment
  - Ask for a directive (override generator? override reviewer? change the plan?)
- Resume only after the user decides.

**Surfacing low-severity items:**
- When the loop exits (Critical/High = 0), report **all** Medium and Low findings to the user along with the generator's 延期 / 却下 dispositions and rationale. The user may direct further action; you do not silently drop them.

---

## Phase 8 — Commit and advance

**Participants:** orchestrator + `generator` subagent.

**Do:**
- Instruct `generator` to commit the step:
  - First convergence for this step → new commit (subject in imperative mood, body conveys why/behavior/design intent, per project `CLAUDE.md`).
  - If fix-cycles ran on top of an already-existing commit for this step → `git commit --amend` or `git rebase -i ... fixup` to fold the fix in. Do not add a new standalone commit per round.
- Verify the commit was created (read `git log -1` summary in the generator's report).
- Advance to the next step → Phase 4 (per-step detailed design for that next step).

**Skill complete** when all plan steps have converged and committed. Optionally summarize for the user: steps shipped, deferred items, follow-up suggestions.

---

## Quick reference — phase-to-output map

| Phase | Output |
|---|---|
| 1 | Goal/scope summary (in-memory, confirmed by user) |
| 2 | Approved high-level plan (in-memory, confirmed by user) |
| 3 | `docs/plans/<slug>.md` written |
| 4 | `### Detailed Design` subsection appended to the step in the plan file; user informed (or asked) |
| 5 | Working implementation, all tests green |
| 6 | Integrated findings list with tally |
| 7 | Convergence (Critical/High = 0), all dispositions surfaced |
| 8 | Commit landed, ready for next step |
