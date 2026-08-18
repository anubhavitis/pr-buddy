# Architecture: current vs proposed

Both diagrams below are drawn from the code as it exists on `main`, not from
prose descriptions of it. Every defect marked in the "current" diagram was
confirmed at a specific line before being drawn.

---

## Current architecture

```mermaid
flowchart TB
    subgraph entry["Entry points — duplicated lifecycle"]
        HUMAN["`**main.go** run()
        bare: pr-buddy N`"]
        JSONC["`**json.go** cmdPrepare / cmdReviewJSON
        cmdDeps / cmdProgress / cmdChecks / cmdRemove`"]
    end

    EXT["`**VS Code extension** (TypeScript)
    extension.ts 760L · module-global state
    prBuddy.ts spawns the binary`"]

    EXT -->|"spawn + parse stdout JSON"| JSONC

    HUMAN -->|"srcRepoDir = cwd"| D1{{"`**D1** human path never
    calls sourceRepoDir —
    -repo outside cwd breaks`"}}
    JSONC -->|"sourceRepoDir(cwd, slug)"| SRC["bare mirror under .repos/"]

    D1 --> WT
    SRC --> WT

    subgraph ghlayer["internal/gh — read-only + 1 write"]
        GH["ViewPR · CurrentRepo · Orgs
        Repos · PRs · ChangedFiles"]
        GHW["RerunChecks
        (only permitted write)"]
    end

    HUMAN --> GH
    JSONC --> GH
    JSONC --> GHW

    GH -->|"base/head SHA from GitHub"| WT

    subgraph wtlayer["internal/worktree"]
        WT["`Ensure() — detached checkout
        fetch refs/pull/N/head
        idempotent · refuses dirty`"]
        D2{{"`**D2** no core.hooksPath=
        no smudge-filter disable
        (worktree.go:178-197)`"}}
        WT -.-> D2
    end

    WT --> DEPS["`internal/deps
    cp -c reviewer's own
    node_modules / dist`"]

    subgraph runlayer["internal/runner"]
        CACHE{"`cached.Usable(prov)?
        CacheKey comparison`"}
        PROMPT["`Prompt(provenance)
        text only`"]
        CLAUDE["`Claude.Review()
        Read/Grep/Glob only
        --setting-sources user
        --strict-mcp-config`"]
        PARSE["`ParseReview()
        derives FindingID
        validates severity + path≠''`"]
        D3{{"`**D3** prompt says
        'run git diff BASE...HEAD
        conceptually by reading files'
        — no delta supplied
        (prompt.go:17-19)`"}}
        D4{{"`**D4** path never checked
        against the snapshot —
        '../../etc/x' passes
        (runner.go:233-236)`"}}
        CACHE -->|miss| PROMPT --> CLAUDE --> PARSE
        PROMPT -.-> D3
        PARSE -.-> D4
    end

    WT --> CACHE
    DEPS -.->|"worktree ready"| CACHE

    subgraph store["internal/artifact — two separate writes"]
        WR["`WriteReview()
        validate → tmp → fsync → rename`"]
        WS["`WriteSession()
        second, independent write`"]
        D5{{"`**D5** crash between the two
        leaves a complete review with
        no session — cache hit that
        cannot resume
        (runner.go:132-149)`"}}
        WR --> D5 --> WS
    end

    PARSE --> WR
    CACHE -->|"hit"| RESULT
    WS --> RESULT["Result{Review, FromCache, StaleReason}"]

    RESULT --> RENDER["`internal/render
    Markdown() — derived, one-way`"]
    RESULT -->|"JSON on stdout"| EXT

    EXEC["`**internal/exec.Runner**
    single subprocess boundary
    no shell · Fake in every test`"]

    WT -.-> EXEC
    GH -.-> EXEC
    CLAUDE -.-> EXEC
    DEPS -.-> EXEC

    classDef defect fill:#3b1219,stroke:#e5484d,stroke-width:2px,color:#ffd7d9
    classDef good fill:#0f2e1a,stroke:#30a46c,stroke-width:2px,color:#c7f2d8
    class D1,D2,D3,D4,D5 defect
    class EXEC,WR good
```

### Confirmed defects

| # | Defect | Evidence | Severity |
|---|---|---|---|
| **D1** | Human and JSON paths resolve the repo differently | `main.go:107` passes `cwd` to `Ensure`; `json.go:157` passes `sourceRepoDir(...)`. `pr-buddy -repo other/x 5` from an unrelated directory fetches into the wrong repo. | High |
| **D2** | Checkout does not disable hooks or content filters | No `core.hooksPath=` / `filter.*` anywhere in `worktree.go`. `git worktree add` (`:184`) and `git checkout` (`:193`) run with ambient config. `CLAUDE.md` claims "no hooks" — the code does not enforce it. | High |
| **D3** | Model receives no delta | `prompt.go:17-19` asks the model to reconstruct `base...head` "conceptually by reading the changed files" from a head-only checkout. It cannot see what changed. | High — this is the product hypothesis |
| **D4** | Model paths unvalidated | `runner.go:233-236` checks only that path is non-empty. `store.go:48` repeats the same non-empty check. Nothing constrains a path to the reviewed tree. | High |
| **D5** | Review and session published separately | `runner.go:132` writes the review, `:141` writes the session. Each write is atomic; the **pair** is not. | Medium |

The `DirName` collision the doc lists is **already fixed** — `worktree.go:61-72` appends a
digest when the slug truncates. The doc's Phase 1 item there is stale.

---

## Proposed architecture

```mermaid
flowchart TB
    subgraph roots["Composition roots — no domain rules"]
        CLI["CLI"]
        VSC["VS Code extension"]
    end

    subgraph proto["Protocol — versioned, validated"]
        P["`process contract
        runtime validation
        structured errors
        one artifact-validity answer`"]
    end

    CLI --> EXEC_R
    VSC --> P --> EXEC_R

    EXEC_R["`**Review execution**
    the single workflow both
    entry points use`"]

    subgraph id["Review identity"]
        RI["`ReviewIdentity
        repo · PR · base · head
        rubric · model · schema
        canonical keys + equality`"]
    end

    subgraph lock["Per-identity ownership"]
        LK["`serialize same identity
        parallel across identities
        single-flight`"]
    end

    subgraph safe["Safe checkout"]
        SC["`exact head revision
        stable for review duration
        **hooks + filters cannot run**
        dirty never discarded
        scoped deletion`"]
    end

    subgraph input["Review input"]
        RIN["`provenance
        changed-file inventory
        **actual base→head delta**`"]
    end

    subgraph exec_m["Model adapter"]
        MOD["read-only invocation"]
    end

    subgraph valid["Output validation"]
        V["`schema + severity
        **path containment:
        every path inside
        the reviewed snapshot**`"]
    end

    subgraph pub["Transactional publication"]
        TX["`review + session + provenance
        = **one generation**
        published together or not at all`"]
    end

    EXEC_R --> RI
    RI --> LK
    LK --> SC
    SC --> RIN
    RIN --> MOD
    MOD --> V
    V --> TX

    TX --> SESS["`**Review session** (extension)
    state keyed by identity + generation
    async results must prove they
    belong to the active session`"]
    SESS --> VSC

    subgraph adapters["Adapters — only where variation is real"]
        A1["GitHub metadata"]
        A2["model runtime"]
        A3["git execution"]
        A4["artifact persistence"]
        A5["editor frontend"]
        A6["dependency copying"]
    end

    SC -.-> A3
    MOD -.-> A2
    TX -.-> A4
    RI -.-> A1
    SESS -.-> A5

    classDef fix fill:#0f2e1a,stroke:#30a46c,stroke-width:2px,color:#c7f2d8
    classDef spec fill:#2e2410,stroke:#f5a623,stroke-width:2px,color:#ffe4b5
    class SC,RIN,V,TX fix
    class LK,P spec
```

Green = fixes a confirmed defect. Amber = speculative, no evidence yet.

---

## The difference, condensed

| Concern | Current | Proposed |
|---|---|---|
| Orchestration | Two entry points, duplicated lifecycle, divergent repo resolution | One `Review execution` both roots call |
| Identity | `Provenance` struct + ad-hoc slug + separate `DirName` | One `ReviewIdentity` owning keys, paths, equality |
| What the model sees | Head checkout + prose asking it to imagine the diff | Head checkout **+ real base→head delta** |
| Untrusted output | Non-empty path check | Path containment against the snapshot |
| Checkout safety | Detached, no remote added; hooks/filters ambient | Hooks and filters provably cannot execute |
| Publication | Two atomic writes, non-atomic pair | One generation, all-or-nothing |
| Concurrency | None | Single-flight per identity, parallel across |
| Extension state | Module globals | Session keyed by identity + generation |
| Go↔TS contract | Duplicated shapes, unvalidated | Versioned, validated, structured errors |

---

## What the diagrams show that the prose does not

Three things become visible once it is drawn:

1. **The defects cluster in two places** — the entry-point fan-in (D1) and the
   runner's trust boundary (D3, D4). They are not spread across the codebase.
   The Go packages themselves are fine.

2. **Four of the five confirmed defects are fixed by localized changes**, not by
   the restructure. Path containment is a function in `runner`. The delta is an
   argument to `Prompt`. Hook disabling is `-c core.hooksPath=/dev/null` on two
   commands. Transactional publish is one directory rename.

3. **The genuinely architectural items are the two amber boxes** — per-identity
   locking and the versioned protocol — and neither addresses a defect that
   exists today. They address defects that would exist in a concurrent,
   multi-consumer world that this tool is not in.

The minimum diff that closes every confirmed defect:

```mermaid
flowchart LR
    subgraph now["Today"]
        N1["main.go: cwd"]
        N2["json.go: sourceRepoDir"]
    end
    subgraph min["Minimum viable fix"]
        M1["`**shared prepare()**
        both entry points`"]
        M2["`Prompt(prov, **delta**)`"]
        M3["`validatePaths(findings,
        worktreeDir)`"]
        M4["`git -c core.hooksPath=/dev/null
        -c filter... `"]
        M5["`publish(review, session)
        atomic dir swap`"]
    end
    N1 --> M1
    N2 --> M1
    classDef fix fill:#0f2e1a,stroke:#30a46c,stroke-width:2px,color:#c7f2d8
    class M1,M2,M3,M4,M5 fix
```

That is five changes against roughly 1,400 lines of non-test Go — not five
phases. Everything else in the plan should wait for the pilot to produce
evidence that it is needed.
