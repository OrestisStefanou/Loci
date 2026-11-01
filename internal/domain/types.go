// Package domain holds Loci's core types and rules. It does no I/O and
// imports no other internal package.
package domain

import "time"

// Project is a body of work, identified only by its slug. No other metadata
// (product.md section 9).
type Project struct {
	ID        ID        `json:"id"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}

// Task is a single piece of work, identified only by its key. It may belong
// to a project; that link is resolved at read time (design.md section 4).
type Task struct {
	ID          ID        `json:"id"`
	Key         string    `json:"key"`
	ProjectSlug string    `json:"project,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Tag is any label.
type Tag struct {
	ID   ID     `json:"id"`
	Name string `json:"name"`
}

// Kind names the listable entity kinds for the List operation.
type Kind string

const (
	KindProject Kind = "projects"
	KindTask    Kind = "tasks"
	KindTag     Kind = "tags"
)

// MemoryStatus values. Correction never deletes (design.md section 4).
type MemoryStatus string

const (
	MemoryActive   MemoryStatus = "active"
	MemoryInactive MemoryStatus = "inactive"
)

// ActorKind says whether an actor is a person or an agent.
type ActorKind string

const (
	ActorHuman ActorKind = "human"
	ActorAgent ActorKind = "agent"
)

// Actor is who said or saved something: a person or an agent, identified by
// handle (design.md section 4).
type Actor struct {
	ID        ID        `json:"id"`
	Kind      ActorKind `json:"kind"`
	Handle    string    `json:"handle"`
	AgentType string    `json:"agent_type,omitempty"`
}

// ItemKind names what a tag can be attached to.
type ItemKind string

const (
	ItemConversation ItemKind = "conversation"
	ItemMemory       ItemKind = "memory"
)

// Memory is a statement an agent or a person chose to keep. Project and Task
// are the effective attachment: the memory's own, or its conversation's when
// it has neither; the project is the task's when the task has one (design.md
// section 4).
type Memory struct {
	ID      ID       `json:"id"`
	Content string   `json:"content"`
	Project string   `json:"project,omitempty"`
	Task    string   `json:"task,omitempty"`
	Tags    []string `json:"tags"`
	// Conversation is the conversation the memory was saved in, if known.
	Conversation ID           `json:"conversation,omitempty"`
	Author       string       `json:"author,omitempty"`
	Status       MemoryStatus `json:"status"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

// Role says who wrote a message: the side that asked or the side that answered.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Conversation is one captured session, or one subagent run inside a session
// (Agent set, Parent the session's main conversation). It is identified by
// harness, session and agent (design.md section 5). Project and Task are the
// effective attachment: a subagent run with none of its own has its parent's,
// and the project follows the task (design.md section 4).
type Conversation struct {
	ID        ID        `json:"id"`
	Harness   string    `json:"harness"`
	Session   string    `json:"session"`
	Agent     string    `json:"agent,omitempty"`
	AgentType string    `json:"agent_type,omitempty"`
	Parent    ID        `json:"parent,omitempty"`
	Project   string    `json:"project,omitempty"`
	Task      string    `json:"task,omitempty"`
	Tags      []string  `json:"tags"`
	Human     string    `json:"human,omitempty"`
	Repo      string    `json:"repo,omitempty"`
	Branch    string    `json:"branch,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

// ConversationSummary is a conversation with what a listing shows about it:
// how many messages it holds, how many subagent runs it started, and the
// first message, which identifies the session to a person. A search with a
// query also says which message matched.
type ConversationSummary struct {
	Conversation
	Messages  int           `json:"messages"`
	Subagents int           `json:"subagents"`
	Opening   string        `json:"opening,omitempty"`
	Match     *MessageMatch `json:"match,omitempty"`
}

// MessageMatch is the message that made a conversation a search hit: the
// nearest by meaning, or else the first containing the query. Conversation
// is the message's own, which for a session can be one of its subagent runs,
// so the pair is what loci show <conversation> --around <message> takes.
type MessageMatch struct {
	Message      ID     `json:"message"`
	Conversation ID     `json:"conversation"`
	Snippet      string `json:"snippet"`
}

// Message is one prompt or one final answer in a conversation. Speaker and
// Addressee are actor handles, derived from the role and the conversation
// (design.md section 4).
type Message struct {
	ID             ID        `json:"id"`
	ConversationID ID        `json:"conversation_id"`
	Seq            int       `json:"seq"`
	PromptID       string    `json:"prompt_id,omitempty"`
	Role           Role      `json:"role"`
	Speaker        string    `json:"speaker,omitempty"`
	Addressee      string    `json:"addressee,omitempty"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
}
