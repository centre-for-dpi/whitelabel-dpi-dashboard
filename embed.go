package main

import (
	"embed"

	// The zone database, compiled in. A scratch image has no
	// /usr/share/zoneinfo, so without this a deployment configuring a local
	// timezone would silently get UTC. About 450 KB, paid once.
	_ "time/tzdata"
)

// Everything the dashboard needs to run is compiled into the binary: the
// default configuration, the templates, the stylesheet, htmx, and the fonts.
//
// This is what makes the deploy story a single artefact. `docker run` with no
// volumes, or a bare binary copied onto a host, produces a working dashboard
// with no files to install alongside it. A deployment that wants to customise
// anything points -config at a directory and overrides only the files it cares
// about; everything else keeps falling back to what is embedded here.

//go:embed config
var configFS embed.FS

//go:embed web/templates web/static
var webFS embed.FS

// Only the catalogue is embedded, not the generated fixtures. The running
// dashboard generates its demo data in memory from these few kilobytes; the
// fixtures under examples/ exist to be read, and shipping several megabytes of
// them inside the binary would serve nobody.
//
//go:embed examples/seed-catalogue.yaml
var examplesFS embed.FS
