# Loci: Architecture

How the processes and packages fit together. For the reasoning behind these choices, see [design.md](design.md); for the rules code must follow, see [CLAUDE.md](../CLAUDE.md).

## One binary, two roles

There is one binary, `loci`. Every time it runs, it plays one of two roles:

| Role | Started by | Lives for | Opens the database? |
|---|---|---|---|
| **Client**: `loci remember`, `loci memories`, `loci embed`, `loci task`, `loci ls`, `loci capture`, `loci status`, `loci stop` (later also `loci mcp`). `loci init` touches no database | You, a script, or Claude Code | One command (`loci mcp` will live for a whole session) | Never |
| **Server**: `loci serve` | A client, automatically, when none is running | Until `loci stop`, or until a newer build replaces it | Yes, and it is the only process that does |

The "daemon", the "HTTP server" and `loci serve` are the same process, seen from different angles:

- **`loci serve`** is the command that runs it.
- **The daemon** (`internal/daemon`) is its lifecycle: take the lock, open the database, create the socket, shut down cleanly. "Daemon" because it runs in the background, detached from whoever started it.
- **The HTTP server** (`internal/rpc`) is how it talks to clients. HTTP is only the request format. Nothing listens on the network: requests travel over a Unix socket, a file at `~/.loci/loci.db.sock` that only processes on this machine, run by you, can connect to.

## Why a server at all

Turso, the database, locks its file to one process. The CLI, capture and the MCP server are separate processes, often several at once when several Claude sessions run. They cannot all open the database, so exactly one process does, and the rest send it requests. See [design.md section 3](design.md) for the alternatives considered.

## The picture

```
  YOU / SCRIPTS               CLAUDE CODE
       |                       |              |
   loci remember ...      loci capture    loci mcp (later)
   loci memories ...      claude-code     (whole session)
   loci task / ls ...     (every turn)
       |                       |              |
       +-----------+-----------+--------------+
                   |
                   |  CLIENT side: parse arguments, send one request, print the answer
                   |  internal/cli -> app.Service -> rpc.Client
                   |
                   |  HTTP over the Unix socket ~/.loci/loci.db.sock
                   v
   +-------------------------------------------------------------+
   |  loci serve  (one per database, runs in the background)     |
   |                                                             |
   |  rpc handler    turns each request into an operation call   |
   |       |                                                     |
   |  app            the operations: all business logic          |
   |       |                                                     |
   |  store          all SQL                                     |
   |       |                                                     |
   |  embedding worker  goroutine: fills missing embeddings  ----+--> Ollama
   |       |            (app.Embed, every few seconds)           |    localhost:11434
   +-------|-----------------------------------------------------+    (optional)
           v
      ~/.loci/loci.db      (later: synced to Turso Cloud by the server)
```

## Following one command

What happens when you run `loci task epd-1 --project northwind`:

1. **`internal/cli`** parses the arguments into `app.SetTaskProjectInput{Task: "epd-1", Project: "northwind"}`.
2. It asks for an `app.Service` (the list of operations). Outside tests that is an **`rpc.Client`**, which sends `POST /op/set_task_project` with the input as JSON over the socket.
3. **If no server is running**, the connection fails. The client starts `loci serve` in the background (`internal/daemon`), waits for the socket to appear, usually 10 to 20ms, and sends the request again.
4. In the server, the **`rpc` handler** decodes the JSON and calls `app.SetTaskProject`.
5. **`internal/app`** normalises the input (`EPD-1`, `northwind`), creates the task and project if they are new, and links them, using **`internal/store`** inside one transaction.
6. The result travels back the same way as JSON. The CLI prints `EPD-1  northwind`, or the JSON itself with `--json`.

Every operation follows exactly this path. Only steps 1 and 5 differ from one operation to the next.

## Who uses what

| Package | Runs in | Job |
|---|---|---|
| `cmd/loci` | both | Entry point: hands the arguments to `internal/cli` |
| `internal/cli` | both | Commands. `serve` runs the server; every other command is a client |
| `internal/rpc` | both | `Client` on the client side, the handler on the server side |
| `internal/daemon` | both | Server side: running `loci serve`. Client side: starting it when it is missing |
| `internal/app` | server | Operations. The only home of business logic |
| `internal/store` | server | The database |
| `internal/embed` | server | Embedding providers: the `Embedder` interface, the Ollama client, a fake for tests |
| `internal/capture` | client | Translates Claude Code hook events into capture operations; reads repo and branch from `.git`; edits a repository's Claude Code settings for `loci init` |
| `internal/render` | client | Markdown for people, such as `loci show`. Formats operation output and nothing else |
| `internal/domain` | both | Shared types and rules |

`app.Service` is the seam. Commands only know "something that has these operations". In production that is `rpc.Client`, which forwards each call to the server. In tests it is an in-process `*app.App`, so command tests need no server.

## Lifecycle

| Event | What happens |
|---|---|
| First command after boot | The client starts `loci serve` in the background. You never need to start it yourself |
| Every later command | Connects to the running server, about 7ms per call |
| You rebuild `loci` | The next command sees a different build version. The old server shuts down and the new binary starts its own, so you never talk to stale code |
| Two servers start at the same moment | A lock file (`loci.db.lock`) lets one win. The other exits cleanly |
| `loci status` | Shows the running server's PID, version, database, embedding state and log file |
| `loci stop` | Stops it. The next command starts a new one |

Files next to the database:

| File | Purpose |
|---|---|
| `loci.db`, `loci.db-wal` | The database |
| `loci.db.sock` | The socket clients connect to. Exists only while the server runs |
| `loci.db.lock` | Guarantees one server per database |
| `log/serve.log` | The server's log. Look here when something goes wrong |
| `log/capture.log` | What `loci capture claude-code` could not store. It never reports errors to Claude Code, so they land here. Empty when all is well |
