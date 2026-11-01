// Command loci is the single Loci binary (design.md section 3).
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/OrestisStefanou/Loci/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, cli.Env{Stdout: os.Stdout, Stderr: os.Stderr, Stdin: os.Stdin}, os.Args[1:])
	stop()
	os.Exit(code)
}
