# pr-buddy Architecture Plan

## Objective

Make the review workflow safe, deterministic, testable, and scalable for multiple
repositories and concurrent operations without turning every module into a
replaceable abstraction.

The design should provide plug-and-play adapters only at external seams. Core
correctness rules remain concrete and centralized.

## Current assessment

The Go packages are generally cohesive and appropriately small. The main
architectural weaknesses are across the end-to-end seams:

- Repository directory names can collide after sanitization.
- Git checkout does not enforce the documented no-hooks/no-filters safety rule.
- The model receives the head checkout but not the pull-request delta.
- Review identity is inconsistent across the CLI, persisted artifacts, and editor.
- Concurrent operations can move a checkout or overwrite related state.
- A complete review and its resumable session are published separately.
- Model-produced paths are not constrained to the reviewed snapshot.
- Human and JSON command orchestration duplicate lifecycle decisions.
- The Go-to-TypeScript protocol is duplicated and not validated at runtime.
- The extension lifecycle has no automated test seam.

## Design principles

1. Use one full `ReviewIdentity`: repository, PR number, base SHA, head SHA,
   rubric, model, and schema.
2. A review observes one immutable delta and one stable checkout.
3. Complete means the review, session, and provenance are mutually consistent.
4. Validate untrusted model output before it becomes a complete artifact.
5. Serialize operations for the same review identity while allowing unrelated
   reviews to run concurrently.
6. Keep external variation behind real seams with concrete adapters.
7. Prefer deep modules with small interfaces over many pass-through wrappers.
8. Keep the CLI and editor as composition roots, not owners of domain rules.
9. Fail closed when safety, identity, or persistence cannot be proven.
10. Do not add deferred product features without pilot evidence.

## Target modules

### Review identity

Owns canonical repository identity, storage keys, provenance, and equality. Storage
paths use a readable prefix plus an unconditional digest so distinct repositories
cannot collide.

### Safe checkout

Owns fetching, materializing, refreshing, and removing review checkouts. Its
interface guarantees:

- the exact head revision is checked out;
- the checkout remains stable for the duration of a review;
- hooks and content filters cannot execute implicitly;
- dirty reviewer changes are never discarded;
- deletion targets only the matching repository and review identity.

### Review input

Builds the immutable input supplied to the model: provenance, changed-file
inventory, and the actual base-to-head delta. The model must not reconstruct the
delta from a head-only checkout.

### Review execution

Coordinates cache lookup, checkout ownership, review input, model invocation,
output validation, and publication. This becomes the single workflow used by both
human-facing and JSON commands.

### Artifact store

Owns review lifecycle transitions and persistent state. A complete generation is
published only when review content and resumable session metadata are both valid
and share the same identity. Running, failed, stale, and complete states remain
observable and fail closed.

### Review session

Owns extension state for one immutable review identity and operation generation.
Every asynchronous result must prove it belongs to the active session before it can
change the tree, diagnostics, progress, dependency state, or terminal state.

### Protocol

Defines the versioned Go-to-TypeScript process contract, runtime validation, and
structured errors. The extension consumes one authoritative artifact-validity
result instead of reimplementing cache rules.

### External adapters

Keep replaceability at these seams when genuine variation exists:

- GitHub metadata provider;
- model runtime;
- Git command execution;
- artifact persistence;
- editor frontend;
- platform-specific dependency copying.

Provenance, path containment, repository identity, lifecycle transitions, and cache
validity are correctness rules, not plug-and-play policies.

## Target flow

```text
CLI / VS Code
    -> Review execution
        -> Review identity
        -> Safe checkout + per-review ownership
        -> Review input (trusted delta)
        -> Model adapter
        -> Artifact validation
        -> Atomic generation publication
    -> Versioned protocol
    -> Review session / presentation
```

## Delivery sequence

### Phase 1: Remove immediate correctness and safety hazards

- Make repository storage identity collision-resistant.
- Enforce safe checkout without implicit hooks or content filters.
- Validate all finding and reading-guide paths against the reviewed snapshot.
- Correct the divergent human `-repo` workflow through shared orchestration.

Exit criteria:

- Distinct valid repository slugs cannot share storage.
- Checkout safety is proven with real Git integration tests.
- No artifact can reference a path outside its reviewed snapshot.
- Human and JSON entry points resolve the same requested repository.

### Phase 2: Deepen review execution

- Introduce the canonical full review identity.
- Supply an immutable base-to-head delta to every review.
- Hold exclusive ownership of the checkout for the review duration.
- Serialize same-review operations while preserving cross-review parallelism.

Exit criteria:

- A review cannot observe two heads.
- Duplicate same-identity requests invoke the model at most once.
- Prepare, review, dependency setup, and removal cannot corrupt each other.

### Phase 3: Make publication transactional

- Treat review content and resumable session metadata as one generation.
- Validate schema, identity, and cache key on every read.
- Make running and failed status persistence fail closed.
- Prevent stale sessions from being resumed against a newer head.

Exit criteria:

- Every cache hit is complete, current, and resumable.
- Interrupted publication never exposes a partial generation as complete.
- Forced review cannot silently return an undeleted cache entry.

### Phase 4: Deepen the extension session

- Move lifecycle state out of module globals into one review-session module.
- Key asynchronous operations and actionable tree nodes by full identity and
  generation.
- Use one validated artifact adoption path for initial load, completion, watcher
  updates, diagnostics, and reading order.
- Ensure cancellation terminates the owned process tree.

Exit criteria:

- Switching repositories, PRs, or heads cannot apply obsolete results.
- Extension state transitions are deterministic under concurrent completion.
- Tree content and diagnostics always derive from the same validated artifact.

### Phase 5: Stabilize the protocol and project governance

- Version and validate the Go-to-TypeScript protocol.
- Add structured error responses.
- Reconcile `plan.md`, `README.md`, and `CLAUDE.md` with implemented scope.
- Decide explicitly whether the extension belongs in the pilot or remains deferred.

Exit criteria:

- Binary/extension version mismatch fails early with a clear error.
- Architecture and safety documentation describe the executable system.
- Pilot behavior matches the governing experiment plan.

## Scalability expectations

After these phases, the architecture should support:

- multiple repositories without identity collisions;
- parallel reviews for unrelated identities;
- single-flight work for the same identity;
- safe recovery after interruption;
- additional model or editor adapters without changing core rules;
- team maintenance without duplicating lifecycle or protocol knowledge.

Remote workers, shared artifact storage, CI execution, automatic GitHub actions,
and team-wide orchestration remain out of scope until pilot evidence requires them.

## Success metrics

- Zero mismatched or mixed-head reviews.
- Zero implicit hook, filter, package-manager, or repository-script execution.
- Zero cross-repository storage collisions.
- Every successful review is resumable.
- Same-identity concurrent requests produce one model invocation.
- All editor state is rejected when its identity or generation is obsolete.
- Go and extension lifecycle paths have automated integration coverage.
- The pilot retains its existing review-quality and human-time targets.

## Testing & Verification

- Follow red-green-refactor for identity, lifecycle, caching, parsing, concurrency,
  and status transitions.
- Add real Git integration tests for hooks, smudge filters, colliding repository
  names, head movement, dirty worktrees, and removal.
- Add orchestration tests for human and JSON commands, including `-repo` outside the
  current checkout.
- Add concurrency tests for overlapping prepare, review, dependency setup,
  progress updates, cancellation, and removal.
- Add artifact tests for failed session writes, stale cache keys, interrupted
  publication, unsafe paths, and schema mismatch.
- Add extension tests for same-number PRs across repositories, rapid selection
  changes, watcher adoption, stale tree nodes, and process cancellation.
- Add a version-skew contract test between the Go binary and TypeScript adapter.
- Perform one manual end-to-end verification on a harmless real PR before the pilot.
- Do not use `lint --write` on unrelated files; scope lint fixes to touched files.
- Run lint only when committing.
- If a pre-push hook fails on unrelated ambient issues, ask before using
  `--no-verify`.

## Non-goals

- Abstracting every module behind an interface.
- Adding adapters without a real second implementation or test need.
- Introducing distributed infrastructure for a personal local workflow.
- Expanding deferred product scope before the evidence gates are complete.
