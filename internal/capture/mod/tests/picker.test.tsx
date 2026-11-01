import { describe, expect, test } from 'claude-code/testing'
import type { On } from 'claude-code'

// The card against a loci that answers from memory: the tests check which
// loci commands the card runs and what it shows, never a real store.

type Fake = { tasks: { name: string; project?: string }[]; projects: string[]; task?: string; project?: string; calls: string[][] }

const SESSION = 's1'
const CONVERSATION = '01J9ZK3Q7V8X2M4N6P0R5T1W3Y'

function fakeLoci(on: On, f: Fake) {
  on('session.start', ($, e) => ({ cwd: e.cwd }) as any)
  on('ui.render', ($, e) => ($.ui.resolve(e) as any).Box({}))
  on('session.id', () => ({ value: SESSION }) as any)
  on('command.register', () => ({ value: undefined }) as any)
  on('ui.focus', () => ({}) as any)
  on('process.run', ($, e) => {
    const args = e.argv.slice(1).filter(a => a !== '--json')
    f.calls.push(args)
    const json = (v: unknown) => ({ value: { exitCode: 0, stdout: JSON.stringify(v), stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }) as any
    switch (args[0]) {
      case 'ls':
        return args[1] === 'tasks' ? json({ kind: 'tasks', items: f.tasks }) : json({ kind: 'projects', items: f.projects.map(name => ({ name })) })
      case 'conversations':
        expect(args).toEqual(['conversations', '--harness', 'claude-code', '--session', SESSION, '--limit', '1'])
        return json({ conversations: [{ id: CONVERSATION, project: f.project, task: f.task, tags: [] }] })
      case 'task':
        f.tasks.unshift({ name: args[1], project: args[3] })
        if (!f.projects.includes(args[3])) f.projects.unshift(args[3])
        return json({})
      case 'attach': {
        const at = (flag: string) => (args.includes(flag) ? args[args.indexOf(flag) + 1] : undefined)
        f.task = at('--task') ?? f.task
        f.project = f.tasks.find(t => t.name === f.task)?.project ?? at('--project') ?? f.project
        return json({})
      }
    }
    return { value: { exitCode: 1, stdout: '', stderr: `loci: unexpected ${args.join(' ')}`, isStdoutTruncated: false, isStderrTruncated: false } } as any
  })
}

const fixture = (): Fake => ({
  tasks: [{ name: 'EPD-1271', project: 'fees-v2' }, { name: 'OPS-88', project: 'trade-settlement' }, { name: 'EPD-990', project: 'fees-v2' }],
  projects: ['fees-v2', 'trade-settlement'],
  calls: [],
})

const start = ($: any) =>
  $.session.start({ cwd: '/repo', surface: 'terminal', isInteractive: true, startedAt: 0, context: {}, rateLimits: [] })

const card = ($: any) =>
  $.ui.mount({
    plugin: 'loci-pick',
    surface: 'terminal',
    component: 'AbovePrompt',
    props: { hasSurvey: false, isWorking: false, maxRows: 30, bodyColumns: 120, scroll: { offset: 0, bodyRows: 30 }, view: {} },
  })

describe('loci-pick', () => {
  test('an unattached session sees its tasks, most recently used first, and attaches with one press', async ($, on) => {
    const f = fixture()
    fakeLoci(on, f)
    await start($)
    const ui = await card($)
    const rows = (await ui.findAll({ type: 'Button' })).map(b => b.key).filter(k => k?.startsWith('task:'))
    expect(rows).toEqual(['task:EPD-1271', 'task:OPS-88', 'task:EPD-990'])

    await ui.press({ key: 'task:OPS-88' })
    expect(f.calls).toContainEqual(['attach', CONVERSATION, '--task', 'OPS-88'])
    expect((await ui.find({ type: 'Text', text: 'trade-settlement · OPS-88 · no tags' }))).toBeDefined()
  })

  test('the next prompt tells the agent what the person picked, and the summary line goes', async ($, on) => {
    const f = fixture()
    fakeLoci(on, f)
    let context: readonly string[] | undefined
    on('prompt.submit', ($, e) => {
      context = e.context
      return { text: e.text, context: e.context }
    })
    await start($)
    const ui = await card($)
    await ui.press({ key: 'task:EPD-1271' })
    await $.prompt.submit({ text: 'Fix the cut-off.', wait: false })
    expect(context).toEqual(['Loci: the person has since attached this session to fees-v2, EPD-1271, no tags.'])
    expect(await ui.find({ type: 'Text', text: /Loci/ })).toBeUndefined()
  })

  test('typing narrows the list by key or project', async ($, on) => {
    fakeLoci(on, fixture())
    await start($)
    const ui = await card($)
    await ui.input({ key: 'field', text: 'trade', kind: 'change' })
    const rows = (await ui.findAll({ type: 'Button' })).map(b => b.key).filter(k => k?.startsWith('task:'))
    expect(rows).toEqual(['task:OPS-88'])
  })

  test('a key that matches nothing becomes a new task, created in the project picked', async ($, on) => {
    const f = fixture()
    fakeLoci(on, f)
    await start($)
    const ui = await card($)
    await ui.input({ key: 'field', text: 'ops-130', kind: 'change' })
    await ui.input({ key: 'field', text: 'ops-130' })
    expect(await ui.find({ type: 'Text', text: /NEW TASK/ })).toBeDefined()
    await ui.press({ key: 'proj:trade-settlement' })
    expect(f.calls).toContainEqual(['task', 'OPS-130', '--project', 'trade-settlement'])
    expect(f.calls).toContainEqual(['attach', CONVERSATION, '--task', 'OPS-130'])
  })

  test('a new project can take the session alone', async ($, on) => {
    const f = fixture()
    fakeLoci(on, f)
    await start($)
    const ui = await card($)
    await ui.press({ key: 'new-project' })
    await ui.input({ key: 'field', text: 'Payments-Core', kind: 'change' })
    await ui.press({ key: 'project-only' })
    expect(f.calls).toContainEqual(['attach', CONVERSATION, '--project', 'payments-core'])
  })

  test('an attached session shows no card', async ($, on) => {
    const f = { ...fixture(), task: 'EPD-1271', project: 'fees-v2' }
    fakeLoci(on, f)
    await start($)
    const ui = await card($)
    expect(await ui.find({ text: /ATTACH THIS SESSION/ })).toBeUndefined()
  })
})
