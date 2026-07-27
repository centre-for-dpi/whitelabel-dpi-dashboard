package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
)

func defaultsFS() fstest.MapFS {
	return fstest.MapFS{
		"config/app.yaml":    {Data: []byte(validApp)},
		"config/brand.yaml":  {Data: []byte(validBrand)},
		"config/domain.yaml": {Data: []byte(validDomain)},
		"config/theme.yaml":  {Data: []byte(validTheme)},
		"config/icons.yaml":  {Data: []byte(validIcons)},
	}
}

func noEnv(string) (string, bool) { return "", false }

func TestLoadUsesDefaultsWhenNoOverrideDir(t *testing.T) {
	cfg, err := config.Load(defaultsFS(), "", noEnv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.App.Server.Addr != ":8080" {
		t.Errorf("addr = %q, want the embedded default", cfg.App.Server.Addr)
	}
}

func TestLoadOverlaysWholeFiles(t *testing.T) {
	// Overlay is whole-file, not deep-merge: the file on disk is exactly what
	// is in effect, with nothing surviving underneath it.
	dir := t.TempDir()
	custom := strings.Replace(validBrand, "wordmarkTermId: brand.wordmark", "wordmarkTermId: acme.wordmark", 1)
	custom = strings.Replace(custom, "iconKey: brand.mark", "iconKey: brand.mark", 1)
	writeFile(t, dir, "brand.yaml", custom)

	cfg, err := config.Load(defaultsFS(), dir, noEnv)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Brand.WordmarkTermID != "acme.wordmark" {
		t.Errorf("wordmark = %q, want the overridden value", cfg.Brand.WordmarkTermID)
	}
	// Files that were not overridden must still come from the defaults.
	if cfg.App.Server.Addr != ":8080" {
		t.Errorf("addr = %q; overriding brand.yaml should not disturb app.yaml", cfg.App.Server.Addr)
	}
}

func TestLoadAcceptsAlternateExtensions(t *testing.T) {
	// yaml.v3 parses JSON, so a deployment generating config from a template
	// need not adopt a particular extension.
	for _, ext := range []string{".yml", ".json"} {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			body := validBrand
			if ext == ".json" {
				body = `{"wordmarkTermId":"json.wordmark","taglineTermId":"brand.tagline","iconKey":"brand.mark","favicon":"/f.svg","footer":[]}`
			} else {
				body = strings.Replace(body, "wordmarkTermId: brand.wordmark", "wordmarkTermId: yml.wordmark", 1)
			}
			writeFile(t, dir, "brand"+ext, body)

			cfg, err := config.Load(defaultsFS(), dir, noEnv)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			want := "yml.wordmark"
			if ext == ".json" {
				want = "json.wordmark"
			}
			if cfg.Brand.WordmarkTermID != want {
				t.Errorf("wordmark = %q, want %q", cfg.Brand.WordmarkTermID, want)
			}
		})
	}
}

func TestLoadRejectsUnrecognisedFile(t *testing.T) {
	// A deployment that writes "domains.yaml" almost certainly meant
	// "domain.yaml"; ignoring it would leave them editing a file with no effect.
	dir := t.TempDir()
	writeFile(t, dir, "domains.yaml", validDomain)

	_, err := config.Load(defaultsFS(), dir, noEnv)
	if err == nil {
		t.Fatal("an unrecognised config file was silently ignored")
	}
	if !strings.Contains(err.Error(), "domains.yaml") {
		t.Errorf("error does not name the stray file: %v", err)
	}
}

func TestLoadIgnoresNonConfigFilesAndDirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# notes")
	if err := os.Mkdir(filepath.Join(dir, "fonts"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := config.Load(defaultsFS(), dir, noEnv); err != nil {
		t.Errorf("Load rejected a directory containing unrelated files: %v", err)
	}
}

func TestLoadExpandsEnvironment(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "app.yaml", strings.Replace(validApp, `addr: ":8080"`, "addr: ${DPI_ADDR}", 1))

	cfg, err := config.Load(defaultsFS(), dir, func(k string) (string, bool) {
		if k == "DPI_ADDR" {
			return ":9999", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.App.Server.Addr != ":9999" {
		t.Errorf("addr = %q, want :9999 from the environment", cfg.App.Server.Addr)
	}
}

func TestLoadReportsUnsetEnvironmentAgainstItsFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "app.yaml", strings.Replace(validApp, `addr: ":8080"`, "addr: ${DPI_ADDR}", 1))

	_, err := config.Load(defaultsFS(), dir, noEnv)
	if err == nil {
		t.Fatal("an unset environment variable was accepted")
	}
	if !strings.Contains(err.Error(), "app.yaml") {
		t.Errorf("error does not attribute the problem to a file: %v", err)
	}
	if !strings.Contains(err.Error(), "DPI_ADDR") {
		t.Errorf("error does not name the variable: %v", err)
	}
}

func TestLoadDefaultsToTheProcessEnvironment(t *testing.T) {
	// Passing nil is the ordinary production call; only tests inject a lookup.
	dir := t.TempDir()
	writeFile(t, dir, "app.yaml", strings.Replace(validApp, `addr: ":8080"`, "addr: ${DPI_TEST_ADDR}", 1))
	t.Setenv("DPI_TEST_ADDR", ":7777")

	cfg, err := config.Load(defaultsFS(), dir, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.App.Server.Addr != ":7777" {
		t.Errorf("addr = %q, want :7777 from the process environment", cfg.App.Server.Addr)
	}
}

func TestLoadFailsOnMissingOverrideDir(t *testing.T) {
	_, err := config.Load(defaultsFS(), filepath.Join(t.TempDir(), "nope"), noEnv)
	if err == nil {
		t.Error("a nonexistent config directory was accepted")
	}
}

func TestLoadFailsOnIncompleteDefaults(t *testing.T) {
	broken := defaultsFS()
	delete(broken, "config/icons.yaml")

	_, err := config.Load(broken, "", noEnv)
	if err == nil {
		t.Fatal("incomplete embedded defaults were accepted")
	}
	if !strings.Contains(err.Error(), "icons.yaml") {
		t.Errorf("error does not name the missing default: %v", err)
	}
}

func TestLoadFailsOnUnreadableOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "brand.yaml")
	writeFile(t, dir, "brand.yaml", validBrand)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Skipf("cannot make a file unreadable here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	if os.Geteuid() == 0 {
		t.Skip("running as root, which can read the file regardless of mode")
	}

	_, err := config.Load(defaultsFS(), dir, noEnv)
	if err == nil {
		t.Error("an unreadable override file was accepted")
	}
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
