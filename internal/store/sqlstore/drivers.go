package sqlstore

import "sort"

// Drivers are registered by the build-tagged files alongside this one, each of
// which imports one database driver for its side effect.
//
// The indirection buys something concrete: the three drivers together are most
// of this binary's size, and a deployment that has settled on one can drop the
// other two with a build tag. `make build-lite` does exactly that. Without the
// registry, `Open` would have no way to tell "you configured postgres and this
// build excluded it" from "postgres is misspelled", and would report the wrong
// one.
var registered = map[string]string{}

// register maps a configured driver name to its database/sql driver name.
func register(configName, sqlDriver string) {
	registered[configName] = sqlDriver
}

// driverName resolves a configured driver to its registered database/sql name.
func driverName(configName string) (string, bool) {
	name, ok := registered[configName]
	return name, ok
}

// Compiled lists the drivers this build includes, for the startup log and for
// the error a deployment sees when it asks for one that was excluded.
func Compiled() []string {
	out := make([]string, 0, len(registered))
	for name := range registered {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
