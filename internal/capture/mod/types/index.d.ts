/** A task as `loci ls tasks` lists it, most recently used first. */
export type Task = { name: string; project?: string }

/** The session's conversation as `loci conversations` returns it. */
export type Conversation = { id: string; project?: string; task?: string; tags: string[] }

/**
 * Which face the card shows: none, the task search, the new task's key and
 * project, the new project's name, or the one line saying where it attached.
 */
export type Mode = 'hidden' | 'search' | 'newTask' | 'newProject' | 'done'

export type Picker = {
  mode: Mode
  /** What the person typed in the card's field, per mode. */
  query: string
  taskKey: string
  projectSlug: string
  tasks: Task[]
  projects: string[]
  /** The row the person last moved to: outlined, and what Enter or the main button acts on. */
  focus: string | null
  error: string | null
  busy: boolean
  conversation: Conversation | null
  /** The line the next prompt carries to the agent after a pick. */
  note: string | null
}

declare module 'claude-code' {
  interface PluginState {
    'loci-pick': { picker: Picker }
  }
}
