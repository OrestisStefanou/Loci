# Loci

Loci stores everything an agent session produces (full conversations, plus memories an agent chose to keep), organised by the work it belongs to (project, task, tags), with operations available to agents (MCP) and people (CLI). One Go binary, `loci`, over a local Turso database that a single server process, `loci serve`, owns.

## Source of truth

- `docs/product.md`: what the product is. Do not add features it excludes (section 9).
- `docs/architecture.md`: how the processes and packages fit together, and the path one command takes. Read it first if you are new to the code.
- `docs/design.md`: how it is built. Before starting work, read the sections the task touches. Operation names and arguments come from section 6, the schema from section 4.
- If implementation diverges from design.md, or resolves one of its open points (section 13), update design.md **in the same change**. Never let code and design silently disagree.

## Current scope

Milestones 1 to 3 (design.md section 12), **CLI only** plus the Claude Code mod. Capture (design.md section 5) is done: the capture operations, the `loci capture claude-code` translator and `loci init` (per-repository, never global settings), `loci conversations`, `loci show` and `attach` on conversations. The agent attaches its session (design.md section 7): the attach instruction with the first prompt and the `/loci` skill over the CLI, written per repository by `loci init` (done). The person attaches it from the `loci-pick` mod, a card above the prompt that `loci init` also writes (milestone 2, done; its TypeScript lives in `internal/capture/mod`, checked by `make mod-check`). The agent also saves memories (`remember --conversation`, done); memories share their conversation's attachment. Milestone 3, conversations searchable by meaning (design.md section 12), is built: message embeddings and hybrid search on `loci conversations`, with the matching message per result. Session-start retrieval is parked (section 8). Do not start work beyond the current step unless asked, but keep every operation adapter-agnostic so they plug in without refactoring.

## Commands

```sh
make check   # fmt-check, vet, golangci-lint, go test -race. Must pass before you say work is done
make test    # tests only
make build   # bin/loci, CGO_ENABLED=0
make mod-check   # validate and test the loci-pick mod; needs the claude binary, not part of check
bin/loci --db /tmp/x.db ls tasks   # try a change by hand against a throwaway database
```

Go 1.27 (pinned via `toolchain` in go.mod). The binary is pure Go: never introduce cgo.

## Layout

```
cmd/loci/          main: calls cli.Execute and nothing else
internal/domain/   types, normalisation, sentinel errors, Clock/IDGen. No I/O, no internal imports
internal/app/      operations, one file each. The only home of business logic
internal/store/    all SQL. database/sql over tursogo. Migrations in store/migrations/
internal/rpc/      HTTP over a Unix socket: handler (server side) and Client (implements app.Service)
internal/daemon/   loci serve lifecycle: lock, socket, start on demand, version handover
internal/cli/      cobra adapter, one file per command
internal/embed/    Embedder interface, Ollama client, Fake for tests. Only cli/serve.go picks one
internal/config/   ~/.loci/config.toml and .loci.toml (a repository's store). Add a field only when code reads it, and document it in README Configuration
internal/version/  build identity: stamped by the Makefile, else derived from the binary
internal/render/   Markdown for people: loci show now, export and the context block later
internal/capture/  translators from agent tools' events to the capture operations (Claude Code), settings editing, the /loci skill and the loci-pick mod (capture/mod, TypeScript) for init
internal/testutil/ real store on a temp file, fixed clock, sequential IDs, short socket dirs
```

Reserved, create when the work starts: `internal/mcp` (MCP adapter, go-sdk), `internal/redact`.

## Process model and layering

Only `loci serve` opens the database (Turso locks the file to one process; design.md section 3). Every other process (CLI, capture, and later MCP) reaches the operations through `rpc.Client`, which starts the server if it is not running.

```
cli / mcp / capture  ->  app.Service  =  rpc.Client  --socket-->  rpc handler  ->  *app.App  ->  store
                                   or *app.App (in tests)
```

- Adapters depend on `app.Service`, never on `*app.App`, `store` or `daemon` internals. They parse input, call **one** operation, render the output.
- Never call `store.Open` outside `daemon` and `testutil`. A second process opening the database will fail.
- `app` never imports cobra, MCP or `fmt.Print*`. It returns data and errors.
- `domain` imports no internal package.
- Only `store` writes SQL. Engine-specific SQL (full-text, vectors, sync) stays in `store` so the `go-libsql` fallback only touches that package.

## Patterns

**Operation.** `internal/app/<snake_name>.go` holds `XInput`, `XOutput` and `func (a *App) X(ctx, XInput) (XOutput, error)`. Input/Output fields carry `json` tags (and `jsonschema` descriptions) because the MCP adapter will use these structs as tool schemas, and `--json` prints Output as is. The op validates and normalises its own input. Reference: `app/set_task_project.go` (write) and `app/list.go` (read).

**Wiring an operation.** Every operation appears in four places, each one line except the first: the method on `*App`, the `app.Service` interface (`app/service.go`), a `handle(...)` line in `rpc/handler.go`, and a method on `rpc.Client` (`rpc/client.go`) calling `call[...]`. The route name is the snake_case operation name. The compiler enforces the interface and the client; `TestEveryOperationHasARoute` in `rpc` catches a missing or misspelled route. For a new operation, use the `/add-operation` skill.

**Store.** One concrete `*store.Store`, no interface. Query methods live on `queries`, so they exist on both `Store` and `Tx`. Multi-step writes go through `store.WithTx`. Lazy creation is `EnsureX` (insert `ON CONFLICT DO NOTHING`, then select). Every `rows` loop ends with `return out, rows.Err()`. Return `[]T{}`, not nil, so JSON prints `[]`. Timestamps are RFC 3339 UTC text (`formatTime`/`parseTime`). `store.Open` sets WAL and a 5000ms busy timeout, and turns on foreign keys for every pooled connection (`connPragmas`). Foreign keys are enforced, so insert parents first (`EnsureX`). A per-connection setting belongs in `connPragmas`, never in a one-off `Exec`, which only reaches one connection.

**Effective project.** An item's project is its task's project when the task has one, otherwise its own `project_id`. Resolve it at read time in one store SQL helper. Never copy it onto rows.

**Migrations.** Add `store/migrations/NNNN_description.sql`. Never edit a migration that has been merged. They are embedded and applied on `store.Open`, so tests always run the real schema.

**IDs and time.** ULIDs from `domain.IDGen`, time from `domain.Clock`, both injected through `app.Deps`. Never call `time.Now()` or generate IDs elsewhere in `app` or `store`.

**Names.** Project slugs and tags are lowercased, task keys uppercased, by `domain.Normalize*`. Always normalise before lookup or insert.

**Errors.** Wrap with `fmt.Errorf("context: %w", err)`. Use `domain.ErrInvalid` / `ErrNotFound` / `ErrConflict` for expected failures. The CLI exits 2 on `ErrInvalid` and on argument errors (use `exactArgs`/`rangeArgs`), 1 on anything else. No panics outside `main`.

**CLI output.** Print through `r.render(out, textFn)` so `--json` works on every command. Text goes to stdout, errors to stderr. Get the operations inside `RunE` (`svc, err := r.service(ctx)`) so `--help` and bad arguments never start a server.

**Capture.** The capture operations take explicit arguments and know nothing about any agent tool; a translator (`loci capture claude-code`) maps one tool's events onto them. A translator must never block or fail the agent: hard timeout on the client call (3s), log to `capture.log` in `daemon.LogDir` (ids only, never content), always exit 0 with empty stderr. The plain `loci capture start|message` commands keep normal exit codes. Capture runs on every turn: a call to a running server takes about 7ms, and starting one about 20ms. Keep it that way: no network or heavy init on the client path.

**Server.** Long-running work (sync, embedding) belongs in `loci serve` as goroutines, not in client processes. Every build has its own identity (`version.Get()`: stamped by `make build`, otherwise derived from the binary's modification time and size), and a client with a different identity replaces the running server, so after rebuilding, with `make` or plain `go build`, you never talk to stale code. `daemon.Starter` launches a server on every call; the lock makes extra launches harmless. `loci status` and `loci stop` inspect and stop it.

## Testing

- Tests are black-box (`package x_test`). When a test needs an internal, expose it in the package's `export_test.go` (see `store`, `version`) rather than making it exported or testing from inside the package.
- Tests use the **real store** via `testutil.NewStore(t)` / `testutil.NewApp(t)`. Never mock the store. Only external services (Ollama, Turso Cloud) get interfaces and fakes.
- Table-driven, with `t.Run` names that say the behaviour.
- Most coverage lives at the `app` level. Store tests cover SQL edge cases. CLI tests call `cli.Execute` with `Env.Service` set to an in-process `testutil.NewApp(t)` and cover parsing, output and exit codes.
- `internal/daemon` tests run the real server in-process over a socket (start on demand, errors, version handover, lock, concurrency). `cmd/loci` has one end-to-end test with real binaries as separate processes. Neither needs changing for a new operation.
- Sockets go in `testutil.ShortDir(t)`, not `t.TempDir()`: macOS limits socket paths to 103 bytes.
- Fixtures use made-up identifiers (`EPD-1`, `northwind`). Never put real client data, company names, account numbers or secrets in tests or examples.

## Do not

- Add an ORM, a query builder, a DI framework or a mocking library.
- Add a dependency without saying why in the PR. Current ones: cobra, tursogo, oklog/ulid, BurntSushi/toml.
- Store project or task metadata (names, statuses, descriptions). The slug or key is the whole identity.
- Hard-delete memories for correction. Mark them `inactive`. `forget` is the only hard delete, and it exists for retention and GDPR erasure.
- Open the database from anything but `loci serve`, including ad hoc tools, while a server is running.
- Add tool calls or tool results to captured messages. Only prompts and final answers are stored (design.md section 5).

## Writing style (docs, comments, PRs)

Direct and concise. No em dashes. Tables for comparisons, prose for reasoning. British spelling in prose is fine, but Go identifiers follow Go convention (`Normalize`).

## Git

Branch off `main`. One logical change per commit. Run `make check` before committing.
