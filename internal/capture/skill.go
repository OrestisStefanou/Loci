package capture

import (
	"bytes"
	"strings"
)

// skillMarker identifies a skill file loci init wrote, so init replaces only
// its own file and --remove deletes only that.
const skillMarker = "<!-- Written by loci init."

// Skill returns the /loci skill (design.md section 7): a Claude Code skill
// that has the agent change its session's attachment with the CLI. loci is
// the command to run, the binary by absolute path with any store flags.
func Skill(loci string) []byte {
	return []byte(strings.ReplaceAll(skill, "{loci}", loci))
}

// IsSkill reports whether contents is a skill file loci init wrote.
func IsSkill(contents []byte) bool {
	return bytes.Contains(contents, []byte(skillMarker))
}

const skill = `---
name: loci
description: Change which Loci project, task or tags this session is attached to. Use when the person runs /loci, or asks to attach, move or tag this session in Loci.
---

` + skillMarker + ` Run loci init again to update it; loci init --remove deletes it. -->

# Attach this session in Loci

This session's Loci conversation id is in your context, in the line ` + "`Loci: this session is conversation <id>, ...`" + `. Use that id and no other.

If the person said what to change (` + "`/loci EPD-12`" + `, "attach this to fees-v2", "tag it auth"), do that. Otherwise work out the task from the branch, the worktree name and the conversation so far; if you cannot tell, ask the person in one line.

Run the commands exactly as written here, path included:

- ` + "`{loci} ls tasks [query]`" + `, ` + "`{loci} ls projects [query]`" + `, ` + "`{loci} ls tags [query]`" + `: what exists. A task is listed with its project. Use an existing key, slug or tag rather than creating a near-duplicate.
- ` + "`{loci} attach <id> --task <KEY>`" + `: set the task; a new key is created. ` + "`--no-task`" + ` removes it.
- ` + "`--project <slug>`" + ` sets the project, which shows only while the task has no project of its own. ` + "`--no-project`" + ` removes it.
- ` + "`--tag <name>`" + ` (repeatable) replaces all the tags; ` + "`--no-tags`" + ` removes them.

Only what you pass changes, and several flags can go in one ` + "`attach`" + `. When done, reply with one line: ` + "`Loci: project <slug or none>, task <KEY or none>, tags <names or none>.`" + `
`
