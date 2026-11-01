# Loci: Product Definition

Name: from the method of loci, where knowledge is stored in places and recovered by returning to them. Provisional. Note that there are existing companies called Loci in developer tooling, which matters only if this is ever published.

Status: definition, no build started
Date: 2026-09-22
Supersedes: `episodic-context-tool-spec.md` (2026-09-18)
Background: `agent-session-context-opportunity.md` covers the market scan.

Scope of this document: what the product is. No design or implementation decisions.

---

## 1. What it is

A store for everything an agent session produces, organised by the work it belongs to rather than by the session or the person, with tools that let an agent put things in and get things out.

Two kinds of thing go in. The full conversation, automatically. And memories, which are the statements an agent judged worth keeping. Both are found the same way: by the work they are attached to.

## 2. Who it is for

Software teams where a meaningful share of engineering thinking now happens in conversation with an agent, and where none of it survives the session.

The first users are one small engineering team. The tool assumes nothing about how a team works beyond having some way to name a piece of work.

## 3. The problem

Conversations with agents are per-session, per-person and disposable. When a session ends or compacts, the reasoning goes with it.

The consequences, in order of how often they bite:

1. You return to the same ticket days later and your agent knows nothing about the previous three sessions on it.
2. Someone else picks up the ticket and their agent re-derives, or contradicts, what was already settled.
3. Months later nobody can answer why it was built this way, because the answer was in a conversation nobody kept.

## 4. What it stores

| | Conversation | Memory |
|---|---|---|
| Content | Every message, verbatim | A statement worth keeping |
| Written | Automatically, all of it | Deliberately, by the agent |
| Shape | Ordered record of what was said | Standalone claim |
| Volume | High | Low |
| Used for | Finding what actually happened | Getting oriented without reading a transcript |

Keeping the raw conversation alongside the memories matters for two reasons. Memories lose the specifics that turn out to be the point later: the exact limit that made an approach unworkable, the document someone checked. And it makes saving non-destructive, so a poor early memory can be corrected against the source rather than being the only surviving trace.

## 5. How things are organised

A conversation or a memory can be attached to a project, a task, and any number of tags. Any of them, all of them, or none.

| | Project | Task | Tag |
|---|---|---|---|
| What it is | A body of work | A single piece of work | Any label |
| How many per item | One | One | Many |
| Relationship | May contain tasks | May belong to a project | Independent of both |
| Example | `settlements` | `EPD-100` | `auth`, `perf`, `client-onboarding` |

**A task may belong to a project, and asking for a task's context also returns its project's.** This is why projects and tasks are first-class rather than two more tags: the caller does not need to know the structure in order to benefit from it, and an agent starting cold never does.

That relationship is resolved when context is retrieved, not recorded when it is stored. A task's project can be set, changed or removed at any time and everything already attached to it follows automatically.

An item can also be linked to a project directly. Where a direct link and the project inherited through the task disagree, which one applies is unresolved. See open question 7.

Nothing is required. A conversation with no project, no task and no tags is valid and still stored and still searchable.

Tags are the escape valve. Anything that does not fit two levels, an epic, an initiative, an incident, a spike, becomes a tag rather than a change to the model.

## 6. What it does

**Stores every message.** Nothing is lost to compaction or to the end of a session.

**Lets an agent save a memory.** The agent decides what is worth keeping and what it belongs to.

**Finds both.** By project, by task, by tag, by search, or any combination. Retrieve the memories for a ticket. Search the conversations on a project. Pull the full transcript behind a memory.

**Lets relationships be set and changed after the fact.** A conversation can be attached to a task later. A task can be moved to a different project. Nothing has to be right at the moment of writing.

**Supports correction.** A memory that is wrong or outdated can be marked inactive rather than silently deleted, and the right statement saved in its place, because a memory nobody can fix is worse than no memory.

**Is readable by people.** Someone who does not use agents can open what is stored for a ticket and understand it. The tools are not the only way in.

## 7. Interface

The product is a set of building blocks. Every operation is available two ways:

- **MCP**, so an agent can use them mid-session.
- **CLI**, so a person or a script can use them without an agent.

Neither is primary. Anything an agent can do, a person can do.

The operations:

| | |
|---|---|
| Store a memory | With optional project, task and tags |
| Search memories | By project, task, tag, text, or any combination |
| Search conversations | Same |
| Retrieve a conversation | In full, or the messages around a match |
| Create or reference a project or task | Lazily, on first mention. No setup step |
| Set a task's project | Or change it, or remove it |
| Attach or re-attach an item | Change the project, task or tags of something already stored |
| Mark a memory inactive | When it is wrong or outdated, without deleting it |

## 8. Retrieval modes

Both are in scope, and the product supports either without favouring one:

- **On demand.** The agent calls a tool when it decides it needs something.
- **Automatic.** The context for the current work is surfaced at the start of a session without anyone asking.

The second matters because agents frequently do not think to ask. Making it possible is a product requirement; when and whether it is switched on is configuration.

## 9. Not in scope

| Excluded | Reason |
|---|---|
| Dashboards, cost tracking, usage analytics | Other tools do this well and it is a different product |
| Synthesising knowledge across projects | Different problem, and the thing every knowledge base gets wrong |
| Mirroring an issue tracker | A project and a task are identifiers. The product holds no names, statuses or descriptions, and syncs with nothing |
| More than two levels of structure | Tags absorb anything that does not fit. Revisit only if they demonstrably cannot |
| Capturing conversations held outside an agent tool | Not technically possible. Planning done in a chat assistant cannot be captured, and nothing in the market solves this |

## 10. Open questions

1. What does an agent actually choose to save, and is it worth having? Only answerable by running it.
2. Does retrieval stay useful as the store grows from hundreds of messages to millions? Project and task bound the search, but relevance within them is unsolved here.
3. Stored conversations contain credentials, environment values and client data. What is kept, for how long, and what is stripped, is unresolved and becomes urgent the moment anything is shared.
4. Who sets a task's project, and when. **Deliberately deferred.** The tool exposes the operation; the policy for calling it gets decided once the building blocks exist and it is clear what is convenient.
5. Same for how a session learns which task it is on. The operation exists; the wiring is a later decision.
6. Does an agent that can save memories at will produce a useful store or just a large one?
7. When an item's direct project link and the project inherited through its task disagree, which applies. Current lean is that inheritance through the task wins where the task has a project. Not settled.
