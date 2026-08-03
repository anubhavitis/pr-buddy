# PR Review System — Small-Step MVP Plan

## Objective

Prove that a personal, AI-assisted PR review workflow reduces reviewer time without reducing review quality. Build only the minimum workflow needed for a real pilot.

## Working assumptions

- Reviews start manually in the MVP.
- PR code is treated as untrusted, including internal PRs.
- AI findings remain private until the reviewer deliberately writes a GitHub comment.
- Reading order is a guided tour, not a reordered VS Code diff.
- Claude Code remains the first model/runtime. Local-model routing is deferred.
- No VS Code extension is built during the pilot.
- No secrets or dependency directories are copied into review worktrees.
- No package installation or repository scripts run automatically.
- The reviewer writes all PR comments in the GitHub web UI and does not use the
  VS Code GitHub Pull Requests extension. Corrected on 2026-08-03; the original
  plan assumed inline commenting in the editor. See Phase 3.

If any assumption is wrong, resolve it before implementation because it changes the safety model or scope.

## MVP scope

### Included

- Create and refresh one isolated worktree per PR.
- Read the PR's actual base and head revisions from GitHub.
- Run the existing review skill without write or execution access.
- Save a versioned review artifact and session metadata.
- Reuse a valid cached review.
- Open the worktree and review artifact in VS Code.
- Resume the same Claude session for follow-up questions.
- Record enough usage data to judge the pilot.

### Deferred

- Problems-panel squiggles.
- Automatic GitHub comments.
- Permanent suppressions.
- Delta re-review.
- Automatic pre-review triggers.
- True diff reordering.
- Dependency setup and test execution.
- Local models and routing proxies.
- Team rollout and GitHub Actions.
- Custom VS Code extension.

## Phase 1 — Establish the baseline

Target: 60–90 minutes.

1. Select ten recent PRs representative of normal work.
2. Include both Go and TypeScript repositories.
3. Include at least two small, two medium, one large, and one cross-cutting PR.
4. Record approximate human review time for each PR.
5. Record the number of substantive comments and defects found.
6. Note common friction: checkout, navigation, context gathering, repeated review, or commenting.
7. Calculate the current median review time.
8. Record how many PRs required another review after an author update.

Exit criteria:

- A credible baseline exists.
- The largest source of review time is identified.
- Review work regularly costs enough time to justify tooling.

Stop condition:

- If median review time is already low and context gathering is not a major cost, do not build the system.

## Phase 2 — Validate review quality

Target: 2–3 hours across three historical PRs.

1. Choose one small, one cross-cutting, and one large closed PR.
2. Run the existing review skill manually on each PR.
3. Do not change the skill during the first pass.
4. Classify every finding as useful, weak, false, or unverifiable.
5. Compare findings with actual review comments and later fixes.
6. Record findings the AI missed that a human found.
7. Record findings the AI found that the original review missed.
8. Measure review runtime and manual ceremony.
9. Identify whether large PRs fail because of context limits or poor prioritization.
10. Revise the rubric only after all three results are classified.
11. Repeat the weakest case once using the revised rubric.

Exit criteria:

- At least 70% of high-severity findings are useful.
- The review adds information beyond a basic diff summary.
- The review completes reliably on normal PRs.
- Large-PR failure behavior is understood.

Stop condition:

- If findings remain mostly generic or unverifiable after one rubric revision, stop building workflow automation and improve the review method first.

## Phase 3 — Validate the VS Code and GitHub seam

Target: 30–60 minutes. **Mostly resolved on 2026-08-03; see below.**

Revised assumption: the reviewer does not use the VS Code GitHub Pull Requests
extension and writes all comments in the GitHub web UI. The original assumption
that inline commenting would happen in the editor was wrong.

Consequences:

- The checkout decision below is settled: the wrapper owns checkout, because no
  extension is competing for it. This was the expensive open question.
- Steps covering extension recognition, inline comment drafting, and approval
  and merge controls do not apply and are struck.
- Leaving the editor to comment is now a known, accepted cost rather than an
  unknown. Phase 7's ten-second criterion was written assuming inline
  commenting and is optimistic; keep the criterion and measure the real number
  during the pilot rather than redefining it now.
- Phase 8 already instruments this through its manual-ceremony stop condition.
  If browser switching turns out to dominate review time, that is Phase 9
  evidence for revisiting editor integration, not a reason to build it early.

Remaining checks:

1. ~~Create a disposable worktree for a harmless real PR.~~ Done: `siphl#4`.
2. Open that worktree in VS Code and confirm changed-file navigation works.
3. Verify that VS Code does not automatically execute branch-controlled tasks
   on open.
4. Confirm behavior for a PR from a fork if fork-based PRs are relevant.

Exit criteria:

- ~~One supported worktree/checkout approach is proven.~~ Proven: detached
  external worktree, head verified against GitHub.
- Opening the worktree does not automatically execute PR code.

Decision:

- Resolved: checkout stays in the wrapper.

## Phase 4 — Define the review artifact

Target: 45–60 minutes.

1. Define one machine-readable source artifact.
2. Include findings, severity, stable finding identity, file location, evidence, confidence, and reading group.
3. Store session identity separately from human-readable review content.
4. Record PR number, repository, base revision, head revision, rubric version, model, and timestamps.
5. Define explicit states: pending, running, complete, failed, and stale.
6. Define when a result becomes stale.
7. Include base movement, head movement, rubric changes, and model changes in invalidation rules.
8. Define a human-readable rendering generated from the source artifact.
9. Keep editor-specific output as a derived representation, not the source of truth.
10. Version the artifact format from the first iteration.

Exit criteria:

- Cache validity can be decided deterministically.
- A failed or interrupted run cannot look complete.
- Future squiggles and suppressions can be added without changing the review engine.

## Phase 5 — Implement safe worktree lifecycle using TDD

Target: half a day.

1. Write lifecycle tests before implementation.
2. Test repository identification.
3. Test retrieval of the PR's actual base and head revisions.
4. Test initial worktree creation.
5. Test repeated invocation for the same unchanged PR.
6. Test refresh after the PR head changes.
7. Test base-branch movement.
8. Test fork-based PR metadata.
9. Test closed and merged PR handling.
10. Test name collisions between repositories and PR numbers.
11. Test interruption during creation or refresh.
12. Implement the smallest lifecycle behavior that passes the tests.
13. Refuse destructive cleanup when the worktree contains unexpected user changes.
14. Avoid secret copying, dependency copying, installs, generators, hooks, and repository scripts.

Exit criteria:

- Invocation is idempotent.
- The checked-out revision matches GitHub's current PR head.
- Existing user work is never silently overwritten.
- No untrusted code executes.

## Phase 6 — Implement the review runner using TDD

Target: half a day.

1. Write runner tests before implementation.
2. Test cache hits and each cache-invalidating change.
3. Test successful review completion.
4. Test Claude failure, malformed output, interruption, and timeout.
5. Test capture of session metadata.
6. Test conversion from source artifact to readable review.
7. Implement a read-only Claude invocation.
8. Exclude write, shell, build, test, and package-manager capabilities.
9. Write status changes atomically so partial output is never treated as complete.
10. Preserve failure details for diagnosis.
11. Display concise progress without blocking access to the worktree.

Exit criteria:

- A successful run produces a valid artifact and resumable session.
- A repeated unchanged run uses the cache.
- A changed PR produces a new review.
- Failures are visible and safely retryable.

## Phase 7 — Connect the minimal reviewer experience

Target: 1–2 hours.

1. Open the correct worktree in VS Code.
2. Open the readable review beside the changed files.
3. Present the reading groups as clickable file locations.
4. Provide one obvious way to resume the review session.
5. Confirm follow-up questions use the current PR revision.
6. Keep GitHub comments manual and reviewer-authored.
7. Avoid repository-committed automatic tasks during the pilot.
8. Measure the manual steps from invocation to useful review state.

Exit criteria:

- The reviewer reaches a useful review state with one command and no more than ten seconds of manual interaction.
- Chat resumes the correct session.
- Commenting remains deliberate and attributable to the reviewer.

## Phase 8 — Pilot on 20–30 PRs

Target: four weeks of normal use.

1. Alternate between AI-first and human-first reviews where practical.
2. Record total human review time per PR.
3. Record AI runtime separately; do not count background waiting as human time.
4. Classify each warning/error finding as accepted, dismissed, or uncertain.
5. Record substantive human findings missed by the AI.
6. Record whether the reading guide was actually used.
7. Record whether follow-up chat was used.
8. Record every time the workflow was bypassed and why.
9. Record failures, stale reviews, and incorrect file locations.
10. Review the data after ten PRs without expanding scope.
11. Complete the full pilot unless a stop condition is reached.

Success criteria:

- Median human review time falls by at least 25%.
- At least 70% of warning/error findings are useful.
- At least 80% of eligible PRs use the workflow by the final pilot week.
- Human reviewers do not find evidence of reduced review care.
- No security boundary is bypassed for convenience.

Stop conditions:

- Useful finding rate remains below 50% after rubric refinement.
- The workflow adds more than 30 seconds of manual ceremony.
- Stale or mismatched reviews occur more than once.
- Usage falls below 60% because the output is noisy or slow.

## Phase 9 — Select exactly one next feature

Use pilot evidence, not preference:

- Add suppressions only if repeated false-positive categories are measurable.
- Add delta re-review only if author updates create material repeated work.
- Add asynchronous pre-review only if model waiting remains a major cost.
- Add Problems-panel integration only if reviewers repeatedly navigate from findings to files.
- Improve reading order only if the guide is used but its ordering is poor.
- Consider local models only if hosted-model cost or data policy becomes limiting.
- Consider an extension only if several remaining friction points require a persistent UI.

For the selected feature:

1. Define one measurable hypothesis.
2. Add tests before or alongside the change.
3. Pilot it for at least ten PRs.
4. Keep it only if the target metric improves.
5. Return to this decision point before selecting another feature.

## Testing & Verification

- Follow red-green-refactor for lifecycle, caching, parsing, and status transitions.
- Use temporary repositories and mocked command boundaries for automated tests.
- Cover default branches other than `main`, forks, force-pushes, base movement, interrupted runs, malformed model output, and closed PRs.
- Perform one manual end-to-end verification on a harmless real PR before the pilot.
- Verify the exact checked-out head revision against GitHub before every review.
- Verify that no package manager, hook, task, generator, or repository script executes automatically.
- Verify that no `.env`, `.npmrc`, credentials, or unrelated local files enter the worktree.
- Run lint only when committing, and scope fixes to touched files.
- If a pre-push hook fails because of unrelated ambient issues, ask before bypassing it.
- Do not commit or begin implementation until explicitly requested.

## Estimated effort

- Evidence and seam validation: 4–6 hours.
- Artifact design and MVP implementation: 1–2 focused days.
- Pilot: 20–30 normal PRs over approximately four weeks.
- Post-pilot work: one evidence-selected feature at a time.

The correct first commitment is not the full system. It is the safe worktree lifecycle plus one reliable, cached, resumable review artifact.

