-- Initial schema, design.md section 4.
-- Timestamps are RFC 3339 UTC text. IDs are client-generated ULIDs.
-- Full-text indexes are added in a later migration (design.md section 13, open point 3).

CREATE TABLE projects (
    id         TEXT PRIMARY KEY,
    slug       TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);

CREATE TABLE tasks (
    id         TEXT PRIMARY KEY,
    key        TEXT NOT NULL UNIQUE,
    project_id TEXT REFERENCES projects (id),
    created_at TEXT NOT NULL
);

CREATE INDEX tasks_project_id ON tasks (project_id);

CREATE TABLE tags (
    id   TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE actors (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('human', 'agent')),
    handle     TEXT NOT NULL UNIQUE,
    agent_type TEXT
);

CREATE TABLE conversations (
    id                     TEXT PRIMARY KEY,
    harness                TEXT NOT NULL,
    session_id             TEXT NOT NULL,
    parent_conversation_id TEXT REFERENCES conversations (id),
    agent_type             TEXT,
    human_actor_id         TEXT REFERENCES actors (id),
    machine                TEXT,
    repo                   TEXT,
    cwd                    TEXT,
    branch                 TEXT,
    project_id             TEXT REFERENCES projects (id),
    task_id                TEXT REFERENCES tasks (id),
    started_at             TEXT NOT NULL,
    ended_at               TEXT
);

CREATE INDEX conversations_session_id ON conversations (session_id);
CREATE INDEX conversations_project_id ON conversations (project_id);
CREATE INDEX conversations_task_id ON conversations (task_id);

CREATE TABLE messages (
    id                 TEXT PRIMARY KEY,
    conversation_id    TEXT NOT NULL REFERENCES conversations (id),
    seq                INTEGER NOT NULL,
    prompt_id          TEXT,
    role               TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    speaker_actor_id   TEXT REFERENCES actors (id),
    addressee_actor_id TEXT REFERENCES actors (id),
    content            TEXT NOT NULL,
    redactions         INTEGER NOT NULL DEFAULT 0,
    embedding          BLOB,
    embedding_model    TEXT,
    created_at         TEXT NOT NULL,
    UNIQUE (conversation_id, seq)
);

CREATE TABLE memories (
    id              TEXT PRIMARY KEY,
    content         TEXT NOT NULL,
    project_id      TEXT REFERENCES projects (id),
    task_id         TEXT REFERENCES tasks (id),
    conversation_id TEXT REFERENCES conversations (id),
    message_id      TEXT REFERENCES messages (id),
    author_actor_id TEXT REFERENCES actors (id),
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    embedding       BLOB,
    embedding_model TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE INDEX memories_project_id ON memories (project_id);
CREATE INDEX memories_task_id ON memories (task_id);

CREATE TABLE item_tags (
    item_kind TEXT NOT NULL CHECK (item_kind IN ('conversation', 'memory')),
    item_id   TEXT NOT NULL,
    tag_id    TEXT NOT NULL REFERENCES tags (id),
    PRIMARY KEY (item_kind, item_id, tag_id)
);

CREATE INDEX item_tags_tag_id ON item_tags (tag_id);
