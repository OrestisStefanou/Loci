// Package version identifies the running build. Clients send it to loci
// serve, which shuts down on a mismatch so that an upgraded binary never
// talks to a stale server.
package version

import (
	"fmt"
	"os"
	"sync"
)

// Version is set at build time by the Makefile via -ldflags. Every make build
// gets a distinct value, so a rebuilt binary replaces a running server.
var Version = "dev"

// Get returns the build's identity: Version when the build stamped one, and
// otherwise (plain go build, go run) one derived from the executable's
// modification time and size, so those rebuilds also replace a running
// server. It costs one stat, which keeps hook startup fast.
var Get = sync.OnceValue(func() string {
	if Version != "dev" {
		return Version
	}
	return fromExecutable(os.Executable)
})

func fromExecutable(executable func() (string, error)) string {
	exe, err := executable()
	if err != nil {
		return "dev"
	}
	info, err := os.Stat(exe)
	if err != nil {
		return "dev"
	}
	return fmt.Sprintf("dev+%x-%d", info.ModTime().UnixNano(), info.Size())
}
