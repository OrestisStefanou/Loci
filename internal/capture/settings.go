package capture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// HookEvents are the Claude Code events the translator handles, in the order
// they are written to settings.json.
var HookEvents = []string{"SessionStart", "UserPromptSubmit", "Stop", "SubagentStart", "SubagentStop"}

// hookTimeout is Claude Code's limit for one hook run, in seconds. The
// translator stops itself well before it.
const hookTimeout = 10

// translatorMarker identifies a hook entry written by any loci binary,
// wherever it lives, so a reinstall from another path replaces it instead of
// adding a second one.
const translatorMarker = " capture claude-code"

// InstallHooks returns settings (the contents of Claude Code's settings.json,
// empty if it does not exist) with a hook running command on every event in
// HookEvents. Earlier loci entries are replaced; everything else, including
// the order of keys, is kept.
func InstallHooks(settings []byte, command string) ([]byte, error) {
	return editHooks(settings, func(event string, groups []object) []object {
		handler := object{
			{"type", jsonString("command")},
			{"command", jsonString(command)},
			{"timeout", json.RawMessage(strconv.Itoa(hookTimeout))},
		}
		return append(groups, object{{"hooks", array([]object{handler})}})
	})
}

// RemoveHooks returns settings without the hook entries InstallHooks writes.
func RemoveHooks(settings []byte) ([]byte, error) {
	return editHooks(settings, func(_ string, groups []object) []object { return groups })
}

// editHooks removes loci's handlers from every event in HookEvents, lets add
// append new groups, and drops whatever is left empty.
func editHooks(settings []byte, add func(event string, groups []object) []object) ([]byte, error) {
	root, err := parseSettings(settings)
	if err != nil {
		return nil, err
	}
	var hooks object
	if raw, ok := root.get("hooks"); ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, fmt.Errorf("parse Claude Code settings: hooks: %w", err)
		}
	}
	for _, event := range HookEvents {
		var groups []object
		if raw, ok := hooks.get(event); ok {
			if err := json.Unmarshal(raw, &groups); err != nil {
				return nil, fmt.Errorf("parse Claude Code settings: hooks.%s: %w", event, err)
			}
		}
		kept, err := withoutTranslator(groups)
		if err != nil {
			return nil, fmt.Errorf("parse Claude Code settings: hooks.%s: %w", event, err)
		}
		kept = add(event, kept)
		if len(kept) == 0 {
			hooks = hooks.without(event)
			continue
		}
		hooks = hooks.with(event, array(kept))
	}
	if len(hooks) == 0 {
		root = root.without("hooks")
	} else {
		root = root.with("hooks", hooks.raw())
	}
	return encodeSettings(root)
}

// parseSettings reads a settings file's contents, empty when it does not
// exist.
func parseSettings(settings []byte) (object, error) {
	var root object
	if len(bytes.TrimSpace(settings)) > 0 {
		if err := json.Unmarshal(settings, &root); err != nil {
			return nil, fmt.Errorf("parse Claude Code settings: %w", err)
		}
	}
	return root, nil
}

// encodeSettings writes root indented, as Claude Code does.
func encodeSettings(root object) ([]byte, error) {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// withoutTranslator drops loci's handlers from groups, and groups left with
// no handlers.
func withoutTranslator(groups []object) ([]object, error) {
	kept := []object{}
	for _, g := range groups {
		raw, ok := g.get("hooks")
		if !ok {
			kept = append(kept, g)
			continue
		}
		var handlers []object
		if err := json.Unmarshal(raw, &handlers); err != nil {
			return nil, err
		}
		var rest []object
		for _, h := range handlers {
			if !isTranslator(h) {
				rest = append(rest, h)
			}
		}
		switch {
		case len(rest) == len(handlers):
			kept = append(kept, g)
		case len(rest) > 0:
			kept = append(kept, g.with("hooks", array(rest)))
		}
	}
	return kept, nil
}

func isTranslator(handler object) bool {
	raw, ok := handler.get("command")
	if !ok {
		return false
	}
	var cmd string
	if json.Unmarshal(raw, &cmd) != nil {
		return false
	}
	return strings.Contains(cmd, translatorMarker)
}

// allowedCommands are the loci subcommands the context block has the agent
// run, to attach its session and save memories (design.md section 7).
// Allowing them spares the person a permission prompt on every session.
var allowedCommands = []string{"attach", "ls", "remember"}

// InstallPermissions returns settings with permissions.allow rules letting
// the agent run loci (the binary by absolute path, with any store flags) for
// each of allowedCommands. Rules for the command loci's hooks run are
// replaced, so call it before InstallHooks; every other rule and setting is
// kept.
func InstallPermissions(settings []byte, loci string) ([]byte, error) {
	return editAllow(settings, func(rules []string) []string {
		for _, sub := range allowedCommands {
			if rule := "Bash(" + loci + " " + sub + ":*)"; !slices.Contains(rules, rule) {
				rules = append(rules, rule)
			}
		}
		return rules
	})
}

// RemovePermissions returns settings without the rules InstallPermissions
// writes. It finds them through loci's hooks, so call it before RemoveHooks.
func RemovePermissions(settings []byte) ([]byte, error) {
	return editAllow(settings, func(rules []string) []string { return rules })
}

// editAllow removes loci's rules from permissions.allow, lets add append new
// ones, and drops whatever is left empty.
func editAllow(settings []byte, add func(rules []string) []string) ([]byte, error) {
	root, err := parseSettings(settings)
	if err != nil {
		return nil, err
	}
	var perms object
	if raw, ok := root.get("permissions"); ok {
		if err := json.Unmarshal(raw, &perms); err != nil {
			return nil, fmt.Errorf("parse Claude Code settings: permissions: %w", err)
		}
	}
	var rules []string
	if raw, ok := perms.get("allow"); ok {
		if err := json.Unmarshal(raw, &rules); err != nil {
			return nil, fmt.Errorf("parse Claude Code settings: permissions.allow: %w", err)
		}
	}
	lociRules, err := hookRules(root)
	if err != nil {
		return nil, err
	}
	kept := []string{}
	for _, r := range rules {
		if !lociRules[r] {
			kept = append(kept, r)
		}
	}
	kept = add(kept)
	if len(kept) == 0 {
		perms = perms.without("allow")
	} else {
		perms = perms.with("allow", stringArray(kept))
	}
	if len(perms) == 0 {
		root = root.without("permissions")
	} else {
		root = root.with("permissions", perms.raw())
	}
	return encodeSettings(root)
}

// hookRules returns the rules InstallPermissions writes for each command
// loci's hooks in root run, whatever the binary is called or where it lives.
func hookRules(root object) (map[string]bool, error) {
	var hooks map[string][]object
	if raw, ok := root.get("hooks"); ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, fmt.Errorf("parse Claude Code settings: hooks: %w", err)
		}
	}
	rules := map[string]bool{}
	for _, groups := range hooks {
		for _, g := range groups {
			var handlers []struct {
				Command string `json:"command"`
			}
			if raw, ok := g.get("hooks"); ok {
				if err := json.Unmarshal(raw, &handlers); err != nil {
					return nil, fmt.Errorf("parse Claude Code settings: hooks: %w", err)
				}
			}
			for _, h := range handlers {
				loci, ok := strings.CutSuffix(h.Command, translatorMarker)
				if !ok {
					continue
				}
				for _, sub := range allowedCommands {
					rules["Bash("+loci+" "+sub+":*)"] = true
				}
			}
		}
	}
	return rules, nil
}

// object is a JSON object that keeps its keys in order, so rewriting one key
// of a person's settings file does not reorder the rest.
type object []member

type member struct {
	key   string
	value json.RawMessage
}

func (o object) get(key string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.key == key {
			return m.value, true
		}
	}
	return nil, false
}

// with sets key, in place if present, else at the end.
func (o object) with(key string, value json.RawMessage) object {
	out := append(object{}, o...)
	for i := range out {
		if out[i].key == key {
			out[i].value = value
			return out
		}
	}
	return append(out, member{key, value})
}

func (o object) without(key string) object {
	out := object{}
	for _, m := range o {
		if m.key != key {
			out = append(out, m)
		}
	}
	return out
}

func (o object) MarshalJSON() ([]byte, error) { return o.raw(), nil }

// raw encodes o by hand: json.Marshal would escape <, > and & in the values
// it passes through, rewriting commands such as "a && b" in other hooks.
func (o object) raw() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(jsonString(m.key))
		b.WriteByte(':')
		b.Write(m.value)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func array(objs []object) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, o := range objs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(o.raw())
	}
	b.WriteByte(']')
	return b.Bytes()
}

// stringArray encodes a list of strings without escaping <, > and &.
func stringArray(ss []string) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, s := range ss {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(jsonString(s))
	}
	b.WriteByte(']')
	return b.Bytes()
}

func jsonString(s string) json.RawMessage {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // encoding a string cannot fail
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

func (o *object) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return errors.New("expected a JSON object")
	}
	out := object{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, _ := tok.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return err
		}
		out = out.with(key, value)
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after the JSON object")
	}
	*o = out
	return nil
}
