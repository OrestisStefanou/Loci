import { atom, read, update } from 'claude-code'
import type { Hook, Register } from 'claude-code'

import type { Conversation, Picker, Task } from '../types'
import { loci } from './loci'

// The card above the prompt with which the person attaches the session to a
// Loci task or project (design.md section 7). It is UI only: every change is
// a loci command, and loci holds the state. Capture stays on the hooks loci
// init writes, so a Claude Code release that breaks this mod loses the card
// and nothing else.

type Dollar = Parameters<Hook<'session.start'>>[0]

const HARNESS = 'claude-code'

const initial: Picker = {
  mode: 'hidden',
  query: '',
  taskKey: '',
  projectSlug: '',
  tasks: [],
  projects: [],
  focus: null,
  error: null,
  busy: false,
  conversation: null,
  note: null,
}

const picker = atom({ plugin: 'loci-pick', key: 'picker' } as const, initial)

/** Runs loci with --json and parses what it prints; throws with loci's own message on failure. */
async function run($: Dollar, args: string[]): Promise<any> {
  const out = await $.process.run([...loci, ...args, '--json'], { timeoutMs: 5000 })
  if (out.exitCode !== 0) throw new Error((out.stderr || out.stdout).trim().replace(/^loci: /, ''))
  return out.stdout.trim() === '' ? {} : JSON.parse(out.stdout)
}

/** The session's conversation, or null while capture has not stored it yet. */
async function findConversation($: Dollar): Promise<Conversation | null> {
  const session = await $.session.id()
  const out = await run($, ['conversations', '--harness', HARNESS, '--session', session, '--limit', '1'])
  const c = out.conversations?.[0]
  return c === undefined ? null : { id: c.id, project: c.project || undefined, task: c.task || undefined, tags: c.tags ?? [] }
}

/** Reads the tasks, the projects and the session's conversation into the card. */
async function load($: Dollar): Promise<void> {
  const [tasks, projects, conversation] = await Promise.all([run($, ['ls', 'tasks']), run($, ['ls', 'projects']), findConversation($)])
  await update($, picker, p => ({
    ...p,
    tasks: (tasks.items ?? []).map((t: any): Task => ({ name: t.name, project: t.project || undefined })),
    projects: (projects.items ?? []).map((one: any) => String(one.name)),
    conversation,
  }))
}

const isAttached = (c: Conversation | null) => c !== null && (c.project !== undefined || c.task !== undefined)

const message = (err: unknown) => (err instanceof Error ? err.message : String(err))

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`

/** Tasks whose key or project holds every word typed, in loci's order (most recently used first). */
function matches(p: Picker): Task[] {
  const words = p.query.toLowerCase().split(/\s+/).filter(Boolean)
  return p.tasks.filter(t => words.every(w => t.name.toLowerCase().includes(w) || (t.project ?? '').includes(w)))
}

/** Where the session is attached, as one line. */
function where(c: Conversation | null): string {
  return [c?.project ?? 'no project', c?.task ?? 'no task', c && c.tags.length ? c.tags.join(', ') : 'no tags'].join(' · ')
}

/**
 * Attaches the session's conversation: to a task (setting its project first
 * when the person chose one for a new task) or to a project alone.
 */
async function attach($: Dollar, target: { task?: string; project?: string; taskProject?: string | null }): Promise<void> {
  await update($, picker, p => ({ ...p, busy: true, error: null }))
  try {
    const conversation = (await read($, picker)).conversation ?? (await findConversation($))
    if (conversation === null) throw new Error('this session is not captured yet: send a prompt, then pick')
    if (target.task !== undefined && target.taskProject) await run($, ['task', target.task, '--project', target.taskProject])
    const args = ['attach', conversation.id]
    if (target.task !== undefined) args.push('--task', target.task)
    if (target.project !== undefined) args.push('--project', target.project)
    await run($, args)
    const now = await findConversation($)
    await update($, picker, p => ({
      ...p,
      busy: false,
      mode: 'done',
      conversation: now,
      query: '',
      taskKey: '',
      projectSlug: '',
      focus: null,
      note: `Loci: the person has since attached this session to ${where(now).replace(/ · /g, ', ')}.`,
    }))
    await load($)
  } catch (err) {
    await update($, picker, p => ({ ...p, busy: false, error: message(err) }))
  }
}

/** Opens the card on the task search, with fresh lists. */
async function open($: Dollar): Promise<void> {
  await update($, picker, p => ({ ...p, mode: 'search', query: '', focus: null, error: null }))
  try {
    await load($)
  } catch (err) {
    await update($, picker, p => ({ ...p, error: message(err) }))
  }
}

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    const result = await next(e)
    try {
      // Not /loci: that is the skill's, and the two layers must not compete for one name.
      await $.command.register({ name: 'loci-pick', description: 'Attach this session to a Loci task or project' })
    } catch {
      /* a name taken elsewhere must not stop the card */
    }
    await update($, picker, () => initial)
    // The card is an offer: it shows only when loci answers and the session is unattached.
    void load($)
      .then(async () => {
        if (!isAttached((await read($, picker)).conversation)) await update($, picker, p => ({ ...p, mode: 'search' }))
      })
      .catch(() => undefined)
    return result
  })

  on('command.run', { command: 'loci-pick' }, async $ => {
    await open($)
    return { text: 'Loci: pick a task above the prompt (ctrl+x tab to search).' }
  })

  // The agent attaches the session itself when the card is skipped: once it has, the card gives way to the summary line.
  on('turn.complete', async ($, e, next) => {
    const result = await next(e)
    const p = await read($, picker)
    if (p.mode === 'search' && !p.busy) {
      const conversation = await findConversation($).catch(() => p.conversation)
      await update($, picker, q => ({ ...q, conversation, mode: isAttached(conversation) ? 'done' : q.mode }))
    }
    return result
  })

  // The next prompt tells the agent what the person picked, and puts the summary line away.
  on('prompt.submit', async ($, e, next) => {
    const p = await read($, picker)
    if (p.mode === 'done' || p.note !== null) await update($, picker, q => ({ ...q, mode: q.mode === 'done' ? 'hidden' : q.mode, note: null }))
    return p.note === null ? next(e) : next({ ...e, context: [...(e.context ?? []), p.note] })
  })

  on('ui.focus', { component: 'AbovePrompt' }, async ($, e, next) => {
    const element = e.element ?? ''
    if (element.startsWith('task:') || element.startsWith('proj:')) await update($, picker, p => ({ ...p, focus: element }))
    return next(e)
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    const p = await read($, picker)
    if (e.props.hasSurvey || p.mode === 'hidden') return next(e)

    const { Box, Text, Button, Input } = $.ui.resolve(e) as any
    const set = (fn: (q: Picker) => Picker) => void update($, picker, fn)
    // Changing face unmounts the focused button, which would hand the keys
    // back to the prompt. The field is drawn on every face under one key, so
    // the focus moves there first.
    const go = (fn: (q: Picker) => Picker) =>
      void Promise.resolve()
        .then(() => $.ui.focus({ requestId: e.requestId, key: 'field' }))
        .catch(() => undefined)
        .then(() => update($, picker, fn))

    if (p.mode === 'done') {
      return (
        <Box flexDirection="row" paddingX={1}>
          <Text color="success" bold>
            ✓ Loci{' '}
          </Text>
          <Text>{where(p.conversation)}</Text>
          <Text dimColor>{'   /loci-pick to change'}</Text>
        </Box>
      )
    }

    // The band gets at most half the terminal, the prompt included, so the
    // card shrinks to fit: framed rows, then one line per row, then no frames
    // at all. Besides the rows it takes a header, the field, the buttons, and
    // a line each for a note or heading and an error; the card's frame takes
    // 2 more, the field's 2.
    const maxRows = Math.max(4, Math.floor(Number(e.props.maxRows) || 20))
    const fixed = 3 + 1 + (p.error ? 1 : 0)
    const layout = maxRows - fixed - 4 >= 9 ? 'rich' : maxRows - fixed - 4 >= 2 ? 'compact' : 'tight'
    const room = maxRows - fixed - (layout === 'tight' ? 0 : 4)
    const isRich = layout === 'rich'
    const limit = Math.max(1, Math.min(5, isRich ? Math.floor(room / 3) : room))

    type Row = { key: string; label: string; meta: string; onPress: () => void }
    let title = ''
    let rows: Row[] = []
    let field: any = null
    let actions: any[] = []
    let note = ''
    let heading = ''
    const selected = () => rows.find(r => r.key === p.focus) ?? rows[0]

    if (p.mode === 'search') {
      title = 'ATTACH THIS SESSION'
      const found = matches(p)
      rows = found.slice(0, limit).map(t => ({
        key: `task:${t.name}`,
        label: t.name,
        meta: t.project ?? 'no project',
        onPress: () => void attach($, { task: t.name }),
      }))
      if (found.length > limit) note = `${found.length - limit} more: type to narrow`
      if (found.length === 0) note = p.query ? `No task matches "${p.query}". Enter creates it as a new task.` : 'No tasks yet. Enter starts a new one.'
      const newTask = (key: string) => go(q => ({ ...q, mode: 'newTask', taskKey: key, focus: null, error: null }))
      field = (
        <Input
          key="field"
          label="🔎"
          placeholder="search tasks and projects"
          value={p.query}
          autoFocus
          submitLabel={rows.length ? 'attach' : 'new task'}
          onInput={(value: string) => set(q => ({ ...q, query: value, focus: null }))}
          onSubmit={(value: string) => (rows.length ? rows[0].onPress() : newTask(value.trim().toUpperCase()))}
        />
      )
      const guess = /^[a-z]+-\d+$/i.test(p.query.trim()) ? p.query.trim().toUpperCase() : ''
      actions = [
        <Button key="attach" variant="primary" label="Attach" onPress={() => selected()?.onPress()} />,
        <Button key="new-task" hotkey="n" label="New task" onPress={() => newTask(guess)} />,
        <Button key="new-project" hotkey="p" label="New project" onPress={() => go(q => ({ ...q, mode: 'newProject', projectSlug: '', focus: null, error: null }))} />,
        <Button key="skip" hotkey="x" dimColor label="Not now" onPress={() => set(q => ({ ...q, mode: 'hidden' }))} />,
      ]
    }

    if (p.mode === 'newTask') {
      title = 'NEW TASK'
      heading = 'WHICH PROJECT'
      const key = p.taskKey.trim().toUpperCase()
      const create = (project: string | null) =>
        key === '' ? set(q => ({ ...q, error: 'type the task key first, e.g. EPD-1302' })) : void attach($, { task: key, taskProject: project })
      const isNew = p.projectSlug !== '' && !p.projects.includes(p.projectSlug)
      const projects = isNew ? [p.projectSlug, ...p.projects] : p.projects
      rows = [
        ...projects.slice(0, limit - 1).map(slug => ({
          key: `proj:${slug}`,
          label: slug,
          meta: isNew && slug === p.projectSlug ? 'new project' : plural(p.tasks.filter(t => t.project === slug).length, 'task'),
          onPress: () => create(slug),
        })),
        { key: 'proj:-', label: 'No project', meta: 'set it later', onPress: () => create(null) },
      ]
      field = (
        <Input
          key="field"
          label="key"
          placeholder="EPD-1302"
          value={p.taskKey}
          autoFocus
          submitLabel="pick project"
          onInput={(value: string) => set(q => ({ ...q, taskKey: value }))}
          onSubmit={() => void $.ui.focus({ requestId: e.requestId, key: selected().key })}
        />
      )
      actions = [
        <Button key="create" variant="primary" label="Create and attach" onPress={() => selected()?.onPress()} />,
        <Button key="new-project" hotkey="p" label="New project" onPress={() => go(q => ({ ...q, mode: 'newProject', focus: null, error: null }))} />,
        <Button key="back" hotkey="b" dimColor label="Back" onPress={() => go(q => ({ ...q, mode: 'search', focus: null, error: null }))} />,
      ]
    }

    if (p.mode === 'newProject') {
      title = 'NEW PROJECT'
      note = 'A project is a body of work; tasks belong to it.'
      const slug = p.projectSlug.trim().toLowerCase()
      const toTask = () =>
        slug === ''
          ? set(q => ({ ...q, error: 'type the project name first, e.g. fees-v2' }))
          : go(q => ({ ...q, projectSlug: slug, mode: 'newTask', focus: `proj:${slug}`, error: null }))
      field = (
        <Input
          key="field"
          label="name"
          placeholder="fees-v2"
          value={p.projectSlug}
          autoFocus
          submitLabel="next"
          onInput={(value: string) => set(q => ({ ...q, projectSlug: value }))}
          onSubmit={toTask}
        />
      )
      actions = [
        <Button key="to-task" variant="primary" label="Add a task to it" onPress={toTask} />,
        <Button key="project-only" hotkey="o" label="Attach to the project only" onPress={() => (slug === '' ? toTask() : void attach($, { project: slug }))} />,
        <Button key="back" hotkey="b" dimColor label="Back" onPress={() => go(q => ({ ...q, mode: 'search', focus: null, error: null }))} />,
      ]
    }

    const highlighted = selected()?.key
    const rowView = (r: Row, i: number) => {
      const isOn = r.key === highlighted
      const line = (
        <Box flexDirection="row" justifyContent="space-between" flexGrow={1}>
          <Button key={r.key} plain hotkey={String(i + 1)} onPress={r.onPress}>
            <Text bold={isOn}>{r.label}</Text>
          </Button>
          <Text dimColor={!isOn} color={isOn ? 'claude' : undefined}>
            {r.meta}
          </Text>
        </Box>
      )
      // Short terminals get one line per row, the selected one marked.
      return isRich ? (
        <Box key={`box-${r.key}`} borderStyle="round" borderColor={isOn ? 'claude' : 'subtle'} borderDimColor={!isOn} paddingX={1}>
          {line}
        </Box>
      ) : (
        <Box key={`box-${r.key}`} flexDirection="row" paddingX={1}>
          <Text color="claude">{isOn ? '❯ ' : '  '}</Text>
          {line}
        </Box>
      )
    }

    const status = p.busy ? 'working…' : isAttached(p.conversation) ? where(p.conversation) : 'unattached'

    return (
      <Box flexDirection="column">
        <Box flexDirection="column" paddingX={1} {...(layout === 'tight' ? {} : { borderStyle: 'round', borderColor: 'subtle' })}>
          <Box flexDirection="row" justifyContent="space-between">
            <Text color="success" bold>
              ◆ LOCI · {title}
            </Text>
            <Text dimColor>{status}</Text>
          </Box>
          {Input !== undefined && (
            <Box paddingX={1} {...(layout === 'tight' ? {} : { borderStyle: 'round', borderColor: 'promptBorder' })}>
              {field}
            </Box>
          )}
          {heading !== '' && (
            <Text dimColor bold>
              {heading}
            </Text>
          )}
          {rows.map(rowView)}
          {note !== '' && <Text dimColor>{`  ${note}`}</Text>}
          {p.error && <Text color="error">{`  ${p.error}`}</Text>}
        </Box>
        <Box flexDirection="row" justifyContent="space-between">
          <Box flexDirection="row" gap={1}>
            {actions}
          </Box>
          {Number(e.props.bodyColumns) >= 112 && (
            <Text dimColor>{`${rows.length ? `1-${rows.length} pick · ` : ''}ctrl+x tab search · ↑↓ move · esc prompt `}</Text>
          )}
        </Box>
      </Box>
    )
  })
}
