package capture

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
)

// mod is the loci-pick Claude Code mod (design.md section 7): the card above
// the prompt with which the person attaches the session. Its tests stay in
// the repository; loci init writes the rest.
//
//go:embed all:mod
var mod embed.FS

// ModLociFile is the mod's file naming the loci command, which loci init
// writes. It carries the marker, so init replaces only a mod it wrote and
// --remove deletes only that.
const ModLociFile = "hooks/loci.ts"

const modMarker = "// Written by loci init."

// Mod returns the mod's files by path inside its folder, with hooks/loci.ts
// naming loci, the command the card runs: the binary by absolute path with
// any store flags, one argument each.
func Mod(loci []string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(mod, "mod", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(path, "mod/")
		if strings.HasPrefix(rel, "tests/") || rel == ModLociFile {
			return nil
		}
		files[rel], err = mod.ReadFile(path)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("read the embedded mod: %w", err)
	}
	argv, err := json.Marshal(loci)
	if err != nil {
		return nil, err
	}
	files[ModLociFile] = []byte(modMarker + " Run loci init again to update the mod; loci init --remove deletes it.\n" +
		"// The loci command the card runs.\n" +
		"export const loci: readonly string[] = " + string(argv) + "\n")
	return files, nil
}

// IsMod reports whether contents, a mod's hooks/loci.ts, is one loci init
// wrote.
func IsMod(contents []byte) bool {
	return bytes.HasPrefix(contents, []byte(modMarker))
}
