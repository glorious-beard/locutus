package main

import (
	"github.com/alecthomas/kong"
	"github.com/chetan/locutus/cmd"
)

// Version is set by -ldflags at build time.
var version = "dev"

func main() {
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
