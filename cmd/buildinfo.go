package cmd

// buildVersion holds the locutus binary version string. Set by main() via
// SetVersion before parsing. `locutus update` consults this to decide whether
// self-update is available; the `kong.VersionFlag` in CLI.Version reads from
// kong.Vars at parse time and is independent of this var.
var buildVersion = "dev"

// SetVersion sets the binary version string exposed to commands that need it
// (currently `update`'s "already up to date" check). Called once from main.
func SetVersion(v string) {
	if v != "" {
		buildVersion = v
	}
}
