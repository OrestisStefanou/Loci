# Loci

Loci keeps what your agent sessions produce, the full conversations plus the memories an agent chose to save, and organises it by the work it belongs to (project, task, tags) rather than by session or person. One local binary, `loci`, used by you from the terminal and by Claude Code through hooks and a skill it installs per repository.

## Why

Conversations with agents are per session, per person and disposable. When a session ends or compacts, the reasoning goes with it. In order of how often that bites:

1. You return to a ticket days later and your agent knows nothing about the previous three sessions on it.
2. Someone else picks up the ticket and their agent re-derives, or contradicts, what was already settled.
3. Months later nobody can answer why it was built this way, because the answer was in a conversation nobody kept.

Loci keeps two things so that does not happen:

| | Conversation | Memory |
|---|---|---|
| What | Every prompt and final answer, verbatim | One statement worth keeping, such as a decision and its reason |
| Written | Automatically, on every turn | Deliberately, by the agent (or by you) |
| Used for | Finding what actually happened | Getting oriented without reading a transcript |

Both are attached to the work they belong to: a **project** (a body of work, such as `fees-v2`), a **task** (one piece of work, such as `EPD-100`, optionally inside a project) and any number of **tags** (topics that cut across projects, such as `customer-support`). Asking for a project includes its tasks. Nothing has to be right at the moment of writing: a session, a memory or a task can be re-attached later and everything follows.

Everything stays on your machine: one database file per store, and semantic search runs on a local model through Ollama.

## Status

Milestones 1 to 3 of [docs/design.md](docs/design.md) section 12 are built:

- Capture of Claude Code sessions, per repository.
- Attaching a session to its project and task, by you (a card above the prompt) or by the agent.
- Memories saved by the agent while it works, with the conversation they came from.
- Search over memories and conversations by project, task, tag, text and meaning.

Not built yet: sharing with a team (sync, redaction, retention), the MCP server, and agents reading Loci automatically at the start of a session. Until then an agent reads Loci through the `loci` CLI; you can tell it when to, for example in your repository's `CLAUDE.md`.

## Setup

### 1. Requirements

- macOS or Linux (Loci uses Unix sockets).
- Go 1.21 or later to build. The repository pins Go 1.27, which `go` downloads automatically on the first build.
- [Claude Code](https://claude.com/claude-code), for capture.
- Optional, for search by meaning: [Ollama](https://ollama.com). Without it, `--query` is a substring match.

### 2. Build and install

```sh
git clone https://github.com/OrestisStefanou/Loci.git && cd Loci
make build                      # writes bin/loci, a static binary
cp bin/loci /usr/local/bin/     # or anywhere on your PATH, e.g. ~/.local/bin
```

After pulling changes, run `make build` and copy the binary again. The next command notices the new build and replaces the running server, so you never talk to stale code. If you moved the binary, run `loci init` again in each captured repository (step 4), because the hooks call it by absolute path.

### 3. Search by meaning (optional)

Install Ollama (on macOS the app from ollama.com, or `brew install ollama && brew services start ollama`), then fetch the embedding model once:

```sh
ollama pull nomic-embed-text    # about 270 MB
```

Nothing else to configure: the defaults point at Ollama on `localhost:11434`. Details in [Search by meaning](#search-by-meaning).

### 4. Capture a repository's Claude Code sessions

Capture is set up per repository, and each repository names its **store**: one database, such as one per employer or client, so their work never mixes. Your global Claude Code settings are never touched, and repositories without `loci init` are not captured at all.

```sh
cd ~/code/northwind/web && loci init northwind       # -> ~/.loci/northwind.db
cd ~/code/northwind/portal && loci init northwind    # same store
cd ~/code/side-project && loci init personal         # -> ~/.loci/personal.db
```

`loci init <store>`, run anywhere in a git repository, writes at its root:

| File | What it does |
|---|---|
| `.loci.toml` | Names the repository's store |
| `.claude/settings.local.json` | Hooks that run `loci capture claude-code` on every turn, and rules letting the agent run `loci attach`, `loci ls` and `loci remember` without asking. Anything already in the file is kept |
| `.claude/skills/loci/` | The `/loci` skill, with which the agent attaches the session |
| `.claude/skills/loci-pick/` | The `loci-pick` card above the prompt, with which you attach it |

All of them are added to `.git/info/exclude`, so they never show up in `git status` or get committed. Run it once per repository and once per worktree. `loci init --remove` stops capturing the repository and keeps the store.

Start a new Claude Code session afterwards. The card only shows in a folder you have trusted in Claude Code.

### 5. Check it works

```sh
loci status
```

```
running    pid 19599  version ...  since ...
db         /Users/you/.loci/loci.db
embedding  ok  nomic-embed-text  0 pending
log        /Users/you/.loci/log/serve.log
```

There is no setup step for the database or the server: the first command creates the database and starts `loci serve` in the background. `loci status` reports on the database of the directory you run it in, so run it inside a captured repository to see that repository's store.

| `embedding` shows | Meaning |
|---|---|
| `ok ... 0 pending` | Search by meaning is working |
| `..., N failed` | N rows the embedding model kept rejecting were set aside so the rest are not held up. They are still found by text search, and are tried again after a model change or `loci embed --backfill`. `serve.log` lists their ids |
| `idle` | Nothing embedded since the server started; save a memory or capture a turn and check again |
| `unavailable` | Ollama is not running. Memories and messages are still saved and are embedded once it is back |
| `error` | Ollama answered with an error, shown on the next line; usually the model is not pulled |
| `off` | `embedding.provider = "off"` in the config |

`loci stop` stops the server; the next command starts it again.

## Using it

### During a Claude Code session

**Attach the session.** A new session starts with a card above the prompt listing your tasks, most recently used first. Press `1` to `5` in the empty prompt to attach the session to one, or press ctrl+x tab (or click the card) to search, create a new task, or create a new project; Esc goes back to the prompt. `/loci-pick` opens the card again later, and the agent is told what you picked.

If you just start typing instead, the agent attaches the session itself: with your first prompt it works out the task (from the branch, the worktree name or the prompt), and its reply starts with a line such as `Loci: project fees-v2, task EPD-1271. Say /loci to change.` When it cannot tell, it leaves the session unattached and says so. Type `/loci` (for example `/loci EPD-12`, or "/loci tag it auth") to have the agent change the project, task or tags.

**Memories.** While it works, the agent saves memories: decisions and why, approaches rejected and why, constraints and open questions that a later session would need and could not get from the code. It tells you in one line each time. A memory records the conversation it came from and follows that session's attachment, including one set or changed later.

**What is stored:** your prompts and the agent's final answers. Tool calls, tool output and the agent's intermediate steps are not. Capture never slows or fails a session: it gives up after 3 seconds and logs what it could not store.

### From the terminal

Commands run inside a captured repository use its store; elsewhere they use the default database (`~/.loci/loci.db`), or name a store with `--store northwind`. Every command takes `--json` for machine-readable output.

```sh
# Find what was decided
loci memories --task EPD-100
loci memories --project fees-v2 --query "when do payments stop"
loci memories --tag customer-support

# Find and read conversations
loci conversations --task EPD-100                  # sessions, most recent first, with their first prompt
loci conversations --query "cut-off"               # sessions whose messages mention it or mean it, with the matching message
loci show <conversation-id>                        # the whole conversation as Markdown
loci show <conversation-id> --around <message-id>  # just the messages around a search match

# Organise
loci attach <id> --project fees-v2 --task EPD-101  # a memory or a conversation
loci attach <id> --tag payments --tag auth          # replaces the item's tags
loci task EPD-101 --project fees-v2                # move a task, and everything on it, to a project
loci ls tasks                                      # also: ls projects, ls tags

# Save and correct memories yourself
loci remember "Cut-off is 16:00 CET" --task EPD-100 --tag payments
loci mark <id> --inactive                          # hide a memory that is wrong or outdated
loci memories --task EPD-100 --all                 # include inactive ones
```

| Command | Does |
|---|---|
| `memories` | List or search memories by project, task, tags and text |
| `conversations` | List or search captured conversations the same way |
| `show` | Print one conversation as Markdown |
| `remember` | Save a memory |
| `mark` | Hide a wrong or outdated memory (`--inactive`), or restore it (`--active`). Nothing is deleted |
| `attach` | Change the project, task or tags of a memory or conversation |
| `task` | Set, change or remove a task's project |
| `ls` | List or search projects, tasks or tags |
| `init` | Capture a repository's Claude Code sessions into a store |
| `status`, `stop` | Inspect or stop the background server |
| `embed` | Embed what is waiting now, instead of waiting for the background worker |

`loci <command> --help` has the details and examples. Ids are the first column of `loci memories` and `loci conversations`.

To try commands without touching your real data, add `--db /tmp/loci-try/l.db` to each one.

## Configuration

Configuration is optional.

### Database path

The first of these that is set wins:

| # | Source | Example | Applies to |
|---|---|---|---|
| 1 | `--db` flag | `loci --db ~/work/loci.db ls tasks` | That one command |
| 2 | `--store` flag | `loci --store northwind ls tasks` | That one command: `<store>.db` next to the default database |
| 3 | `LOCI_DB` environment variable | `export LOCI_DB=~/work/loci.db` | Every command in that environment |
| 4 | The repository's store | `.loci.toml` written by `loci init northwind` | Commands run in that repository, and its Claude Code sessions |
| 5 | `storage.path` in the config file | see below | Everything that reads the config |
| 6 | Default | `~/.loci/loci.db` | |

The config file is `~/.loci/config.toml`, or the file `LOCI_CONFIG` points to. A missing file means defaults.

```toml
[user]
handle = "ada"            # who you are in the store; default: your OS user name

[storage]
path = "~/work/loci.db"   # ~ is expanded
```

Things to know:

- **Each database path gets its own server and its own data.** The socket, lock file and log are derived from the path (`<db>.sock`, `<db>.lock`, `log/serve.log` next to it). Two different paths are two separate stores, so check you are not writing to one and reading from another. `loci status` shows which database the server is using.
- **Relative paths are made absolute**, so `--db ./x.db` names the same database whichever command started its server.
- **Keep the path short.** macOS limits socket paths to 103 bytes, and the socket is the database path plus `.sock`. A path that is too long gets an error asking for a shorter one.
- **Capture always goes to the repository's store** (unless `loci init` was run with `--db` or `--store`, which are then written into the hooks). Claude Code runs capture, not you, so `LOCI_DB` in your shell does not reach it. Stores live next to `storage.path`, so moving that moves every store with it.

### Other settings

| Setting | Read by | Meaning |
|---|---|---|
| `user.handle` | `loci serve` | Recorded as the author of memories you save (`loci remember`) and as the human participant in captured conversations. Lowercase letters, digits, `.`, `_`, `-`. Default: your OS user name, or `me` if that is not a valid handle |
| `storage.path` | every command | The database, see above |
| `embedding.provider` | `loci serve` | `ollama` (default) or `off`. Off means `--query` is a substring match only |
| `embedding.url` | `loci serve` | The Ollama server. Default `http://localhost:11434` |
| `embedding.model` | `loci serve` | Default `nomic-embed-text`. After changing it, run `loci stop`: the next command starts a server with the new model, which re-embeds every memory and message in the background |
| `embedding.max_distance` | `loci serve` | Default `0.40`. Memories and conversations farther than this cosine distance from the query (a conversation by its nearest message) are not returned as meaning matches (substring matches always are). Lower is stricter; `0` turns the cut-off off. The right value depends on the model |
| `embedding.document_prefix`, `embedding.query_prefix` | `loci serve` | Prepended to stored texts and to queries. Defaults `search_document: ` and `search_query: `, which `nomic-embed-text` requires; clear them for a model that does not use prefixes. After changing them, run `loci stop` and then `loci embed --backfill` |

[design.md section 11](docs/design.md) lists the full planned config file; each setting is wired in when the feature that needs it is built.

### Search by meaning

`loci memories --query "when do payments stop"` finds "Settlement cut-off is 16:00 CET": substring matches are ranked together with memories close in meaning. `loci conversations --query` does the same over captured messages and shows, for each session, the message that matched; read it in context with `loci show <conversation> --around <message>`. Meaning comes from a local embedding model run by [Ollama](https://ollama.com); nothing leaves the machine.

`loci serve` embeds new memories and captured messages in the background within a few seconds, memories first. `loci status` shows the state and how many are waiting, and `loci embed` catches up now. Changing `embedding.model` needs no manual step beyond `loci stop`, because rows embedded by another model count as pending. `loci embed --backfill` embeds everything again when the model name stays the same but its output changes: after editing the prefixes, or pulling a new version of the same model.

If Ollama is not running, nothing breaks: memories and messages are saved and embedded later, and `--query` falls back to substring matches with a note on stderr. To turn it off entirely, set `provider = "off"` under `[embedding]`.

## Your data

- **Where it is.** Each store is one file, `~/.loci/<store>.db` (the default one is `~/.loci/loci.db`), plus logs in `~/.loci/log/`. Nothing is sent anywhere: the server listens on a local Unix socket, and Ollama runs on your machine.
- **What is in it.** Prompts and final answers from captured repositories, memories, and the project, task and tag names you use. The capture log holds ids, never content.
- **Keeping work apart.** Use a separate store per employer or client (`loci init <store>`), so one store never holds another's data.
- **Stopping and removing.** `loci init --remove` stops capturing a repository. To delete a store, run `loci stop` with that store (for example `loci --store northwind stop`) and delete its `.db` file. A command to erase single items (`forget`) is planned.

## Troubleshooting

| Symptom | Check |
|---|---|
| Nothing is captured | Run `loci status` inside the repository: is it the store you expect? Was the Claude Code session started after `loci init`? `log/capture.log` next to the store's database lists turns that could not be stored, and should stay empty |
| Capture stopped after moving or reinstalling `loci` | Run `loci init <store>` again in the repository; the hooks call the binary by absolute path |
| The card above the prompt does not show | The folder must be trusted in Claude Code, and the session unattached. `/loci-pick` opens it on demand |
| The agent asks permission to run `loci` | Run `loci init` again in the repository to refresh the permission rules, then start a new session |
| `--query` finds only exact text | `loci status` shows the embedding state; see [Search by meaning](#search-by-meaning) |
| "socket path ... is longer than 103 bytes" | Use a shorter database path (macOS limit) |
| You see someone else's data, or none | Each database path is a separate store. `loci status` shows which one you are using |

The server's own log is `log/serve.log` next to the database.

## Architecture

One binary, `loci`, in two roles: short-lived clients (every command, and the capture hooks) and one background server, `loci serve`, that owns the database. Commands start the server automatically when it is not running.

- What the product is: [docs/product.md](docs/product.md)
- How the pieces fit together, and the path a command takes through the packages: [docs/architecture.md](docs/architecture.md)
- How it is built, and the decisions behind it: [docs/design.md](docs/design.md)

## Develop

```sh
make check       # format check, vet, lint, tests with -race
make mod-check   # validate and test the loci-pick card; needs the claude binary
```

Lint needs `golangci-lint` (`brew install golangci-lint`). `make check` skips it with a notice when it is missing, but CI always runs it. How to work in this repository, for people and agents: [CLAUDE.md](CLAUDE.md).
