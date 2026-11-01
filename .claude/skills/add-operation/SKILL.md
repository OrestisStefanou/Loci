---
name: add-operation
description: Add or change a Loci operation (remember, search memories/conversations, show, attach, mark, set context, etc.) end to end across domain, store, app, server wiring, CLI and tests, following the repo's reference slice. Use whenever a task asks for a new loci command, MCP tool or operation from docs/design.md section 6.
---

# Add an operation

An operation is one capability from `docs/design.md` section 6, implemented once in `internal/app` and exposed by thin adapters. Copy the reference slice; do not invent a new shape.

| Layer | Reference (write op) | Reference (read op) |
|---|---|---|
| app | `internal/app/set_task_project.go` | `internal/app/list.go` |
| app test | `internal/app/set_task_project_test.go` | `internal/app/list_test.go` |
| store | `internal/store/tasks.go` (`EnsureTask`, `SetTaskProject`) | `internal/store/projects.go` (`ListProjects`) |
| wiring | `internal/app/service.go`, `internal/rpc/handler.go`, `internal/rpc/client.go` | same |
| cli | `internal/cli/task.go` | `internal/cli/ls.go` |
| cli test | `internal/cli/cli_test.go` | same |

## Steps

1. **Pin the contract.** Find the operation in design.md section 6: MCP tool name, CLI name, arguments. Read the sections it depends on (section 4 rules for data, section 6 for search, section 9 for redaction/retention). If the task needs something section 6 does not say, decide it, then record the decision in design.md as part of the change.

2. **Domain.** Add types or constants to `internal/domain` only if more than one package needs them. Reuse `Normalize*` for every project, task or tag argument.

3. **Schema.** If the schema changes, add `internal/store/migrations/NNNN_description.sql` with the next number. Never edit an existing migration. Keep engine-specific SQL (FTS, vectors) in `store`.

4. **Store.** Add query methods on `queries` in the file for the main table (`internal/store/<table>.go`). Use `EnsureX` for lazy creation. Wrap errors with context, map `sql.ErrNoRows` to `domain.ErrNotFound`, return `[]T{}` rather than nil, end row loops with `rows.Err()`. Add tests to `internal/store/store_test.go` only for SQL edge cases (conflicts, nulls, ordering, filters).

5. **App.** Create `internal/app/<snake_name>.go`:
   - `XInput` and `XOutput` with `json` tags and `jsonschema` descriptions. Optional fields are pointers or `omitempty`. These become the MCP tool schema, so name fields as section 6 does.
   - `func (a *App) X(ctx context.Context, in XInput) (XOutput, error)`: validate and normalise first (`domain.ErrInvalid`), then do the work. Use `a.store.WithTx` for more than one write. Get IDs and time only from `a.newID()`.
   - Add a table-driven `<snake_name>_test.go` in `package app_test` using `testutil.NewApp(t)`. Cover the happy path, normalisation, lazy creation, not found and invalid input.

6. **Wire it through the server.** Each is one line, mirroring the existing operations:
   - add the method to `app.Service` in `internal/app/service.go`
   - add `handle(mux, ctl.Logger, "<snake_name>", svc.X)` in `rpc.NewHandler` (`internal/rpc/handler.go`)
   - add `func (c *Client) X(...)` returning `call[app.XOutput](ctx, c, "<snake_name>", in)` in `internal/rpc/client.go`

   The compiler fails until the interface and client match, and `TestEveryOperationHasARoute` (`internal/rpc`) fails until the route exists. Do not add other tests in `rpc` or `daemon` for a new operation; they test the transport.

7. **CLI.** Create or extend `internal/cli/<command>.go` with a `(r *root) xCommand()` method and register it in `root.command()`. Parse args/flags into `XInput`, then `svc, err := r.service(ctx)` -> `svc.X(ctx, in)` -> `r.render(out, textFn)`. Use `exactArgs`/`rangeArgs` and return `domain.ErrInvalid` for flag combinations, so bad usage exits 2. Add cases to `internal/cli/cli_test.go` (they run against an in-process `testutil.NewApp(t)`): one success path with text output, one `--json` decode, and the exit codes.

8. **MCP** (only once `internal/mcp` exists). Register the tool with the section 6 name, using `XInput`/`XOutput` directly as the typed handler's input and output, calling the operation through `app.Service`. No logic in the handler.

9. **Verify.** Run `make check`. Then try it by hand: `make build && bin/loci --db /tmp/loci-try/l.db <command> ...` (this starts a server for that database; the rebuilt binary replaces any older one). Check text output, `--json`, and one bad input. Finish with `bin/loci --db /tmp/loci-try/l.db stop`.

10. **Docs.** If arguments, behaviour or schema differ from design.md, update it. If an open point in section 13 was answered, record the answer there.

## Checklist before done

- [ ] Operation added to `app.Service`, `rpc.NewHandler` and `rpc.Client`
- [ ] No `store` import outside `app`, `daemon` and `testutil`
- [ ] No printing in `app`; no SQL outside `store`
- [ ] Every name argument normalised through `domain`
- [ ] Errors wrap domain sentinels where the caller should react
- [ ] App, CLI (and store, if new SQL) tests added
- [ ] `make check` passes
- [ ] design.md updated if anything diverged
