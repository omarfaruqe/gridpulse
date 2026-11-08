package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	for _, k := range []string{"GP_TEST_PLAIN", "GP_TEST_QUOTED", "GP_TEST_EXPORTED", "GP_TEST_EXISTING"} {
		os.Unsetenv(k)
		t.Cleanup(func() { os.Unsetenv(k) })
	}
	t.Setenv("GP_TEST_EXISTING", "from-shell")

	path := filepath.Join(t.TempDir(), ".env")
	content := "# a comment\n\nGP_TEST_PLAIN=abc123\nGP_TEST_QUOTED=\"with space\"\nexport GP_TEST_EXPORTED=yes\nGP_TEST_EXISTING=from-file\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"GP_TEST_PLAIN":    "abc123",
		"GP_TEST_QUOTED":   "with space",
		"GP_TEST_EXPORTED": "yes",
		"GP_TEST_EXISTING": "from-shell", // the real environment wins
	} {
		if got := os.Getenv(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestLoadDotEnvMissingFileIsFine(t *testing.T) {
	if err := LoadDotEnv(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Errorf("missing file should not be an error, got %v", err)
	}
}

func TestLoadDotEnvBadLineDoesNotLeakContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("this-is-a-secret-without-equals\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := LoadDotEnv(path)
	if err == nil {
		t.Fatal("expected error for a line without '='")
	}
	if got := err.Error(); strings.Contains(got, "this-is-a-secret") {
		t.Errorf("error leaks file contents: %q", got)
	}
}
