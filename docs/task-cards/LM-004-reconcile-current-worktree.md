# LM-004 — Reconcile current worktree and establish multi-agent baseline

- Status: READY
- Suggested owner: coordinator / maintainer
- Depends on: none
- Architecture: implementation sequencing (§20), safe incremental alignment (§4), and execution-backlog rules

## Goal

Create a durable, reviewed disposition for every existing modified, staged, and untracked file before agents create worktrees or characterize the prototype.

## Requirements

- Record current branch, commits ahead/behind, `git status --short --branch`, staged diff, unstaged diff, and untracked files.
- Classify each change as: maintainer-approved baseline, task-owned pending work, discard/revert candidate, or ambiguous requiring maintainer decision.
- Specifically identify current README/architecture/task artifacts, migration-create changes, and any config-loader/dependency work; do not assume ownership from chat history.
- Never reset, clean, stage, commit, or modify an ambiguous change without maintainer approval.
- Establish the exact worker baseline: either a maintainer-approved local commit/branch, or a content-addressed reviewed patch applied verbatim to an individually owned clean branch. A copied dirty worktree is never a worker baseline.
- Add/run the board-consistency check described in `docs/TASKS.md` before issuing worker assignments.

## Acceptance criteria

- Every current changed/untracked file has a recorded disposition and owner.
- The baseline for LM-001/LM-002 workers is immutable, unambiguous, reproducible, and independently hash-verifiable.
- No existing work is lost or overwritten.
- Queue/card/graph dependency data pass the consistency check.

## Verification

```bash
git status --short --branch
git diff --cached --stat
git diff --stat
git ls-files --others --exclude-standard
```

Attach the review/disposition record to the coordinator system. Run the board-consistency check and preserve its output.

## Do not

- Do not implement runtime features.
- Do not turn a dirty working tree into an implicit shared baseline for concurrent workers.
