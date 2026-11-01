# Loci: System Design

Status: design, build in progress (milestone 1)
Date: 2026-09-27
Depends on: `product.md` (2026-09-22), which defines the product. This document decides how it is built.
Verification: every capability claim about Claude Code and Turso below was checked against primary sources on 2026-09-27 (code.claude.com docs, docs.turso.tech, GitHub via the API, pkg.go.dev). Anything not verified is listed in section 12.

---

## 1. Requirements added since the product definition

Two requirements arrived after `product.md` was written. Both are in scope.

**Cross-repository projects.** A project is a body of work, not a repository. `fees-v2` and `trade-settlement` are both implemented across the same repositories (`northwind-backend`, `web`, `portal`), so a repository says nothing about which project a session serves. The repository is recorded on every conversation as provenance only: nothing is inferred from it and nothing is retrieved by it.

**Participants.** Every stored message records who said it and to whom. In a plain session that is the person and the main agent. When the main agent delegates to a subagent such as `tech-pm-agent`, the exchange between them is recorded as a separate conversation with the subagent as a participant. This is feasible with what Claude Code exposes today (section 5), with one limit: for a subagent, the hooks expose its final answer and its type, and possibly not the prompt the main agent gave it.

## 2. Decisions

| Decision | Choice | Why |
|---|---|---|
| Language | Go | Capture runs on every turn and must start in milliseconds, which a static binary does. The official MCP SDK is Go. No cgo needed with the chosen database driver |
| Database | Turso Database via `turso.tech/database/tursogo` | Same SQL locally and shared. Native full-text search and vectors. Sync is a library call, not a service to build |
| Process model | One server process, `loci serve`, owns the database. Every other entry point is a client over a Unix socket and starts the server on demand | `tursogo` locks the file to one process in its default mode. Its `multiprocess_wal` alternative is experimental with open data-loss bugs. A single owner also gives sync and the embedding worker one long-lived home. See section 3 |
| Deployment | Local-only first, sync to Turso Cloud later | One person runs it alone until the store is proven worth reading. Sync is designed in now and switched on at milestone 4 |
| Capture | Hooks only, not the transcript file | The transcript format is documented as internal and changing between versions. Hooks are a stable contract and carry exactly what is wanted: what the person asked and what the agent answered |
| What is stored per turn | User prompt and final assistant message | How the agent arrived at an answer is not the point. Tool calls and results are not stored, which also removes most secret-bearing content |
| Session context | Loci infers nothing. Whoever calls capture supplies project and task, or they are set later; for Claude Code the agent sets them | A repository serves many projects, and how task keys appear in branch or directory names is a team convention, not Loci's. The product says nothing has to be right at the moment of writing, so correction is cheap. See section 7 |
| Search | Hybrid: full-text plus local embeddings, on memories and messages | Both are stored at write time. Embeddings are computed by a local model so no conversation content leaves the machine |

### Why Turso Database and not libSQL

Turso ships two engines. libSQL is the SQLite fork with `go-libsql` (cgo, embedded replicas that read locally and write to a primary) and a self-hostable server. Turso Database is the Rust rewrite with `tursogo` (no cgo, local reads and writes, push/pull sync). Turso's own documentation positions `go-libsql` as being for existing libSQL codebases and `tursogo` as the default for local and embedded use.

| | `tursogo` (chosen) | `go-libsql` (fallback) |
|---|---|---|
| Build | Pure Go, `purego` FFI | cgo, `CGO_ENABLED=1` |
| Local writes | Yes, synced later with `Push()` | No, writes go to the primary |
| Sync remote | Turso Cloud only, as documented | Turso Cloud or self-hosted `libsql-server` (MIT) |
| Full-text | Turso FTS, Tantivy-based | SQLite FTS5 |
| Vectors | `vector32()` columns, `vector_distance_cos`, linear scan | Same functions plus a `vector_top_k` index |
| Maturity | Pre-1.0: v0.8.1 in use (2026-10-01) | Production-ready per Turso, server last released 2025-02-14 |

The one thing `go-libsql` offers that `tursogo` does not is a self-hosted sync remote. If the shared store must one day live on our own infrastructure, `go-libsql` plus `libsql-server` is the fallback, and the storage package is the only thing that changes. The schema is plain SQLite apart from the full-text index and the vector functions, both of which are isolated there.

## 3. Architecture

One Go binary, `loci`. One process, `loci serve`, owns the database; every other entry point is a client of it.

| Entry point | Invoked by | Job |
|---|---|---|
| `loci serve` | Started on demand by any client; also runnable in the foreground | The only process that opens the database. Serves every operation over a Unix socket. Later: runs sync and the embedding worker |
| `loci capture claude-code` | Claude Code hooks, event JSON on stdin | Translates each event into a capture operation (section 5): every turn is stored, and a context block is injected at session start |
| `loci capture start\|message` | Any other caller, with explicit arguments | The same capture operations, for a tool without a built-in translator |
| `loci mcp` | Claude Code, as a stdio MCP server | Tools for the agent |
| `loci <command>` | A person or a script | Every operation the agent has, plus `init`, `show`, `export`, `embed`, `sync`, `forget`, and `status`/`stop` for the server |
| `loci claude` | A person, optionally | Offers a picker for the session's context, then runs `claude`. Not planned: the mod's card covers it (section 7) |

```
person ──── claude ────┬── hooks ── loci capture ───┐
                       │                            │  unix socket
                       └── MCP ────── loci mcp ─────┼──────────────── loci serve ── storage ── loci.db ── (Push/Pull) ── Turso Cloud
                                                    │                     │
person ──── loci <command> ─────────────────────────┘                     └── embedding worker ── Ollama, localhost
```

Libraries:

| Library | Version verified | Used for |
|---|---|---|
| `github.com/modelcontextprotocol/go-sdk` | v1.8.0, 2026-09-14, official | Stdio transport, tools, prompts, `ServerSession.Elicit` (form mode) |
| `turso.tech/database/tursogo` | v0.8.1, verified 2026-10-01 | Local database, `NewTursoSyncDb` for sync |
| Ollama HTTP API | local | Embeddings, default model `nomic-embed-text`, 768 dimensions |

### Single owner: `loci serve`

Capture, the MCP server and the CLI are separate, mostly short-lived processes, often from several sessions at once. In its default mode `tursogo` locks the database file to one process: a second process fails with `File is locked by another process`. Turso's experimental `multiprocess_wal` feature ([Multi-Process Access](https://github.com/tursodatabase/turso/blob/main/docs/sql-reference/multiprocess-access.mdx)) lifts that, but it is experimental, its on-disk format may change between releases, and it has open bugs including lost writes when one process opens without the flag ([#9362](https://github.com/tursodatabase/turso/issues/9362)). So exactly one process opens the database.

| Aspect | Behaviour |
|---|---|
| Transport | HTTP over a Unix socket, JSON bodies. `POST /op/<operation>` takes the operation's input struct and returns its output struct. `GET /status`, `POST /shutdown` |
| Files | For database `<db>`: socket `<db>.sock` (mode 0600), lock `<db>.lock`, log `<dir>/log/serve.log`. Each database has its own server. The socket path must fit in 103 bytes |
| Exclusivity | An `flock` on the lock file. A second server waits up to 3s (covering a server that is shutting down), then exits cleanly |
| Start on demand | A client that cannot connect starts `loci serve` detached (`setsid`) from its own binary, waits up to 5s for the socket, and retries once. Concurrent starts are safe: the lock leaves one server |
| Upgrades | Every request carries the client's build version. A server receiving a different version answers `version_mismatch` and shuts down; the client waits for it to go, starts its own, and retries. `make build` stamps a unique version per build. Unstamped builds (`go build`, `go run`) use the binary's modification time and size, one `stat` per call. Either way, rebuilding replaces the server |
| Database settings | WAL and a 5000ms busy timeout. `PRAGMA foreign_keys = ON` on every pooled connection: Turso, like SQLite, leaves foreign keys off by default and applies the setting per connection |
| Socket permissions | Created under umask 0177, so it is 0600 from the moment it exists |
| Handover order | Stop: close listener, remove socket, close database, release lock. Start: the reverse. The two servers never hold the database at once |
| Errors | Domain error kinds (`invalid`, `not_found`, `conflict`) cross the wire and are rebuilt client-side, so callers handle errors the same as in-process |
| Latency | Measured 2026-10-01 on darwin/arm64: 7ms for a CLI call to a running server, 10 to 20ms when the call has to start the server on an existing database |
| Platforms | macOS and Linux. Windows is not supported |

## 4. Data model

Primary keys are ULIDs generated by the client, so several machines can write without coordination once sync is on, and rows sort by creation time.

| Table | Columns |
|---|---|
| `projects` | id, slug (unique), created_at |
| `tasks` | id, key (unique, e.g. `EPD-100`), project_id (nullable), created_at |
| `tags` | id, name (unique) |
| `actors` | id, kind (`human` or `agent`), handle (`orestis`, `claude`, `tech-pm-agent`), agent_type (nullable) |
| `conversations` | id, harness (`claude-code`), session_id, agent_id (`''` for the main conversation), parent_conversation_id (nullable, set for a subagent run), agent_type (nullable), human_actor_id, repo (normalised origin URL), branch, project_id (nullable), task_id (nullable), started_at |
| `messages` | id, conversation_id, seq, prompt_id, role (`user` or `assistant`), speaker_actor_id, addressee_actor_id, content, redactions (count), embedding (BLOB, nullable), embedding_model (nullable), embedding_failed (nullable), created_at |
| `memories` | id, content, project_id (nullable), task_id (nullable), conversation_id (nullable), message_id (nullable, the source), author_actor_id, status (`active`, `inactive`), embedding (BLOB, nullable), embedding_model (nullable), embedding_failed (nullable), created_at, updated_at |
| `item_tags` | item_kind (`conversation` or `memory`), item_id, tag_id |

Full-text indexes on `messages.content` and `memories.content` using Turso FTS.

Embeddings are columns on the rows they describe, which is the pattern Turso documents. A null `embedding`, or an `embedding_model` other than the configured one, means not yet embedded, and the worker selects on that, leaving out rows whose `embedding_failed` names the configured model (section 10). A model change is a re-embed, not a migration.

Rules:

- **Effective project.** An item's effective project is its task's project when the task has one, otherwise the item's own `project_id`. Resolved at read time, never copied. This settles open question 7 of the product definition in favour of inheritance: moving a task moves everything attached to it, and the direct link only matters for items with no task.
- **Nothing is deleted by correction.** A wrong or outdated memory is marked `inactive`. `forget` is the only hard delete and exists for retention and data protection, not for tidiness.
- **A conversation's key** is (harness, session_id, agent_id), unique. `agent_id` is empty for the session's main conversation and the tool's subagent id for a subagent run, whose `parent_conversation_id` is the main conversation.
- **Subagent runs share their session's attachment.** A subagent conversation with no project or task of its own has its parent's, resolved at read time like the effective project, so re-attaching the main conversation moves its subagent runs too. One with its own (given to `start`, or set by `attach`) keeps it; removing both makes it share its session's again. Tags are not shared: a run has only its own.
- **Memories share their conversation's attachment** (decided 2026-10-16). A memory saved with `conversation` (its source, `memories.conversation_id`) and with no project or task of its own has that conversation's effective attachment, resolved at read time, through the session when the conversation is a subagent run with nothing of its own. The agent often saves before its session is attached, or under a task later corrected with `/loci`; this way the memory follows. One with a project or task of its own keeps it; removing both makes it share its conversation's again. Tags are not shared. Rejected: copying the conversation's task at save time (does not follow a correction), and requiring the agent to pass the task (impossible while unattached).
- **Participants are derived, not asked.** In a top-level conversation the speaker alternates between the human actor and the main agent, and the addressee is the other one. In a subagent conversation the participants are the main agent and the subagent. Nobody types who they are talking to. The human actor is config `user.handle`, the main agent's handle is the harness name (`claude-code`), and a subagent's handle is its type with anything but letters, digits, `.`, `_` and `-` turned into `-` (`review-kit:code-reviewer` becomes `review-kit-code-reviewer`; an unknown type is `subagent`). The raw type is kept in `agent_type`. Known limitation: handles are unique across kinds, so a subagent type that cleans up to the same handle as the person, the harness or another type shares that actor (and its kind). Unlikely enough to accept; fixing it means keying actors by kind and handle.
- **Repositories are stored normalised**, as `host/path` lowercased (`git@github.com:Northwind/API.git` and `github.com/Northwind/API` become `github.com/northwind/api`). User names, credentials and ports in an origin URL are dropped, so a token embedded in a remote never reaches the database. Only remote repositories are kept: a local path or `file://` URL is stored as empty, because it carries the user's name and identifies nothing shared.
- **Identities are normalised.** Task keys are trimmed and uppercased (`epd-1271` becomes `EPD-1271`). Project slugs and tag names are trimmed and lowercased. All three accept letters, digits, `.`, `_` and `-`, up to 128 characters. This stops lazy creation from making duplicates that differ only in case.
- **Projects and tasks carry no metadata.** No names, statuses or descriptions, per section 9 of the product definition. The slug or key is the whole identity, created lazily on first mention.

## 5. Capture

Capture is two layers, so the store is not tied to one agent tool:

1. **Capture operations** take explicit arguments, like every other operation, and know nothing about Claude Code. A conversation is identified by `harness` (`claude-code`), `session` (the tool's session id) and, for a subagent run inside that session, `agent`. Callers never need a Loci id.

| Operation | Arguments | CLI |
|---|---|---|
| Start or resume a conversation | harness, session, agent?, agent_type?, project?, task?, repo?, branch? | `loci capture start` |
| Add a message | harness, session, agent?, role (`user` or `assistant`), content, prompt_id?, agent_type? | `loci capture message` |

`start` is an upsert and returns the conversation and whether it was created. A new conversation is attached to the project and task given, if any, and nothing is inferred (section 7); repo and branch are provenance only. On an existing conversation (a resumed session) project and task apply only while it has neither of its own, which covers a conversation an earlier message created unattached; one that has either is left as it is, so a correction survives. Changing an attachment is `attach`'s job (section 6). Every tool-supplied value (session, agent, agent type, prompt id, repo, branch) is capped at 256 characters. A message for a conversation that was never started starts it (`agent_type` is used only then). A subagent conversation (`agent` given) is a child of the session's main conversation, which is created if needed.

There is no end operation, and nothing records the working directory or the machine (decided 2026-10-10): no feature read an end time, and the directory and host name are personal data (they carry user names) with no purpose, so data minimisation rules them out. Add any of them back only with a reader. A second message with the same prompt_id and role in one conversation replaces the first one's content instead of adding a row: Claude Code fires `Stop` again for the same prompt when a stop hook makes the agent continue, and the last answer is the final one. The person on a message is the one recorded on the conversation, not the current `user.handle`. Messages are numbered per conversation (`seq`) in the order the server receives them; the server serialises capture writes, so concurrent sessions never collide. Content is stored exactly as given; whitespace-only content is invalid. Capture operations are CLI only, never MCP tools: an agent does not write its own transcript.

2. **A translator per tool.** `loci capture claude-code` reads one Claude Code hook event as JSON on stdin and calls the operations above. It is what `loci init` puts in a repository's `.claude/settings.local.json` (section 11). Claude Code runs a hook as a fixed command and passes the event only on stdin, so a translator is needed; building it in avoids per-event shell scripts and a `jq` dependency. A tool without a translator can call `loci capture start|message` directly.

Claude Code fields relied on, all documented (rechecked 2026-10-10): `hook_event_name`, `session_id`, `prompt_id` (common to all events, absent before the first prompt), `cwd`, `prompt` on `UserPromptSubmit`, `last_assistant_message` on `Stop` and `SubagentStop`, `agent_id` and `agent_type` on `SubagentStart` and `SubagentStop`. `SubagentStart` does not carry the prompt the main agent gave the subagent, so a subagent conversation holds its final answer only. A subagent run in the background may hold nothing at all: it hands its report back through a tool call (`SubagentHandback`) and writes no final text, so `SubagentStop` has no `last_assistant_message` to store (seen on the first real capture, 2026-10-11, with an `Explore` run). Its conversation is kept, empty, as the record that the run happened, with its type and parent; the main agent's answer usually carries what it found. Capturing the handback was rejected: it is a tool call, which capture does not store, and the tool is internal to Claude Code and absent from the hooks reference. `SessionEnd` is not used.

| Claude Code event | Operation |
|---|---|
| `SessionStart` | `start`, with repo and branch read from the git directory at or above `cwd`. Prints the context block to stdout, which Claude Code injects into the agent's context |
| `UserPromptSubmit` | `message`, role `user`, with `prompt_id`. Speaker is the human actor. With a session's first prompt, while it is still unattached, prints the attach instruction (section 7) |
| `Stop` | `message`, role `assistant`, with the same `prompt_id`. Speaker is the main agent |
| `SubagentStart` | `start` with `agent` and `agent_type` |
| `SubagentStop` | `message` with `agent`, the subagent's final answer |

How the translator does it (built 2026-10-11):

- **Repo and branch** are read from files, not by running `git`, which would cost more than the rest of the call: walk up from `cwd` to the first `.git`, follow a `.git` file (`gitdir:`, worktrees and submodules) and its `commondir`, take the branch from `HEAD` (none when detached) and the first `url` of `[remote "origin"]` from `config`. Anything unreadable is stored as empty.
- **Answers with no text** (an interrupted turn) are skipped rather than rejected.
- **Subagent events with no `agent_type` are skipped.** Claude Code runs background helpers as hidden agents that fire `SubagentStop` (and no `SubagentStart`) without a type, for example the one that suggests the person's next prompt after each answer. The first real capture (2026-10-11) stored those suggestions as subagent conversations. A subagent started through the agent tool always has a type. Inferred from that capture and the hooks reference, not a documented guarantee; revisit if real subagent runs go missing.
- **Context block.** Its first line is `Loci: this session is conversation <id>, not attached to a project or task.`, or the project and task once attached. Every block, attached or not, goes on with the instruction to save memories (section 7). It is printed on every `SessionStart` source, so it returns after compaction. Commands are written as the hooks run loci: the binary by absolute path with any `--db` or `--store`, so the agent reaches the same store whatever its PATH.
- **Attach instruction.** Printed with the session's first prompt (the message with sequence 1), and only if the conversation is still unattached then (moved from `SessionStart` 2026-10-17, so a pick on the card before the first prompt is never overwritten; section 7). Its first line is `Loci: this session, conversation <id>, is still not attached to a project or task.`; then: work out the task from the branch, the worktree name or the first prompt, find its key with `ls tasks`, run `attach <id> --task <KEY>`, begin that reply with `Loci: project <slug>, task <KEY>. Say /loci to change.`, and leave it unattached rather than guess. Nothing is printed for later prompts or other events: `UserPromptSubmit` output is added to the context.
- **Never failing.** The whole call has a 3 s limit, including starting the server. Errors go to `capture.log` in the log directory next to the database (`~/.loci/log/` by default), with the event, session and agent ids and never the content. Exit is always 0 and stderr stays empty.
- **Hook entries.** One handler per event, with no `matcher` (every source and subagent type), a 10 s `timeout`, and not `async`: `SessionStart` must print, and the rest take milliseconds while `async` could reorder a prompt and its answer.

Properties that follow:

- Every turn is written as it happens. Compaction and crashes lose nothing already written, which is the product's first promise.
- `loci capture claude-code` never blocks or fails the agent. It has a hard timeout, logs errors to `capture.log` in the log directory, and exits 0 regardless. `loci capture start|message` behave like any other command (exit codes, errors on stderr), because a script calling them wants to know.
- Capture is a client of `loci serve` (section 3). If the server cannot be reached or started within the timeout, the turn is lost and the translator logs it with the session id. Decided 2026-10-10 (open point 9): starting a server takes about 20 ms, so this should be rare; a spool for the server to import on start is built only if the log shows losses.
- Redaction will happen in the operation, before the write (section 9), so every caller gets it. Not built in milestone 1; see section 9.
- The hook entries are installed per repository, by `loci init`, never in the person's global Claude Code settings (section 11). A session in a repository without them never runs Loci. The translator also checks for the repository's `.loci.toml` and stores nothing without one.
- Two sessions running at once, in the same directory or not, do not interfere. Every call carries its own session and nothing is keyed by directory or machine.

## 6. Operations

Each operation exists as an MCP tool and as a CLI command, with the same name and arguments. The capture operations (section 5) are the exception: CLI only.

| Operation | MCP tool | CLI |
|---|---|---|
| Save a memory | `loci_remember` (content, project?, task?, tags?, author?, conversation?) | `loci remember` |
| Search memories | `loci_search_memories` (project?, task?, tags?, query?, include_inactive?, limit?) | `loci memories` |
| Search conversations | `loci_search_conversations` (project?, task?, tags?, query?, harness?, session?, include_subagents?, limit?) | `loci conversations` |
| Retrieve a conversation | `loci_get_conversation` (id, around_message?, window?) | `loci show <id>` |
| Select context for this session | `loci_set_context` (project?, task?, tags?) | `loci context set` |
| Attach or re-attach an item | `loci_attach` (item, project?, task?, tags?) | `loci attach` |
| Set a task's project | `loci_set_task_project` (task, project?) | `loci task <key> --project` |
| Mark a memory | `loci_mark` (id, status: `inactive` or `active`) | `loci mark` |
| List or search projects, tasks, tags | `loci_list` (kind, query?) | `loci ls` |

**Author.** `remember` records who saved the memory. From the CLI that is the person in config `user.handle`, stored as a `human` actor. An agent passes its handle in `author` and is stored as an `agent` actor: the Claude Code main agent passes `claude-code`, the handle capture gives it, so its memories and its messages share one actor. Nobody is asked; the adapter knows who it serves. Handles are normalised like slugs.

**Search semantics.** For memories as below. Conversations follow the same rules, with the differences after the table.

| Filter | Rule |
|---|---|
| `project` | Matches the effective project (section 4), so memories on the project's tasks are included |
| `task` | That task only. Folding in project-level memories is the session-start retrieval's job (section 8) |
| `tags` | All given tags must be present |
| `query` | Case-insensitive substring of the content, fused with the nearest memories by embedding when embeddings are on (see Search below). Substring stands in for full-text search until it lands (open point 3). The field keeps its name so the schema does not change then |
| status | `active` only unless `include_inactive` |
| unknown project, task or tag | Empty result, not an error: nothing is attached to it |
| order and `limit` | Without `query`, most recent first (`created_at`, then id). With one, by fused rank. Default 50, at most 500, applied after fusion |

Conversations (built 2026-10-13):

- `project` and `task` match the effective attachment, so a subagent run with nothing of its own matches its session's (section 4). `tags` must be on the conversation itself.
- `query` is a case-insensitive substring of any message, fused with the conversations whose messages are nearest by embedding when embeddings are on, as for memories (Search below; built 2026-10-18, milestone 3). A conversation ranks by its single nearest message, and `embedding.max_distance` applies to that message. A session also matches through its subagent runs' messages, by substring and by meaning, so a finding an `Explore` run reported leads to the session that asked for it.
- With a query, each result carries `match`: the message that made it a hit (`message`, its own `conversation`, which for a session can be a subagent run, and a `snippet` of one line, 120 characters). The match is the nearest message by meaning, or, for a conversation found only by substring, its first message containing the query, with the snippet cut around the query. The pair is what `loci show <conversation> --around <message>` takes, so a hit can be read in context. Decided 2026-10-18 over listing conversations alone (no way to find the hit in a long session) and listing messages (a different shape from the listing without a query). Short messages ("yes", "ok") are embedded like any other; the cut-off keeps them out of most results.
- Only sessions' main conversations are listed unless `include_subagents`; each carries its message count, its count of subagent runs and its first message cut to one line of 120 characters, so a person can recognise it. There is no status, so no `include_inactive`.
- Without a query, most recent first by `started_at`, then id; with one, by fused rank, ties to the later start. Default 50, at most 500, applied after fusion.
- `harness` and `session` match the agent tool and its session id, so a picker in the tool finds its own session's conversation (section 7; added 2026-10-17). `session` needs `harness`, because a session id is only unique within its tool.

**List.** Projects and tags are listed by name. Tasks are listed most recently used first, then by key (decided 2026-10-17, for the picker and the CLI alike): a task's last use is the latest of its creation, a conversation on it starting, a message in one, and a memory saved on it.

`remember --task T --project P` stores both links and does not change T's project; that is `set_task_project` (product open question 4). While T has a project, the effective project comes from T.

**Attach.** Only the fields given change: an omitted field is left as it is, an empty `project` or `task` removes it, and `tags` replaces the item's whole tag set (an empty list removes them all). At least one field is required. Projects, tasks and tags are created lazily, as in `remember`, and setting the task does not change the task's project. `item` is a memory's or a conversation's id, which are unique across tables; the output carries `memory` or `conversation`, whichever it was. Conversations have no `updated_at`.

**Mark.** A memory is `active` or `inactive`. To correct one, mark it `inactive` and `remember` the right statement; when that one turns out wrong too, repeat. There is deliberately no link from a memory to its replacement: decided 2026-10-10, because a link needs rules against cycles and dangling targets (a memory superseded by one later marked inactive would stay hidden with nothing pointing at the truth), and the replacement is normally on the same task with a later date, so `--all` shows them together. One status rather than `wrong` and `outdated` because the caller often cannot tell which, and agents would classify inconsistently. `active` restores a memory, so a mis-mark is undone without deleting anything. Embeddings are kept; search leaves out inactive memories. This changed `0001_init.sql` in place, allowed once because no database held real data yet.

**Session identity.** The MCP server is a separate process and is not documented to receive the session id. The context block printed at `SessionStart` therefore contains the `conversation_id`, and every session-scoped tool requires it. There is deliberately no fallback to "the most recent conversation in this directory", because two sessions can run in one directory.

**Search.** Every query is bounded by project and task first, then ranked. Ranking merges text hits and vector-similarity hits with reciprocal rank fusion (k = 60): each list contributes up to max(100, 3 x limit) candidates, capped at 500, and a result scores 1/(60 + rank) per list it appears in. Ties go to the newer item, then the greater id. Vector matches farther than `embedding.max_distance` (cosine, default 0,40) are dropped before fusion, so a query with nothing related returns only its substring hits rather than the least-bad memories. 0,40 comes from a 2026-10-04 sample with `nomic-embed-text`: related pairs measured 0,29 to 0,38, unrelated 0,43 to 0,64. The scale is model-specific, hence configurable; re-tune on real data. Until Turso FTS is usable (open point 3) the text list is the substring matches, most recent first. Vector similarity is a linear scan over the bounded subset, which is the engine's behaviour and is acceptable at the volumes one project produces; measuring this is an open point.

The output carries `ranking`: `recency` (no query), `text` (substring only: embeddings off, or the provider unreachable) or `hybrid`. When embeddings are on but the provider cannot be reached, the output also sets `semantic_unavailable` and the CLI prints a note to stderr; search never fails because of it. Any other embedding error (a missing model, say) is returned, because it is misconfiguration, not downtime. Implemented for memories and, since milestone 3, conversations.

**Retrieve a conversation.** `loci show <id>` prints one conversation as Markdown (`internal/render`): a header with its attachment, start, repository and session, then each message in order, then its subagent runs (id, type, message count). The runs are listed, not interleaved with the messages: nothing records which prompt started a run, and placing it by time would imply a link that is not stored. `loci show <run-id>` prints a run, whose header names its session. `around_message` returns only the messages within `window` (default 5, at most 500) of that message, for reading a search hit in context; `window` without it is invalid, as is a message from another conversation. The output always carries the conversation's total message count. Message headers are bold lines between rules, not headings, because an answer's own Markdown headings would otherwise read as new messages.

**Readable by people.** `loci show EPD-100` (not built yet; `show` takes a conversation id only, decided 2026-10-13) will print the active memories and then the conversations for a task as Markdown. `loci export --task EPD-100` writes the same to a file for sharing. No web interface in the first version.

## 7. Session context: set by the caller, shown, corrected

Loci infers nothing (decided 2026-10-10). A repository serves many projects, and how task keys appear in branch or directory names is a team convention, not Loci's. How a conversation's project and task are decided is up to whoever calls capture: it passes them to `start` (section 5), or they are set later with `loci_set_context` or `attach`. A conversation with neither is stored unattached, which the product allows.

For Claude Code, two layers set it (decided 2026-10-16), built in order: the agent first (milestone 1), then a picker for the person (milestone 2, built 2026-10-17). The translator passes no project or task.

| Layer | When | How | Milestone |
|---|---|---|---|
| Attach instruction | With the first prompt, only if the conversation is still unattached | The agent works out the task from what it can see (the branch, the worktree name, the person's first prompt) and runs `loci attach <conversation id>` | 1 |
| `/loci` skill | Any time, when the person asks | The agent changes the attachment as asked ("attach this to EPD-12", "drop the tags") with `loci attach` | 1 |
| Claude Code mod `loci-pick` | Session start while unattached, and `/loci-pick` | A card above the prompt: search the tasks, create a task or a project, pick one. Runs `loci ls`, `loci task` and `loci attach` | 2 |

1. **Ask the agent.** The project follows from the task when the task has one (section 4), so a task's project is set once with `set_task_project` and holds for every session on that task. When the agent cannot tell the task, it leaves the conversation unattached and says so in one line rather than guessing.
2. **Show.** Once attached, the agent begins its reply with one line: `Loci: project fees-v2, task EPD-1271, no tags. Say /loci to change.`
3. **Correct.** At any time, `/loci` or the CLI changes the attachment of the current conversation. Everything already stored follows, because attachment is a property of the conversation row.

Rules that keep the layers from competing:

- **The most recent explicit choice wins.** `attach` replaces, so a pick on the card after the agent attached overrides the agent, and `/loci` overrides both. No precedence is stored.
- **The agent only attaches an unattached conversation.** The instruction comes with the first prompt and only while there is no attachment, so a pick made on the card before typing is never overwritten. A pick made later reaches the agent as one line added to the next prompt (`Loci: the person has since attached this session to fees-v2, EPD-1271, no tags.`), so what the agent believes stays true.
- **The skill owns `/loci`, with or without the mod.** Whether a mod command and a skill may share a name is not documented, so the mod never registers `/loci`. If it needs a command to reopen its picker, it uses another name (`/loci-pick`).
- **The card is an offer, not a gate.** It is drawn in the band above the prompt and never takes the keyboard by itself: the person types a prompt as usual, presses a digit in the empty prompt to pick one of the listed tasks, or presses ctrl+x tab (or clicks) to search. A prompt sent without picking leaves the attaching to the agent; Not now hides the card. Once attached, the card becomes one summary line, gone after the next prompt.
- **Why a card and not a pane** (decided 2026-10-17, after a prototype). In the fullscreen layout a pane on a terminal of 110 columns or more always docks as a full-height sidebar, whatever height it asks for; the built-in question dialog allows four options and its text is unreadable in a light theme. The band above the prompt is drawn the same in every layout, takes the mod's own colours and a text field, and shrinks to the rows it is given (framed rows, one line per row, then no frames).
- **The mod is thin.** UI only: it runs `loci` (`ls`, `conversations --harness --session` to find its conversation, `task`, `attach`) and keeps nothing but what the card shows. Capture stays on hooks and attachment on `attach`, so a Claude Code release that breaks the mod loses the card and nothing else. Mods are an early-access Claude Code surface; the mod's tests (`make mod-check`, which needs the `claude` binary) are kept out of `make check` and CI.
- **Per repository.** `loci init` writes the skill and the mod under the repository's `.claude/skills/`, which Claude Code loads for that project, so nothing global is touched (section 11). Claude Code loads a mod there only in a trusted folder; in an untrusted one the agent layer covers it.

**Saving memories** (built 2026-10-16). Every context block, attached or not, also tells the agent what to save and how: what a later session on this work would need and could not get from the code, the git history or the ticket (decisions and why, approaches rejected and why, constraints found, open questions); one standalone, specific statement per memory, saved once settled, not as progress notes; never credentials, secrets, or personal or client data. It runs `remember "<statement>" --conversation <id> --author claude-code [--tag <topic>]...` and tells the person in one line what it saved, so the person sees what the store gets (product open questions 1 and 6). It passes no project or task: the memory shares the session's attachment (section 4). Tags are optional topics that cut across projects and tasks (`customer-support`, `auth`), never the project, the task or the kind of statement, and never a client or a person; the agent checks `ls tags` first and reuses an existing tag. The instruction is in the context block rather than a skill because a skill runs only when the model thinks of it, and this is the instruction that matters most. `remember` joins `attach` and `ls` in the allowed commands.

Both layers use the CLI in milestone 1. When `loci mcp` exists, the instruction and the skill call `loci_set_context` instead; nothing else changes. MCP elicitation and the `loci claude` wrapper stay possible pickers but are not planned: the mod pane covers the person's side.

Why not a pattern or a repository map in config: a repository does not identify a project, and a pattern bakes one team's naming into Loci while still guessing wrong whenever a branch is named differently. Why not ask the person every time: it adds a step to every session, and the agent can usually tell. Why not only a picker (pane or wrapper): people skip or forget it, and the agent can usually tell. Why not only the agent: some people prefer to set it before typing, and the pane serves them.

## 8. Automatic retrieval

**Parked (decided 2026-10-16).** Not every person wants memories in the agent's context on every session. How and when an agent reads Loci is the person's to say, in their own instructions (a repository's `CLAUDE.md`, for example "after attaching, run `loci memories --task <KEY>`"), using the on-demand commands of section 6. Revisit only if that proves not enough. The design below is kept for then.

As first designed, at session start, capture prints within a size cap:

1. Active memories for the effective project and task, most recent first, each with author, date and source conversation id.
2. Counts of conversations on the task and on the project, with the ids of the three most recent.
3. The instruction lines from section 7.

Configurable: on or off, the cap, and whether project-level memories are included when a task is set. On-demand retrieval is the tools in section 6 and needs no configuration.

## 9. Redaction, retention, sync

**Redaction.** Capturing only prompts and final answers already excludes tool results, which is where most secrets appear: environment dumps, file contents, command output. What remains is secrets pasted into a prompt or echoed in a reply. Patterns of the kind gitleaks uses are applied at write time and matches are replaced with `[REDACTED:<kind>]`. The original is never stored. The count of redactions is kept per message so the rules can be audited. Deferred on 2026-10-10 until before sync is switched on (milestone 4): until then captured messages are stored as typed, and rows written before redaction exists stay unredacted unless re-processed. The gate below already requires redaction before a second person joins.

**Retention and data protection.** Conversations may contain client data, so GDPR applies and the store must support erasure. `loci forget --conversation <id>` and `loci forget --before <date>` are hard deletes, and a `retention_days` setting runs the second one automatically. Run by a person, `forget` lists what it would delete and deletes only with `--force` (decided 2026-10-25); the retention run deletes without asking. A memory whose source conversation is forgotten keeps its content and loses its source link.

**Sync.** Off by default. `mode = "sync"` opens the same file with `NewTursoSyncDb` against a Turso Cloud database, with the auth token read from the environment and never written to config. Reads and writes stay local. `loci serve` owns sync: it pulls at start and when a session starts, and pushes shortly after writes (batched) and at shutdown. Conflicts resolve as last push wins, which is acceptable because rows are append-only with client-generated ids and the only updates are memory status changes, attachments, and a message's content when the same prompt's answer is captured again (which also clears its embedding). The remote must be reachable the first time a machine connects, to bootstrap.

Gates before sync is switched on for a second person, in this order: redaction verified against real captured sessions, retention configured, identity handles agreed (handles, not email addresses), and a position on Turso Cloud as a processor of conversation data that may contain client information.

## 10. Embeddings

Memories and messages are embedded so that search finds things phrased differently from the query, which full-text search does not. Everything searchable is stored as text first, so embeddings can always be recomputed from the source.

- **Where it runs.** Never in capture's critical path. The operation inserts the row with a null embedding. A worker goroutine inside `loci serve` embeds memories and messages with no embedding from the current model (null, or another model's), oldest first, 64 per batch, pending memories before messages so a backlog of captured messages never delays a memory just saved, back to back while a backlog remains and every 3 s otherwise. `loci embed` runs the same `Embed` operation to catch up now; A change of `embedding.model` therefore re-embeds everything with no extra step once the server restarts (config is read at start; `loci stop` restarts it). `loci embed --backfill` clears and re-embeds everything, for when the output changes under the same model name: new prefixes, or a newer pull of the same tag. One `Embed` runs at a time, so the worker and the CLI never embed the same rows twice.
- **When the provider is down.** `remember` is unaffected and rows stay pending. The worker backs off 5, 10, 20, 40, then every 60 s, and logs only when the state changes. Unreachable, timed out, and HTTP 503 (Ollama's "busy") all count as down; any other HTTP error, such as 404 for a model not pulled, is a real error. The error is `domain.ErrUnavailable`, which crosses the socket as kind `unavailable` (HTTP 503), so `loci embed` can print a hint. `loci status` shows the worker state, the model and the pending count, counted live rather than remembered from the last pass. The state describes the worker's last call to the provider: `idle` (none since the server started; the worker only calls when something is pending), `ok`, `unavailable` or `error`, and `off` when embedding is disabled. Memories with blank content are never pending, so they cannot block the queue. Provider output is checked before it is stored or used: an empty vector, vectors of different sizes in one batch, or a non-finite value fails the call, because one such vector stored under the current model would make every vector search for that model fail.
- **A row the provider rejects** (added 2026-10-18). Batches are oldest first and all or nothing, so one row the provider always rejects would block every row behind it. When a batch fails for a reason other than the provider being down, its rows are tried one at a time; those still rejected, and any whose vector size differs from the one most rows got (alone, each passes the size check), are set aside: `embedding_failed` records the model they failed under, and they are no longer pending for it. Only when at least one other row in the pass was embedded: if every row fails, the cause is the provider or its configuration, so nothing is set aside and the error is reported as before. A lone bad row therefore keeps the state at `error` until the next row arrives beside it. Set-aside rows stay searchable by text, and are tried again under another model, after `loci embed --backfill`, and, for a message, when its content changes. `loci status` counts them and `serve.log` lists their ids, never their content. Rejected: keeping the list in the server's memory, which forgets it on every restart and cannot report it.
- **Model.** The embedder is an interface (`internal/embed`); only `cli/serve.go` picks the implementation. The default provider is Ollama on localhost (`POST /api/embed`) with `nomic-embed-text`, 768 dimensions. Texts are prefixed `search_document: ` and queries `search_query: `, as the model card requires; both prefixes are configurable for other models. The model name is recorded on each row. Nothing leaves the machine. Measured on darwin/arm64 (2026-10-04): 0,57 s to load the model, 7 ms per query, 0,59 s per batch of 64. Each Ollama call has a 10 s timeout: `loci embed` may wait for the worker's batch and then make its own call, and both must fit in the 30 s timeout of the client it uses. Embedding a search query has its own 3 s limit; past it, search falls back to text ranking as if the provider were down. A caller that cancels, or whose own deadline passes, gets an error instead: falling back on its behalf would answer a request it has already abandoned.
- **Storage and query.** Vectors are written with `vector32()` into the `embedding` column and compared with `vector_distance_cos`, per Turso's vector search guide.
- **One vector per message, no chunking** (decided 2026-10-18). Measured on 240 Claude Code sessions' transcripts (about 5.000 prompts and final answers, lengths only): `nomic-embed-text` reads at most 2.048 tokens, roughly 8.000 characters, and the provider truncates past that. 99,8% of final answers fit (p99 5.900 characters). 1,5% of prompts do not, and those are pasted material whose opening says what it is. Subagent answers are the exception, 42% over (median 5.080, max 26.716 characters), but they are about 1,5% of messages and the main agent's next answer usually restates their finding. Milestone 3 requests truncation explicitly (`truncate: true`) rather than relying on the provider's default. Rejected for now: chunking messages over the limit, which needs a chunks table and rank merging for a few dozen messages. Revisit if search misses findings that only a subagent answer's tail holds.

## 11. Install and configuration

As first designed, `loci install` backs up `~/.claude/settings.json`, adds hook entries that run `loci capture claude-code` and the MCP server entry, writes `~/.claude/skills/loci/SKILL.md`, and creates `~/.loci/config.toml`. The hooks part was replaced by the per-repository `loci init` below; the config file stays as shown:

```toml
[user]
handle = "orestis"

[storage]
mode = "local"                     # or "sync"
path = "~/.loci/loci.db"
remote_url = ""                    # turso://... ; token from LOCI_TURSO_TOKEN

[retrieval]
auto = true
max_memories = 30

[retention]
days = 0                           # 0 keeps everything

[embedding]
provider = "ollama"                # or "off"
url = "http://localhost:11434"
model = "nomic-embed-text"
document_prefix = "search_document: "
query_prefix = "search_query: "
max_distance = 0.40                # 0 keeps every vector match
```

**Implemented so far: `loci init` instead of `loci install`** (decided 2026-10-11). The person did not want Loci to edit their global Claude Code settings, and needed one laptop's work for several companies (an employer's repositories and personal projects) kept apart: client data stays in its own database (purpose limitation). So capture is set up per repository, and nothing outside the repository changes.

- A **store** is one database, `<store>.db` in the directory of `storage.path` (`~/.loci/northwind.db`), with its own server like any database.
- `loci init <store>`, run anywhere inside a git repository, writes at the repository's root: `.loci.toml` naming the store (`store = "northwind"`), and the hook entries (section 5) in `.claude/settings.local.json`, Claude Code's per-person settings for the repository. Both paths are added to `.git/info/exclude`, which belongs to the clone and is never committed. Claude Code only ignores `settings.local.json` itself when it created the file, so Loci does this. Outside a git repository `init` refuses: Claude Code reads that file from the repository's root (when started in a subdirectory, from v2.1.211) and from no parent directory, so a file in `~/code/northwind` would not reach the repositories below it. Each repository, and each worktree, is set up once.
- The settings file is rewritten atomically, through a symlink if it is one, keeping its other keys in order and other hooks as they were (whitespace is re-indented). Entries from an earlier `init`, from any path, are replaced, so running it again is safe, for example after moving the binary. The hooks run the binary by absolute path, since Claude Code's `PATH` may not include it, with `--db` or `--store` only when given to `init`.
- `loci init --remove` takes the hook entries and `.loci.toml` out, and deletes the settings file if nothing else is left in it. The store is kept.
- **Capture is opt-in.** A repository without `init` is not captured, so forgetting one means "not captured", never "captured into the wrong store".
- **Database precedence**, for every command: `--db`, `--store`, `LOCI_DB`, the store of the nearest `.loci.toml` at or above the working directory (the event's `cwd` for capture), `storage.path`, default `~/.loci/loci.db`. So commands typed inside a repository use its store.
- **Only the store is per repository.** Everything else (`user.handle`, embeddings) stays in `~/.loci/config.toml`, read by every server. A per-repository handle would need the server to know its repository; added only if wanted. `init` does not create the config file: a missing one means defaults.
- Rejected alternatives: hooks in the global `~/.claude/settings.json` with `.loci.toml` files, which could cover several repositories with one parent-directory file (the person did not want global settings touched); one config per directory found by walking up (settings scattered, capture opt-out); a directory-to-store map in the user config (one more file to keep in sync when directories move).
- `init` also writes the `/loci` skill (section 7) to `.claude/skills/loci/SKILL.md`, and `permissions.allow` rules for `Bash(<loci> attach:*)` and `Bash(<loci> ls:*)` in the same settings file, so attaching costs no permission prompt (built 2026-10-16). Both use the command the hooks run, and the skill folder is added to `.git/info/exclude`. Loci's rules are recognised as the ones for the command its hooks run, so a rerun after moving the binary replaces them, and `--remove` takes out only those. `init` refuses to replace a `SKILL.md` it did not write (no `Written by loci init` marker), and `--remove` deletes only its own. It also writes the `loci-pick` mod (section 7) to `.claude/skills/loci-pick/` (built 2026-10-17): the mod's source embedded in the binary, with `hooks/loci.ts` naming the command the card runs, one argument each. That file carries the marker; `init` refuses a `loci-pick` folder without it, replaces its own folder whole, and `--remove` deletes only its own. The MCP entry is added when `loci mcp` exists.

**Configuration implemented so far:** `user.handle` (default: the OS user name, else `me`), `storage.path`, and `embedding.*` with the defaults shown above (any `provider` other than `ollama` or `off` is invalid, and `max_distance` must be between 0 and 2). Each setting is added to `internal/config` when the code that reads it is built. The database path is resolved as described under workspaces above. The config file is `~/.loci/config.toml` unless `LOCI_CONFIG` names another, and a missing file means defaults. The README's Configuration section covers this for users.

## 12. Milestones

1. **Capture works.** One person, local file. Every turn stored, `loci show` reads it back, two concurrent sessions do not interfere. Judge by hand whether what the agent saves is worth reading. This answers product open question 1 and decides whether to continue.
2. **The person attaches.** The mod's card above the prompt (section 7), so attachment does not depend on the agent guessing right. The agent instruction and `/loci` come earlier, in milestone 1. Built 2026-10-17.
3. **Conversations are searchable by meaning.** Message embeddings, and hybrid search on messages as on memories, with the embedding worker running. Built 2026-10-18.
4. **It becomes shared.** Sync to Turso Cloud, redaction verified, retention on. A second person joins only here.

Reordered 2026-10-16: the mod moved ahead of message embeddings, and session-start retrieval (formerly milestone 2) is parked (section 8).

**Queued** (added 2026-10-25, from a review of Delta, Zed's agent workspace at delta.dev). In priority order:

| # | Item | When | Why there |
|---|---|---|---|
| 1 | **Commit provenance.** Capture records the commit `HEAD` points at with each message, read from `.git` files like repo and branch (section 5), never by running `git`. `loci conversations --commit <sha>` finds the sessions that worked on a commit, so a reviewer can go from a change to the reasoning behind it | Next, before milestone 4 | A capture column is cheapest to add while the store is local and has one user. Useful from the first day of dogfooding. Open point 11 |
| 2 | **`forget` previews by default.** It lists what it would delete and deletes only with `--force` (section 9) | Built with `forget`, milestone 4 | It is the one hard delete, and a mistyped date or id cannot be undone. Decided now because it costs nothing |
| 3 | **Data storage page.** What Loci stores, where, for how long, what is redacted and how it is erased, including what sync sends to Turso Cloud | Before milestone 4 ends; a gate for the second person (section 9) | Answers product open question 3 in a form a person can check, and backs the processor position the gates require |
| 4 | **`remember --from-message <id>`.** Promote a captured message to a memory, keeping the message as its source (`memories.message_id`, in the schema but not yet written) | After dogfooding shows the need | Depends on product open questions 1 and 6: whether people want to keep a specific answer or the agent's saved statements already cover it |

## 13. Open points carried into the build

| # | Question | Matters at |
|---|---|---|
| 1 | ~~Does `SubagentStart` carry the prompt the main agent gave the subagent~~ Resolved 2026-10-10: no, the hooks reference lists only `agent_id` and `agent_type`. A subagent conversation holds the final answer only | Milestone 1 |
| 2 | Does the MCP server process receive the session id. If so, the `conversation_id` argument becomes optional | Milestone 1 |
| 3 | Turso FTS behaviour and query syntax through the Go binding: tokenisation, prefix matching, ranking. And whether FTS and vector functions are in the current `tursogo` release rather than only in the CLI. Checked 2026-10-04: FTS is behind `--experimental-index-method` in tursogo v0.8.1; vector functions (`vector32(?)`, `vector_distance_cos`, `vector_extract`) work through the driver and are in use. Until FTS is usable the text side of ranking is a substring match (section 6) | Milestone 1 |
| 4 | ~~Vector linear scan cost on a project with tens of thousands of messages~~ Resolved 2026-10-18 (`BenchmarkConversationSearch`, Apple M5): with 50.000 messages of 768 dimensions in 1.000 sessions, the vector side takes 144 ms and the substring side 81 ms, so a hybrid query about 0,23 s. Each message is scored once and grouped per conversation with a window function; a correlated subquery per conversation was quadratic (1,05 s at 5.000 messages). The query vector is bound as a blob, because `vector32(?)` inside a subquery is parsed again per row (6x slower). Known limit: project and task filters do not cut the scan, which scores every embedded message before the filter applies; push the filter into the scan, or use a vector index, when a store outgrows this | Milestone 3 |
| 5 | ~~Embedding chunk size for long messages~~ Resolved 2026-10-18: no chunking, one truncated vector per message (section 10) | Milestone 3 |
| 6 | ~~Memory kind (decided, rejected, open, from the superseded spec)~~ Resolved 2026-10-03 as tags, revised 2026-10-16: the kind is not stored. The statement carries it ("chose X because Y"). Tags are topics that cut across projects and tasks (`customer-support`), so one can search across the work hierarchy | Milestone 1 |
| 7 | Whether Turso Sync can target anything other than Turso Cloud. Only matters if the remote must move to our own infrastructure, in which case `go-libsql` plus `libsql-server` is the fallback | Milestone 4 |
| 8 | ~~`multiprocess_wal` stability~~ Resolved 2026-10-01: not used. A single server owns the database (section 3) | Milestone 1 |
| 9 | ~~Capture writes when `loci serve` is unreachable~~ Resolved 2026-10-10: accept the loss and log it with the session id; build a spool only if the log shows losses (section 5) | Milestone 1 |
| 10 | Whether `loci serve` should exit after a long idle period or run until stopped. Currently it runs until stopped. Less pressing with embeddings: `loci serve` holds only an HTTP client, and the model's memory lives in Ollama, which unloads it when idle | Milestone 3 |
| 11 | Commit provenance (section 12, queued item 1): a turn can make several commits, and the commit recorded with the answer is only the last one. Finding the earlier ones means walking ancestry between the commit at the prompt and the one at the answer, which takes reading git objects (packed, compressed) or running `git` in the CLI process at query time, never on the capture path. Also whether the commit goes on messages only or on the conversation as well | Commit provenance |
