-- Capture keys a conversation by harness, session and, for a subagent run,
-- the agent id (design.md section 5). Empty agent_id is the session's main
-- conversation; a non-null default keeps the unique index a plain one.
--
-- machine, cwd and ended_at are dropped: nothing reads them, and machine and
-- cwd hold personal data (host and user names) for no purpose.

ALTER TABLE conversations ADD COLUMN agent_id TEXT NOT NULL DEFAULT '';

ALTER TABLE conversations DROP COLUMN machine;

ALTER TABLE conversations DROP COLUMN cwd;

ALTER TABLE conversations DROP COLUMN ended_at;

CREATE UNIQUE INDEX conversations_key ON conversations (harness, session_id, agent_id);

CREATE INDEX conversations_parent ON conversations (parent_conversation_id);

CREATE INDEX messages_prompt ON messages (conversation_id, prompt_id);
