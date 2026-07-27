package source_test

import (
	"strings"
	"testing"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/mapping"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/source"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/source/pull"
)

func TestEachDriverValidatesItsOwnSection(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  source.Config
	}{
		{"seed", source.Config{Driver: "seed", Seed: source.SeedConfig{Catalogue: "examples/seed-catalogue.yaml"}}},
		{"pull", source.Config{Driver: "pull", Pull: pull.Config{Endpoints: []pull.Endpoint{{
			ID: "a", URL: "https://x.test", Spec: mapping.Spec{Map: map[string]string{"id": "$.id"}},
		}}}}},
		{"push", source.Config{Driver: "push", Push: source.PushConfig{TokenEnvVar: "TOKEN"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if errs := tc.cfg.Validate(); len(errs) != 0 {
				t.Errorf("a valid %s config was rejected: %v", tc.name, errs)
			}
		})
	}
}

func TestOnlyTheActiveDriverIsValidated(t *testing.T) {
	// A deployment running on seed data should not be blocked by a
	// half-finished pull mapping it has not switched to yet; being unable to
	// leave work in progress in a config file makes the file harder to edit.
	cfg := source.Config{
		Driver: "seed",
		Seed:   source.SeedConfig{Catalogue: "examples/seed-catalogue.yaml"},
		Pull:   pull.Config{}, // deliberately empty
		Push:   source.PushConfig{},
	}

	if errs := cfg.Validate(); len(errs) != 0 {
		t.Errorf("an inactive driver's incomplete config blocked startup: %v", errs)
	}
}

func TestValidateRejectsMistakes(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  source.Config
		want string
	}{
		{"unknown driver", source.Config{Driver: "telepathy"}, "unknown source driver"},
		{"no driver at all", source.Config{}, "unknown source driver"},
		{"seed with no catalogue", source.Config{Driver: "seed"}, "no catalogue"},
		{"pull with no endpoints", source.Config{Driver: "pull"}, "no endpoints"},
		{
			// An unauthenticated ingest endpoint lets anyone rewrite the
			// dashboard, which is worse than having no dashboard.
			"push with no token",
			source.Config{Driver: "push"},
			"lets anyone rewrite the dashboard",
		},
		{
			"push with a negative body limit",
			source.Config{Driver: "push", Push: source.PushConfig{TokenEnvVar: "T", MaxBodyBytes: -1}},
			"cannot be negative",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := tc.cfg.Validate()
			if len(errs) == 0 {
				t.Fatal("accepted")
			}
			if !strings.Contains(errs[0].Error(), tc.want) {
				t.Errorf("error does not mention %q: %v", tc.want, errs[0])
			}
		})
	}
}

func TestBodyLimitFallsBackToADefault(t *testing.T) {
	if got := (source.PushConfig{}).BodyLimit(); got != source.DefaultMaxBodyBytes {
		t.Errorf("got %d, want the default", got)
	}
	if got := (source.PushConfig{MaxBodyBytes: 1024}).BodyLimit(); got != 1024 {
		t.Errorf("got %d, want the configured value", got)
	}
}

func TestDriversAreDocumented(t *testing.T) {
	if got := len(source.Drivers()); got != 3 {
		t.Errorf("got %d drivers, want three", got)
	}
}
