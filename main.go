package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/chetan/locutus/cmd"
	"github.com/joho/godotenv"
)

// Version is set by -ldflags at build time.
var version = "dev"

func main() {
	// Best-effort load of .env from the current working directory. godotenv
	// does not overwrite variables already set in the environment, so a
	// shell-exported value still wins over an entry in .env. A missing
	// file is not an error.
	_ = godotenv.Load()

	cmd.SetVersion(version)

	// SIGINT (Ctrl-C) and SIGTERM cancel the bound context so handlers can
	// unwind cleanly: in-flight subprocesses get killed via CommandContext,
	// dispatch loops select on ctx.Done(), the executor stops accepting new
	// workstreams. SIGKILL still bypasses everything — DJ-073's persisted
	// workstream records are the recovery story for that case.
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var cli cmd.CLI
	kctx := kong.Parse(&cli,
		kong.Name("locutus"),
		kong.Description("Autonomous project manager for spec-driven software"),
		kong.Vars{"version": version},
		kong.BindTo(signalCtx, (*context.Context)(nil)),
	)
	err := kctx.Run(&cli)
	if code, ok := cmd.IsExitCode(err); ok {
		os.Exit(code)
	}
	kctx.FatalIfErrorf(err)
}
