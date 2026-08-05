# Selective Rollback + Refresh — by-name, by-batch, and refresh

> **Status:** Draft plan (reviewed — REVISE fixed)  
> **Repo:** /Users/rajifafif/www/lamigrate  
> **Created:** 2026-08-04  
> **Updated:** 2026-08-05 (post-review: fixed 3 BLOCKERs, 2 MAJORs, 4 MINORs)

---

## Problem

Two gaps in the current CLI:

1. **Selective rollback.** `lamigrate down` only targets the latest batch. There
   is no way to roll back a single named migration or a specific batch number
   without manually re-applying everything else.
2. **Clean restart.** During development iteration, the common pattern is
   "rollback a range, then re-run it" (Laravel's `migrate:refresh`). There is
   no way to do this — you must run `down` then `up` manually, risking a
   mismatch between what was rolled back and what is re-applied.

## Goals

### Selective rollback (Feature A)

1. `down <migration_name>` — rollback the named migration **plus all migrations
   applied after it** (in the latest batch), in reverse chronological order.
2. `down --batch N` — rollback **all migrations in batch N**. If batch N is the
   latest, identical to bare `down`. If newer batches exist, error with clear
   guidance.

### Refresh (Feature B)

3. `refresh` — rollback **all** migrations, then re-apply **all**. Equivalent to
   Laravel's `migrate:refresh`.
4. `refresh --step N` — rollback last N migrations, then re-apply those same N.
   Useful for iterating on a recently-broken migration. The step limit counts
   applied migrations globally across all batches (not per-batch). If N exceeds
   the total number of applied migrations, clamp to all.
5. `refresh <migration_name>` — rollback **all** migrations, then re-apply only
   **up to and including** the named migration. Equivalent to Laravel's
   `migrate:refresh --class=Name`.

## Non-goals (v1)

- Out-of-batch rollback (rolling back batch 3 when batch 5 exists). This is
  inherently dangerous because newer migrations may depend on older ones, and
  the rollback SQL would likely fail on FK/column constraints. Rejected for v1.
- Cross-batch selective rollback (`down --name X` when X is not in the latest
  batch). Error + guidance instead.
- Dependency-graph-aware rollback (future: `architecture.md §5` dependency
  declarations).
- `refresh --batch N` — refresh is about development iteration, not batch
  targeting. Use `down --batch N` + manual `up` for that case.

---

## CLI Design

### Feature A: Selective rollback

#### By-name

```
lamigrate down <migration_name>
```

- `<migration_name>` is the canonical name (e.g. `20260730094235_create_users`).
- **Scope:** only the latest batch (same scope as current `down`).
- Selects the named migration **and all migrations applied after it** in that
  batch (i.e., newer migrations first, down to the named one).
- If the name is not found in the latest batch, error:

```
error: migration '20260730094235_create_users' is in batch 3, not the latest batch (5).
To roll back a non-latest batch, use: reset --from-batch 3  (not yet implemented)
Or: manually roll back newer batches first.
```

#### By-batch

```
lamigrate down --batch N
```

- N is a positive integer (batch number).
- **Scope:** if N == latestBatch, identical to bare `down` (rollback entire batch).
- If N < latestBatch, error:

```
error: batch 3 is not the latest batch (5).
Newer batches must be rolled back first. Use:
  lamigrate down --batch 5    (rollback batch 5)
  lamigrate down              (rollback latest batch)
```

#### Disambiguation

The current `down` accepts `--step N` (or positional N) for step count. The new
flags `--batch N` and positional `<name>` must not conflict:

| Input | Interpretation |
|-------|---------------|
| `down` | Rollback all of latest batch (existing) |
| `down --step 3` | Rollback 3 from latest batch (existing) |
| `down --batch 3` | Rollback batch 3 (new — must be latest) |
| `down 20260730094235_create_users` | Rollback named migration + newer ones in latest batch (new) |
| `down --step 3 20260730094235_create_users` | Error — --step and name are mutually exclusive |
| `down --batch 3 --step 5` | Error — --batch and --step are mutually exclusive |

Numeric positional arg still means step count (backward compat). Non-numeric
positional arg means migration name (new).

---

### Feature B: Refresh

#### Bare refresh

```
lamigrate refresh
```

- Rolls back ALL applied migrations (reverse order).
- Re-apply ALL pending migrations (forward order).
- Equivalent to: `lamigrate down --all` then `lamigrate up`.

#### By-step

```
lamigrate refresh --step N
```

- Rolls back the last N applied migrations (globally across all batches, in
  reverse chronological order), then re-applies them.
- The step limit counts applied migrations across ALL batches, not per-batch.
  If N exceeds the total number of applied migrations, clamp to all and do a
  full rollback + re-apply.
- Use case: "I broke migration #5, let me fix it and re-run the last 3."

#### By-name

```
lamigrate refresh <migration_name>
```

- Rolls back ALL applied migrations (reverse order, all batches).
- Re-apply only **up to and including** `<migration_name>` (forward order).
- Use case: "I want the schema state as it was when this migration was the
  latest applied — clean restart up to this point."
- The named migration must exist and be in the pending set after rollback.

#### Disambiguation

| Input | Interpretation |
|-------|---------------|
| `refresh` | Rollback all + re-apply all |
| `refresh --step 3` | Rollback last 3 + re-apply last 3 |
| `refresh 20260730094235_create_users` | Rollback all + re-apply up to name (new) |
| `refresh --step 3 20260730094235_create_users` | Error — --step and name are mutually exclusive |
| `refresh --batch N` | Not supported — error |

#### Execution protocol

`refresh` is a **single atomic operation** under one advisory lock:

```
1. Acquire advisory lock (same as any migration command)
2. Build the rollback plan (same logic as down with DownTarget.All)
3. Execute the rollback plan (§11.3 protocol per migration)
4. Build the forward plan (up plan based on refresh target)
5. Execute the forward plan (§11.2 protocol per migration)
6. Release lock
```

This is NOT a compose of `down` + `up` as separate commands. Both phases run
within the same lock session to prevent interference from concurrent processes.

**Failure semantics:**
- Rollback phase fails → stop, report error. State is partially rolled back
  but consistent (each completed down migration is individually valid).
- Forward phase fails → state is "partially refreshed". Recoverable by
  running `lamigrate up` to finish the pending migrations.

**Pretend mode** (`--pretend refresh`): show both plans (down + up) without
executing either. Output format:

```
Refresh plan:

Rollback (5 migrations):
  3. 20260801120000_add_payment_index
  2. 20260731150000_create_payments_table
  1. 20260730094235_create_users
  0. 000027_add_legacy_index        (baseline, skipped)

Re-apply (3 migrations):
  1. 20260730094235_create_users
  2. 20260731150000_create_payments_table
  3. 20260801120000_add_payment_index
```

**Confirmation:** `refresh` is destructive (it temporarily removes schema).
Requires `-y`/`--yes` or interactive confirmation, same as `reset`.

---

## Public API Changes

### New types

```go
// DownTarget controls what "down" selects.
type DownTarget struct {
    // Exactly one of Name, Batch, or Limit must be set (mutually exclusive).
    Name  string     // rollback to-and-including this migration in latest batch
    Batch int        // rollback all migrations in this batch (must == latestBatch)
    Limit StepLimit  // existing: step count or all (legacy path)
}

// New constructor functions:
func DownToName(name string) (DownTarget, error)
func DownToBatch(batch int) (DownTarget, error)
func DownAll() DownTarget
func DownSteps(n int) (DownTarget, error)

// RefreshTarget controls what "refresh" does.
type RefreshTarget struct {
    // Exactly one of Name or Limit must be set (mutually exclusive).
    Name  string     // rollback all, re-apply up to name
    Limit StepLimit  // rollback N + re-apply N, or rollback all + re-apply all
}

// New constructor functions:
func RefreshAll() RefreshTarget
func RefreshSteps(n int) (RefreshTarget, error)
func RefreshToName(name string) (RefreshTarget, error)
```

### RefreshResult (extends Result)

```go
// RefreshResult describes the outcome of a refresh operation.
type RefreshResult struct {
    Rollback Result  // result of the rollback phase
    Apply    Result  // result of the forward phase
}
```

### RefreshPlanView (for --pretend)

```go
// RefreshPlanView is a read-only preview of a refresh operation.
type RefreshPlanView struct {
    Command   string   // "refresh"
    Directory string
    TableName string
    Rollback  []string // migrations to roll back (reverse chronological)
    Apply     []string // migrations to re-apply (forward chronological)
    DryRun    bool
}
```

### Changed signatures

```go
// BEFORE:
func (m *Migrator) Down(ctx context.Context, limit StepLimit) (Result, error)
func (m *Migrator) PreviewDown(ctx context.Context, limit StepLimit) (PlanView, error)

// AFTER:
func (m *Migrator) Down(ctx context.Context, target DownTarget) (Result, error)
func (m *Migrator) PreviewDown(ctx context.Context, target DownTarget) (PlanView, error)

// NEW:
func (m *Migrator) Refresh(ctx context.Context, target RefreshTarget) (RefreshResult, error)
func (m *Migrator) PreviewRefresh(ctx context.Context, target RefreshTarget) (RefreshPlanView, error)
```

**Migration note:** this is a breaking change to the public API. Documented as
breaking under the existing `docs/known-limitations.md §5.8` (experimental
API stability — no backward compatibility guarantee until v1.0).

### Internal planner changes

`buildDownPlan` gains a `DownTarget` parameter (replaces `StepLimit`):

```go
func (m *Migrator) buildDownPlan(
    ctx context.Context,
    conn *sql.Conn,
    caps *SessionCapabilities,
    target DownTarget,
) (*MigrationPlan, error)
```

Inside `buildDownPlan`, after selecting all candidates from `latestBatch`:

1. **Target.Limit (legacy path):** unchanged — apply `applyDownLimit`.
2. **Target.Name:** find the named migration in candidates. Candidates are in
   **reverse execution order** (newest first). Slice up to and including the
   named migration: `candidates[0..found]`. This selects the named migration
   and everything newer (applied after it). If not found in latest batch,
   return `ErrMigrationNotFoundInLatestBatch` with the migration's actual batch.
3. **Target.Batch:** read `latestBatch`. If `target.Batch != latestBatch`,
   return a wrapped `ErrBatchNotLatest` error:
   `fmt.Errorf("batch %d is not the latest batch (%d): ...", target.Batch, latestBatch)`
   Otherwise proceed as if `Limit = All`.

`buildRefreshPlan` is new — it composes a down plan + an up plan:

```go
func (m *Migrator) buildRefreshPlan(
    ctx context.Context,
    conn *sql.Conn,
    caps *SessionCapabilities,
    target RefreshTarget,
) (*RefreshPlan, error)
```

Where:

```go
type RefreshPlan struct {
    downPlan  *MigrationPlan
    upPlan    *MigrationPlan
    command   string
}
```

Logic:
- **Target.Limit:** build a down plan with same limit, then build an up plan
  for the same set of migrations (re-apply after rollback).
- **Target.Name:** build a down plan with `Limit = All()` (rollback everything),
  then build an up plan up to the named migration (`buildUpPlan` with a
  name-based cutoff after selecting pending migrations).

---

## Files to Change

### New files

| File | Purpose |
|------|---------|
| `down_target.go` | `DownTarget` type, constructors, validation |
| `down_target_test.go` | Unit tests for constructors and validation |
| `refresh_target.go` | `RefreshTarget` type, constructors, validation, `RefreshResult`, `RefreshPlanView` |
| `refresh_target_test.go` | Unit tests |
| `execute_refresh.go` | `executeRefresh` — single-lock-session down-then-up execution |
| `planner_refresh.go` | `buildRefreshPlan` — composes down + up plans |
| `preview_refresh.go` | `PreviewRefresh` implementation |
| `integration/selective_rollback_test.go` | Integration tests for selective down |
| `integration/refresh_test.go` | Integration tests for refresh |

### Modified files

| File | Change |
|------|--------|
| `types.go` | Export `DownTarget`, `RefreshTarget`, `RefreshResult`, `RefreshPlanView`; update `Down`/`PreviewDown` docstrings |
| `planner.go` | `buildDownPlan` signature → accept `DownTarget`; add by-name and by-batch selection logic |
| `execute_impl.go` | `Down`/`PreviewDown` call `buildDownPlan` with `DownTarget` |
| `preview_impl.go` | `PreviewDown` call `buildDownPlan` with `DownTarget` |
| `errors.go` | Add `ErrMigrationNotFoundInLatestBatch`, `ErrBatchNotLatest`, `ErrRefreshNothingToRollback` sentinels |
| `cmd/lamigrate/main.go` | Parse `--batch` for down; parse `refresh` command; parse `--step`/positional name for refresh; add exit-code mappings for new sentinels → `ExitUsage` (code 2) |
| `cmd/lamigrate/main_test.go` | Tests for new CLI parsing |
| `cmd/lamigrate/render.go` | Render `RefreshResult`/`RefreshPlanView`; add error category mappings for new sentinels; add `"refresh"` case to `verbPresent`/`verbPast` |
| `cmd/lamigrate/confirm.go` | Add `ConfirmRefresh(yes bool)` following existing `ConfirmReset` pattern |
| `api_test.go` | Update `Down`/`PreviewDown` calls from `StepLimit` to `DownTarget` constructors |
| `integration/execute_test.go` | Update `Down`/`PreviewDown` calls from `StepLimit` to `DownTarget` constructors |
| `integration/repair_test.go` | Update `Down` calls from `StepLimit` to `DownTarget` constructors |
| `README.md` | Update library usage examples (lines ~257, ~261) to use `DownTarget` constructors |
| `docs/cli-reference.md` | Document new `down` flags + full `refresh` command |
| `docs/known-limitations.md` | Mark selective rollback and refresh as addressed |

### Error sentinels

| Sentinel | Meaning | Exit code | JSON category |
|----------|---------|-----------|---------------|
| `ErrMigrationNotFoundInLatestBatch` | Name target not in latest batch | 2 (ExitUsage) | `"invalid_config"` |
| `ErrBatchNotLatest` | Batch target != latestBatch | 2 (ExitUsage) | `"invalid_config"` |
| `ErrRefreshNothingToRollback` | No applied migrations to refresh | 2 (ExitUsage) | `"invalid_config"` |

---

## Implementation Order

### Phase 1: Core types + validation (no behavior change)

1. Create `down_target.go` with `DownTarget`, constructors, validation.
2. Create `refresh_target.go` with `RefreshTarget`, constructors, validation, `RefreshResult`, `RefreshPlanView`.
3. Add unit tests in `down_target_test.go` and `refresh_target_test.go`.
4. Add error sentinels to `errors.go` (all three, with exit-code mapping noted).

### Phase 2: Selective down planner + test migration

1. Change `buildDownPlan` signature to accept `DownTarget`.
2. Keep the legacy `Limit` path working exactly as before.
3. Add by-name selection logic: candidates are reverse-ordered (newest first);
   slice `candidates[0..found]` inclusive (named migration + everything newer).
4. Add by-batch validation logic: `target.Batch != latestBatch` → wrapped error.
5. Update `execute_impl.go` and `preview_impl.go` to pass `DownTarget`.
6. Update `Down`/`PreviewDown` public signatures to accept `DownTarget`.
7. **Migrate ALL callers** of the old `Down(ctx, StepLimit)` signature:
   - `api_test.go` — update `m.Down(ctx, lm.All())`, `m.Down(ctx, zero)` etc.
   - `integration/execute_test.go` — update 10+ `m2.Down(ctx, lamigrate.All())` calls.
   - `integration/repair_test.go` — update `m2.Down(ctx, lamigrate.All())` call.
   - All tests MUST pass after these updates (the legacy path is preserved via
     `DownTarget.Limit` for backward-compatible behavior).

### Phase 3: Selective down CLI wiring

1. Add `--batch N` flag parsing to `cmd/lamigrate/main.go`.
2. Add positional-name detection (non-numeric arg → migration name).
3. Add mutual exclusivity check (`--batch` vs `--step` vs positional name).
4. Construct `DownTarget` and pass to `Down`/`PreviewDown`.
5. Add exit-code mappings for new sentinels in `exitCodeForError()`.
6. Add error-category mappings for new sentinels in `errorCategory()` (render.go).
7. Update `printUsage()` with new examples.
8. Add CLI unit tests.

### Phase 4: Refresh planner + execution

1. Create `planner_refresh.go` with `buildRefreshPlan`.
2. Create `execute_refresh.go` with `executeRefresh` (single-lock-session protocol).
3. Add `Refresh`/`PreviewRefresh` methods to `execute_impl.go`/`preview_impl.go`.
4. Implement refresh plan rendering in `cmd/lamigrate/render.go` (`RefreshPlanView`).
5. Add `"refresh"` case to `verbPresent`/`verbPast` in render.go.

### Phase 5: Refresh CLI wiring

1. Add `refresh` to `isDatabaseCommand`.
2. Parse refresh args: `--step N`, positional name, or bare.
3. Add `ConfirmRefresh(yes bool)` to `cmd/lamigrate/confirm.go`.
4. Call `ConfirmRefresh` before constructing the `RefreshTarget`.
5. Construct `RefreshTarget` and pass to `Refresh`/`PreviewRefresh`.
6. Update `printUsage()` with refresh examples.
7. Add CLI unit tests.

### Phase 6: Integration tests

**Selective down:**
1. By-name rollback: apply A,B,C in batch 1, `down B` → plan contains [C,B] (2 migrations).
2. By-name rollback: apply A,B,C,D in batch 1, `down B` → plan contains [C,B] (not [B,A]).
3. By-name with name not in latest batch → error with correct batch number.
4. By-batch with latest batch → same as bare `down`.
5. By-batch with non-latest batch → error with correct batch numbers.
6. Mutual exclusivity (`--batch 3 --step 5` → error).

**Refresh:**
7. Bare refresh: apply 3, refresh → all rolled back, all re-applied.
8. Refresh --step 2: apply 5, refresh --step 2 → last 2 rolled back, 2 re-applied.
9. Refresh --step 100: apply 6, refresh --step 100 → all 6 rolled back, all re-applied (clamped).
10. Refresh by-name: apply 5, refresh migration_3 → all rolled back, re-applied up to migration_3.
11. Refresh --pretend: shows both plans, no DB change.
12. Refresh failure in up phase: verify partial state is recoverable via `up`.

### Phase 7: Documentation

1. Update `docs/cli-reference.md` with new down flags + full refresh command docs.
2. Update `docs/known-limitations.md` (section 5, feature limitations).
3. Update `README.md` library examples (lines ~257, ~261) to use `DownTarget` constructors.

---

## Edge Cases

### Selective down

| Case | Behavior |
|------|----------|
| `down name` where name is pending (not applied) | Error: `migration is not applied` |
| `down name` where name is in an older batch | Error: not in latest batch |
| `down name` where name is the oldest in latest batch | Equivalent to `down` (rollback entire batch) |
| `down name` where name is the newest in latest batch | Rollback only that one migration |
| `down --batch 0` (legacy baselines) | Error: batch 0 (imported baselines) cannot be rolled back |
| `down name` with `--step` | Error: mutually exclusive |
| `down --batch N` with `--step` | Error: mutually exclusive |
| `down name` with `--pretend` | Shows plan without executing |
| `down name` with `--json` | Structured output with error or plan |

### Refresh

| Case | Behavior |
|------|----------|
| `refresh` with 0 applied migrations | Error: nothing to refresh |
| `refresh --step 100` (more than applied) | Clamped: rollback all + re-apply all |
| `refresh <name>` where name is already the only migration | Rollback all + re-apply that one |
| `refresh <name>` where name doesn't exist | Error after rollback: migration not found |
| `refresh <name>` where name is pending | Rollback all + re-apply up to it (it becomes applied) |
| `refresh` with `--pretend` | Shows both down and up plans, no DB change |
| `refresh` with `--json` | Structured output with both results |
| `refresh` in non-interactive mode without `-y` | Error: confirmation required |

---

## Safety Considerations

### Selective down

- The execution protocol (`executeRollbackOne` §11.3) is unchanged — checksum
  verification, `rolling_back` state mark, lock re-verification, session
  inspection all happen per-migration. The selective rollback only changes
  **selection**, not **execution**.
- No new lock protocol changes.
- The `--pretend` dry-run works with both new modes.
- Dirty state detection still blocks all operations (no relaxation).

### Refresh

- Both phases run in a single advisory-lock session — no concurrent
  interference.
- Each migration in each phase uses the standard §11.2/§11.3 protocol
  (checksum verify → state mark → execute → session inspect → state commit).
- If the rollback phase succeeds but the forward phase fails, the database is
  in a consistent (partially-applied) state. `lamigrate up` completes it.
- Requires `-y` confirmation (same as `reset`).

---

## Rollback Plan

If the feature introduces regressions:
1. Revert `DownTarget` type — the `StepLimit` signature is the backward path.
2. Remove `--batch` and positional-name parsing from CLI.
3. Remove `refresh` command entirely.
4. All existing tests pass on revert (the legacy path was never modified).

---

## Verification Checklist

### Selective down

- [ ] `go test ./...` passes (existing + new tests)
- [ ] `go vet ./...` clean
- [ ] `./lamigrate -dir sql/migrations status` shows expected state after
      selective rollback
- [ ] `./lamigrate -dir sql/migrations --pretend down <name>` shows correct plan
- [ ] `./lamigrate -dir sql/migrations --pretend down --batch N` shows correct plan
- [ ] Mutual exclusivity errors are clear and actionable
- [ ] Error messages include migration name, batch, and guidance
- [ ] JSON output works with new modes (`--json down <name>`)
- [ ] Exit code is 2 (not 1) for validation errors
- [ ] Integration tests cover all edge cases

### Refresh

- [ ] `./lamigrate -dir sql/migrations -y refresh` rolls back all + re-applies all
- [ ] `./lamigrate -dir sql/migrations -y --step 3 refresh` rolls back 3 + re-applies 3
- [ ] `./lamigrate -dir sql/migrations -y refresh <name>` rolls back all + re-apply up to name
- [ ] `./lamigrate -dir sql/migrations --pretend refresh` shows both plans without executing
- [ ] `./lamigrate -dir sql/migrations --pretend --step 2 refresh` shows correct step-limited plan
- [ ] `./lamigrate -dir sql/migrations --json -y refresh` produces valid JSON output
- [ ] Non-interactive mode without `-y` correctly blocks refresh
- [ ] Integration tests cover refresh failure recovery
- [ ] `verbPresent("refresh")` returns "refresh", not "process"
