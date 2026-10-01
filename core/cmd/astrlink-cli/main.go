// Command astrlink gives coding agents read-only, shell-friendly access to
// the local AstrLink gateway's request records through the Control API.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/QuantumNous/astrlink/core/internal/agentcli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := agentcli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
