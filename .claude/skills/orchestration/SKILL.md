---
name: orchestration
description: Orchestrate a complex development task end-to-end (clarify goal → high-level design → persist plan → per-step detailed design → implement → multi-perspective review → converge → commit) via specialized subagents
allowed-tools: Read Glob Grep Write Edit ExitPlanMode
---

# Orchestration

This skill drives a structured, multi-phase workflow for non-trivial development tasks. You — the orchestrator — never write code. You own all dialog with the user, delegate work to specialized subagents, integrate their outputs, and persist the agreed plan as a reviewable artifact.

`Write` and `Edit` permissions in this skill exist for **three purposes only**: (1) writing the plan document at `docs/plans/<slug>.md` in Phase 3, (2) appending per-step detailed-design subsections to the same plan file in Phase 4, and (3) under plan-mode entry, building up the plan in the harness-designated plan file (whose path the plan-mode preamble names; varies by environment) across Phases 1 and 2 — that path is the only writable target while plan mode is active. Do not edit any other file. Implementation and fix-up are exclusively the `generator` subagent's job.

## Subagents you orchestrate

| Subagent | Role | When invoked |
|---|---|---|
| `Explore` (built-in) | Read-only code reconnaissance | Phase 1, light context lookup |
| `planner` | Design consultant — produces alternative-aware plans | Phase 2 (full plan), Phase 4 (per-step detailed design), Phase 9a (knowledge-migration triage) |
| `generator` | Implementer of one plan step (one session reused across that step's fix-cycles via SendMessage when reachable; fresh spawn with explicit `Fix-cycle context` as fallback) | Phases 5, 7, and 9c (knowledge migration + plan cleanup) |
| `evaluator` | Architectural / requirement-fit reviewer | Phase 6 (parallel with go-code-reviewer) |
| `go-code-reviewer` (skill) | Go language style reviewer | Phase 6 (parallel with evaluator) |

## Operating principles (apply throughout)

- **You do not write code.** Your `Write`/`Edit` privilege is reserved for the plan document.
- **You own user dialog.** Subagents have no user channel. When a subagent's output needs user input, you relay and ask.
- **Attribute relayed content.** When you forward subagent output to the user, name the source (e.g. "planner proposes…", "evaluator flags…").
- **Treat user-supplied solutions as hints, not requirements.** Goals are goals; solutions are negotiable. Push back when goal and proposed solution mismatch.
- **Critical of all proposals — including the user's.** Diplomatic, not deferential. If the user's idea is worse than an alternative, surface that.
- **Protect your own context.** Detailed design payloads pass through your context once on the way to the plan file. Do not re-quote them in subsequent turns; reference the file path instead. The arbiter step in Phase 7 is the one place you read code directly — keep those reads narrow (specific line ranges).
- **Preserve implementer context across fix-cycles when possible.** Try `SendMessage` to the same generator session first (it remembers what it tried and why); on failure — session terminated, not addressable, or error — spawn a fresh `generator` and pass a `Fix-cycle context` payload (current diff, prior findings with their dispositions, one-line notes on prior rounds) so the new session can reconstruct what was tried. Don't split a step across multiple sessions on purpose; the fallback is a recovery path, not the routine.
- **You arbitrate High/Critical rejections; you do not arbitrate Medium/Low.** Generators may finalize Medium/Low rejections autonomously. High/Critical rejections come back to you as `Disputed Findings`; resolve obvious cases, escalate only genuine judgment calls.
- **Tune subagent model per invocation when warranted.** Each subagent declares a default model; you may override it by passing `model: opus` or `model: sonnet` to the Agent tool when invoking. Defaults: `planner` and `evaluator` on opus (design judgment and review depth), `generator` on sonnet (volume implementation work). Override upward to opus when invoking `generator` for a step that touches public API design, when a step is on its 2nd-or-later fix-cycle (subtlety suspected), or when generator self-reported difficulty in a prior round. Override downward to sonnet for `planner`/`evaluator` only when the step is genuinely simple and the default's depth would be wasted.

---

## Plan mode integration

This skill accepts two entry paths:

- **Plan mode entry.** When the harness has activated plan mode (look for a `Plan mode is active` system-reminder, which names the designated plan-file path the harness wants you to write to — the absolute location varies by environment), Phases 1 and 2 are naturally compatible: they are read-only design activities. Use the designated plan file as your incremental scratchpad — it is the **only** path the harness lets you write while plan mode is on. Phase 3 then calls `ExitPlanMode` to request user approval and exit plan mode, after which you copy the agreed plan to `docs/plans/<slug>.md`.
- **Coding mode entry.** When plan mode is not active, Phases 1 and 2 keep the draft plan in your working context (no file write yet). Phase 3 writes directly to `docs/plans/<slug>.md` without invoking `ExitPlanMode`.

Detection: rely on the `Plan mode is active` system-reminder when present, or on the designated plan-file path mentioned in plan-mode preamble. If ambiguous, an early Write attempt that the harness refuses confirms plan mode is on.

Phases 4-9 are identical for both entry paths — once Phase 3 finishes, you are always in coding mode with `docs/plans/<slug>.md` as the persistent plan reference.

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

**Where to record the summary:** in **plan-mode entry**, write the summary as the first section of the harness-designated plan file (path given in the plan-mode preamble) — this is your only writable path until Phase 3. In **coding-mode entry**, keep the summary in your working context.

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

**Where to record the plan:** in **plan-mode entry**, append each planner revision to the harness-designated plan file — overwrite or edit in place across refinement rounds so the file always reflects the latest agreed shape. In **coding-mode entry**, keep the plan in your working context.

**Exit when** the user approves the plan structure (steps, per-step detail, recommended approach).

---

## Phase 3 — Persist plan (and exit plan mode if active)

**Participants:** orchestrator only.

**Do:**

1. **Derive a slug** from the goal: kebab-case, ≤50 chars, alphanumeric + hyphens only. If the slug collides with an existing file under `docs/plans/`, append a numeric suffix (`-2`, `-3`, …).

2. **Branch on entry path:**

   - **Plan-mode entry:**
     1. Load the `ExitPlanMode` schema once: `ToolSearch` with query `select:ExitPlanMode` (it is a deferred tool).
     2. Finalize the harness-designated plan file so its contents are the exact plan to persist. The plan document **must contain**: Goal, Scope, ordered Steps, per-step detail (functional requirements, modules, verification, quality requirements), Alternatives discussed, Recommended approach + rationale.
     3. Call `ExitPlanMode`. The tool reads the designated file and presents it to the user for approval; it does not take the plan content as a parameter.
     4. After approval (plan mode is now off, Write is allowed for any path), create `docs/plans/` if it does not exist and Write the designated file's contents to `docs/plans/<slug>.md`. The designated file itself is harness-managed — leave it alone.

   - **Coding-mode entry:**
     1. Create `docs/plans/` if it does not exist.
     2. Write the agreed plan (held in your working context) to `docs/plans/<slug>.md`. Same required sections as above.
     3. Do **not** call `ExitPlanMode` — plan mode is not active.

3. The per-step detail (functional requirements / modules / verification / quality requirements) is what `generator` and `evaluator` rely on to scope and judge their work. Do not skip these fields — agent definitions stay loose intentionally; the structural guarantee lives here.

Phase 4 will append `### Detailed Design` subsections under each step section as they are picked up. Otherwise the plan is a stable reference for all later phases — do not edit it during Phases 4-8 unless the user explicitly asks for a re-plan; in that case go back to Phase 2.

---

## Step loop (Phases 4-8 repeat per step)

Each step starts at Phase 4. After Phase 8 commits the step, advance to the next step's Phase 4. After Phase 8 commits the **last** step, advance to Phase 9 (knowledge migration + cleanup) instead.

---

## Phase 4 — Per-step detailed design

**Participants:** orchestrator + `planner` subagent + (conditionally) user.

**Purpose:** lock only the **Contract** (externally observable surface) for the step about to be implemented. Internal abstractions stay advisory — the implementer discovers them better than the designer can.

**Skip condition (decide first):** Phase 4 is optional. Skip it and go directly to Phase 5 when **all** of the following hold:
- The step does not change any public API or exported type, OR the public surface is fully pinned by the Phase 2 plan already.
- No other step depends on internals of this step (no cross-step coupling to design).
- Error semantics and behaviors observable to callers are already specified at a level the generator can implement without further design.

If you skip, briefly inform the user ("Step N: skipping detailed design — internal-only / Contract already pinned. Proceeding to implementation.") and proceed to Phase 5. **When in doubt, do not skip** — running Phase 4 is cheap; skipping a step that needed it is not.

**When running Phase 4, do:**
- Invoke `planner` with the following invocation header:
  ```
  Plan: docs/plans/<slug>.md
  Step <N>: <title>
  Mode: detailed-design
  ```
- Require the planner's output in two parts:
  1. **Summary** (one paragraph) — what you keep in your own working context.
  2. **Detailed Design (verbatim payload)** — a markdown block with **two explicit layers** plus alternatives and recommendation:
     - `#### Contract` — locked: public API, error semantics, cross-step coupling. The generator must hit this exactly.
     - `#### Suggested Internals` — advisory: module-internal abstractions and decomposition, presented with alternatives where non-trivial. The generator is free to adopt, modify, or replace based on what it discovers while coding.
     - Followed by Alternatives discussed, Recommendation + rationale.
- Append the verbatim block to `docs/plans/<slug>.md` under the step's section, as a `### Detailed Design` subsection.
- If the planner's `#### Contract` layer is bloated with internal decomposition that does not affect external surface, **send it back** asking the planner to move those items into `#### Suggested Internals` or drop them. Over-locked internals are a known quality drag.
- Decide how to surface to the user, using this heuristic:
  - **Material Contract-level tradeoff with long-term implications** (public API shape, extension points, error semantics, cross-step coupling) → present the alternatives and ask the user to choose.
  - **Internals-only tradeoff or planner's recommendation is clearly sound** → just inform the user with the summary and proceed. Do not consult the user on internal-abstraction choices — those are the generator's territory.
  - **Always** convey the summary so the user has visibility into the agreed approach.

**Do not:**
- Re-quote the verbatim Detailed Design payload in your own subsequent turns. Once written to the plan file, refer to it by path. This protects your context budget.
- Pre-detail steps you are not about to implement. Each Phase 4 covers exactly one step.
- Accept a planner output where `#### Contract` and `#### Suggested Internals` are not clearly separated. The distinction is what gives the generator room to do good work.

**Exit when** the detailed design is recorded in the plan file (or skip condition was met) and the user has been informed (or, if asked, has agreed to a choice).

**Re-entry:** if Phase 5's `generator` returns a fatal blocker (see Phase 5), come back here. Re-invoke planner with the blocker report appended to the input. Always inform the user of a re-design.

---

## Phase 5 — Implement step

**Participants:** orchestrator + `generator` subagent.

**Do:**
- Spawn a **new** `generator` agent via the Agent tool for this step. Record its agent id/name — Phase 7 will attempt SendMessage to it first for fix-cycles, and fall back to a fresh spawn with `Fix-cycle context` if SendMessage is no longer reachable (see Phase 7). Each step gets its own generator session; do not reuse a generator across steps.
- Invoke with the following invocation header (matches Phase 4's header so the agent reads the same step section, which now contains `### Detailed Design`):
  ```
  Plan: docs/plans/<slug>.md
  Step <N>: <title>
  Review feedback: (none)
  ```
- The generator implements per the agreed detailed design (Contract is locked; Suggested Internals are advisory), runs Bazel + Gazelle, runs all tests, performs its self-simplify pass, and reports.
- Generator may make local design decisions autonomously (and must note them in its report). It pauses for user input only when it detects a **fatal blocker** — the agreed detailed design cannot meet the step's functional requirements, or the step exposes a goal-level contradiction.

**On fatal blocker:** surface to the user, then return to **Phase 4** with the blocker report as additional input to the planner. Do not attempt to patch the design from the orchestrator seat. When you return to Phase 5 after re-design, try SendMessage to the same generator session with the updated Detailed Design first — its prior exploration context is still useful — and fall back to a fresh spawn (with the updated design and a brief "prior blocker: …" note in the prompt) if SendMessage is no longer reachable.

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

**Participants:** orchestrator + `generator` subagent (continued via SendMessage when reachable, fresh spawn with `Fix-cycle context` as fallback) + (looping back to) Phase 6.

**Generator continuity (important):**
- **First, try `SendMessage` to the same generator session** spawned in Phase 5 (or in this step's most recent successful fix-cycle). When it works, this preserves the implementer's context — what was tried, what was rejected and why, the shape of the code as actually written — so the fix-cycle does not have to rebuild that understanding from the diff.
- **If SendMessage fails** (session no longer addressable, returns an error, or the runtime reports the agent terminated), fall back to spawning a **fresh `generator`** with an explicit `Fix-cycle context` payload (see step 1 below). The fix-cycle workflow in the generator's agent definition accepts both invocation shapes — what matters is that the new generator can see (a) the current diff, (b) what reviewers have already said and how prior rounds disposed of it, (c) what the new findings are.
- Only spawn a new `generator` for non-recovery reasons when advancing to a new step's Phase 5, or when the user explicitly asks for a fresh implementer perspective.

**Loop:**
1. **Send the fix-cycle invocation to the generator:**

   - **SendMessage path (preferred):** SendMessage the existing `generator` agent with the same `Plan: …` / `Step <N>: <title>` header and the integrated findings as the `Review feedback:` section.
   - **Fresh-spawn fallback (when SendMessage fails):** spawn a new `generator` via the Agent tool with this invocation prompt shape:
     ```
     Plan: docs/plans/<slug>.md
     Step <N>: <title>
     Fix-cycle context (the prior generator session is no longer addressable):
       - Current diff: see `git diff HEAD`
       - Prior findings and dispositions:
         <list with 対応 / 延期 / 却下 / Disputed, location, gist>
       - Prior fix-cycle attempts:
         <one-liner per round: round, what was changed, what remained>
     Review feedback:
       <integrated findings from this round of Phase 6>
     ```
     Record the new agent's id/name and use it as the SendMessage target for the **next** fix-cycle round; if that also fails, fall back again. Falling back is a recovery path, not a routine — do not pre-emptively respawn.
2. Generator classifies each finding as **対応 / 延期 / 却下 / Disputed (High-Critical)** (per its agent definition; High/Critical rejections cannot be finalized inside the generator), applies 対応 items, runs its self-simplify pass on the new diff, re-runs tests, and reports all four buckets.
3. **Arbiter step (orchestrator) — only when `Disputed Findings` is non-empty.** For each disputed High/Critical finding:
   - Read the relevant file/lines yourself (Read tool, narrow range).
   - Compare the reviewer's claim against the generator's rationale.
   - Decide one of three outcomes:
     - **Reviewer is clearly right** → next loop iteration, force the item back to the generator as `対応 (orchestrator-confirmed)`; it must be addressed.
     - **Generator is clearly right** → close the item with a short orchestrator note recording the decision; inform the reviewer side by carrying a `Resolved by orchestrator` line into the next Phase 6 invocation's `Prior unresolved findings` so the reviewer does not regenerate it.
     - **Genuine judgment call** (both rationales plausible, tradeoff depends on user intent) → pause the loop, surface to the user with reviewer's claim, generator's rationale, the code excerpt, and your own brief assessment; ask for a directive. Resume after the user decides.
   - Keep your arbiter reads narrow (specific line range, not whole-file). The goal is to filter (a) clearly-right and (b) clearly-wrong rejections out without bloating your context.
4. Re-run **Phase 6** on the updated implementation.
5. Stop when **Critical = 0 AND High = 0** in Phase 6 output AND no Disputed Findings remain unarbitrated.

**Stuck detection (safety net):**
- A finding is "the same" if its category, location (file/symbol), and gist match a finding from the immediately prior round.
- If the same finding survives **two consecutive review rounds** (despite the arbiter step), pause the loop and surface to the user. The arbiter step should make this rare; if it triggers, it likely indicates a deeper plan-level disagreement that needs user input.

**Surfacing low-severity items:**
- When the loop exits, report **all** Medium and Low findings to the user along with the generator's 対応 / 延期 / 却下 dispositions and rationale (Medium/Low rejections are generator-final by design). The user may direct further action; you do not silently drop them.

---

## Phase 8 — Commit and advance

**Participants:** orchestrator + `generator` subagent.

**Do:**
- Instruct `generator` to commit the step:
  - First convergence for this step → new commit (subject in imperative mood, body conveys why/behavior/design intent, per project `CLAUDE.md`).
  - If fix-cycles ran on top of an already-existing commit for this step → `git commit --amend` or `git rebase -i ... fixup` to fold the fix in. Do not add a new standalone commit per round.
- Verify the commit was created (read `git log -1` summary in the generator's report).
- Advance to the next step → Phase 4 (per-step detailed design for that next step). If this was the **last** step, advance to **Phase 9** instead.

---

## Phase 9 — Knowledge migration + cleanup

**Participants:** orchestrator + `planner` subagent (triage) + `generator` subagent (migration + delete) + user.

**Trigger:** Phase 8 has committed the **last** step of the plan. All steps converged.

**Purpose:** preserve enduring knowledge from `docs/plans/<slug>.md` in its proper long-term home (godoc, inline comments, `docs/architecture/`), then remove the plan scaffolding from the branch tip. The plan document is a process artifact, not a deliverable; what survives is the subset of its content that cannot be recovered from the implementation alone.

**Do not run Phase 9** when the skill terminates mid-flow (user-requested stop, unresolved blocker, or any path that did not advance every step through Phase 8). The plan file stays in place under those conditions.

### Phase 9a — Knowledge triage (planner)

Invoke `planner` with this invocation header:
```
Plan: docs/plans/<slug>.md
Mode: knowledge-migration
Steps shipped: <list of step titles or ordinals>
Commit range: <first-step-commit>..HEAD
```

The planner reads the plan and compares it against the implementation (`git log` / `git diff` in the commit range). It returns a **Migration brief** with four buckets; each item carries a one-line rationale:

- **Already in code (discard):** items the implementation captures via type names, function names, module structure. No migration needed.
- **→ godoc / inline comment:** API contracts, error semantics, invariants, non-obvious workarounds. Each item names the target `<file>:<symbol>` and a 1–3 line content sketch following the project Go style (concise, contract-focused — see `CLAUDE.md`).
- **→ `docs/architecture/<topic>.md`:** architecture-wide judgments that span multiple packages or are not naturally attached to any one symbol (e.g. cross-cutting design rationale, rejected architectural alternatives whose reasoning informs future work). Each item names the target file (existing or new) and a content sketch.
- **Discard (ephemeral):** process artifacts whose conclusion is fully reflected in the current code (e.g. rejected alternatives that ended in a clear winner now implemented). No migration needed.

### Phase 9b — User review

Surface the Migration brief to the user with `planner proposes…` attribution. Heuristic for how to surface (mirrors Phase 4):

- **Material categorization tradeoff** (item plausibly belongs in `docs/architecture/` vs. just a godoc; item plausibly belongs in the Discard bucket but the user might want it preserved) → present the alternatives and ask the user.
- **Categorization clearly sound** → summarize the brief and proceed.
- **Always** display the full **Discard** list verbatim — the user is the final authority on what counts as scaffolding.

### Phase 9c — Migrate + delete (generator)

Invoke `generator` via SendMessage to the most recent generator session when reachable; fall back to a fresh spawn (Phase 7's continuity rules apply). Invocation:
```
Plan: docs/plans/<slug>.md
Mode: knowledge-migration
Migration brief: <agreed brief from 9b, verbatim>
```

Generator:
1. For each `→ godoc / inline` item: verify whether the target file already carries equivalent documentation; add it if missing, conforming to the project Go style (brevity, contract focus, no narration of implementation).
2. For each `→ docs/architecture/` item: create `docs/architecture/` if it does not exist, then create or append to `docs/architecture/<topic>.md`.
3. `git rm docs/plans/<slug>.md`.
4. Run `bazel build //... && bazel test //...` as a safety net (added godoc rarely breaks builds, but verify).
5. Create a **single dedicated commit** (do not amend or fixup into an earlier step's commit — keep cleanup independently revertable):
   - Subject (imperative): convey purpose, e.g. "Preserve <slug> design notes and remove plan scaffolding".
   - Body: enumerate which files received doc additions, which architecture docs were created/updated, and confirm the plan file was removed.
6. Report: files touched, architecture docs created/updated, plan file removal confirmed, build/test status.

**Do not:**
- Migrate the plan document verbatim. The point is to preserve only what the implementation alone cannot convey.
- Skip Phase 9a triage. Without it, generator either copies everything (over-migration) or drops decisions worth preserving (under-migration).
- Amend or fixup the cleanup into an earlier step's commit. Keep it independent so it can be reverted without unwinding implementation work.

**Skill complete** when Phase 9c's commit lands and the generator's report confirms `docs/plans/<slug>.md` no longer exists. Optionally summarize for the user: steps shipped, deferred items, follow-up suggestions, where preserved knowledge now lives.

---

## Quick reference — phase-to-output map

| Phase | Output |
|---|---|
| 1 | Goal/scope summary confirmed by user (in plan-mode designated file under plan-mode entry; otherwise in working context) |
| 2 | Approved high-level plan (in plan-mode designated file under plan-mode entry; otherwise in working context) |
| 3 | `docs/plans/<slug>.md` written (and plan mode exited via `ExitPlanMode` if it was active) |
| 4 | `### Detailed Design` (Contract + Suggested Internals) appended to step; user informed (or asked); or skipped with note if skip condition met |
| 5 | Working implementation (generator agent id recorded for SendMessage-first fix-cycles), all tests green, self-simplify pass done |
| 6 | Integrated findings list with tally |
| 7 | Convergence (Critical/High = 0); High/Critical disputes arbitrated by orchestrator or escalated; all dispositions surfaced |
| 8 | Step commit landed; advance to next step's Phase 4, or Phase 9 if this was the last step |
| 9 | Plan knowledge migrated to godoc / inline comments / `docs/architecture/`; `docs/plans/<slug>.md` removed; final dedicated commit landed |
