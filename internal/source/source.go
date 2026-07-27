// Package source selects and configures where the dashboard's data comes from.
//
// Three drivers, one active at a time, chosen by a single key in sources.yaml.
// The server does not know which is running: it asks a Snapshots for the
// current state of the world and renders whatever it gets.
package source

import (
	"fmt"
	"slices"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/source/pull"
)

// Driver names.
const (
	DriverSeed = "seed"
	DriverPull = "pull"
	DriverPush = "push"
)

// Drivers lists every driver, for validation and documentation.
func Drivers() []string { return []string{DriverSeed, DriverPull, DriverPush} }

// Config is sources.yaml.
type Config struct {
	Driver string      `yaml:"driver"`
	Seed   SeedConfig  `yaml:"seed"`
	Pull   pull.Config `yaml:"pull"`
	Push   PushConfig  `yaml:"push"`
}

// SeedConfig points at the catalogue the demonstration data is generated from.
type SeedConfig struct {
	Catalogue string `yaml:"catalogue"`
}

// PushConfig is the ingest endpoint's settings.
type PushConfig struct {
	// TokenEnvVar names the environment variable holding the bearer token
	// collectors must present. Never the token itself: a secret in a config
	// file ends up in a git history.
	TokenEnvVar string `yaml:"tokenEnvVar"`
	// MaxBodyBytes caps a request, so a runaway collector cannot exhaust
	// memory.
	MaxBodyBytes int64 `yaml:"maxBodyBytes"`
}

// DefaultMaxBodyBytes is used when a deployment sets no cap.
const DefaultMaxBodyBytes = 32 << 20

// Validate checks a configuration at startup.
//
// Only the active driver is validated in full: a deployment running on seed
// data should not be blocked by a half-finished pull mapping it has not
// switched to yet, and being unable to leave notes-in-progress in a config file
// makes the file harder to work on.
func (c Config) Validate() []error {
	var errs []error

	if !slices.Contains(Drivers(), c.Driver) {
		return []error{fmt.Errorf("unknown source driver %q; expected one of %v", c.Driver, Drivers())}
	}

	switch c.Driver {
	case DriverSeed:
		if c.Seed.Catalogue == "" {
			errs = append(errs, fmt.Errorf("seed: no catalogue"))
		}

	case DriverPull:
		if len(c.Pull.Endpoints) == 0 {
			errs = append(errs, fmt.Errorf("pull: no endpoints"))
		}
		// The full check happens in pull.New, which compiles every mapping and
		// reports all of their problems together.

	case DriverPush:
		if c.Push.TokenEnvVar == "" {
			errs = append(errs, fmt.Errorf(
				"push: no tokenEnvVar; an unauthenticated ingest endpoint lets anyone rewrite the dashboard"))
		}
		if c.Push.MaxBodyBytes < 0 {
			errs = append(errs, fmt.Errorf("push: maxBodyBytes cannot be negative"))
		}
	}
	return errs
}

// BodyLimit is the configured cap, or the default.
func (p PushConfig) BodyLimit() int64 {
	if p.MaxBodyBytes <= 0 {
		return DefaultMaxBodyBytes
	}
	return p.MaxBodyBytes
}
