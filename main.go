package main

import (
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

	var cli cmd.CLI
	ctx := kong.Parse(&cli,
		kong.Name("locutus"),
		kong.Description("Autonomous project manager for spec-driven software"),
		kong.Vars{"version": version},
	)
	cli.Version.Version = version
	err := ctx.Run(&cli)
	ctx.FatalIfErrorf(err)
}
