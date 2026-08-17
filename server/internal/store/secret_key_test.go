package store

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSecretKeyFilePathPrefersExplicitEnv(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "explicit-key")
	t.Setenv("AGENTBOX_SECRET_KEY_FILE", explicit)
	path, legacy, err := secretKeyFilePath()
	if err != nil {
		t.Fatal(err)
	}
	if path != explicit || legacy {
		t.Fatalf("path = %q legacy = %v, want %q legacy = false", path, legacy, explicit)
	}
}

func TestSecretKeyFilePathDetectsLegacyWorkingDirectoryFile(t *testing.T) {
	t.Setenv("AGENTBOX_SECRET_KEY_FILE", "")
	t.Chdir(t.TempDir())
	if err := os.WriteFile(".agentbox-secret-key", []byte("key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, legacy, err := secretKeyFilePath()
	if err != nil {
		t.Fatal(err)
	}
	if !legacy || path != ".agentbox-secret-key" {
		t.Fatalf("path = %q legacy = %v, want legacy working-directory file", path, legacy)
	}
}

func TestSecretKeyFilePathFallsBackToUserConfigDir(t *testing.T) {
	t.Setenv("AGENTBOX_SECRET_KEY_FILE", "")
	t.Chdir(t.TempDir()) // no legacy file in this working directory
	root := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("AppData", root)
	case "darwin":
		t.Setenv("HOME", root)
	default:
		t.Setenv("XDG_CONFIG_HOME", root)
	}
	path, legacy, err := secretKeyFilePath()
	if err != nil {
		t.Fatal(err)
	}
	if legacy {
		t.Fatal("user config dir fallback must not be flagged as legacy")
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("path = %q, want absolute", path)
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(configDir, "agentbox", "secret-key")
	if path != expected {
		t.Fatalf("path = %q, want %q", path, expected)
	}
}

func TestLoadSecretKeyPersistsAndReloads(t *testing.T) {
	t.Setenv("AGENTBOX_SECRET_KEY", "")
	path := filepath.Join(t.TempDir(), "agentbox", "secret-key")
	t.Setenv("AGENTBOX_SECRET_KEY_FILE", path)
	key, err := loadSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 {
		t.Fatalf("key length = %d, want 32", len(key))
	}
	again, err := loadSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, again) {
		t.Fatal("reloaded key differs from generated key")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %o, want 600", info.Mode().Perm())
	}
}

func TestLoadSecretKeyReadsLegacyWorkingDirectoryFile(t *testing.T) {
	t.Setenv("AGENTBOX_SECRET_KEY", "")
	t.Setenv("AGENTBOX_SECRET_KEY_FILE", "")
	t.Chdir(t.TempDir())
	expected := bytes.Repeat([]byte{5}, 32)
	if err := os.WriteFile(".agentbox-secret-key",
		[]byte(base64.RawStdEncoding.EncodeToString(expected)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := loadSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, expected) {
		t.Fatal("legacy working-directory key was not loaded")
	}
}

func TestLoadSecretKeyFromEnvironment(t *testing.T) {
	expected := bytes.Repeat([]byte{9}, 32)
	t.Setenv("AGENTBOX_SECRET_KEY", base64.StdEncoding.EncodeToString(expected))
	key, err := loadSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, expected) {
		t.Fatal("environment key mismatch")
	}
}

func TestLoadSecretKeyRejectsInvalidEnvironmentValue(t *testing.T) {
	t.Setenv("AGENTBOX_SECRET_KEY", "not-a-valid-key")
	if _, err := loadSecretKey(); err == nil {
		t.Fatal("expected an error for an invalid AGENTBOX_SECRET_KEY")
	}
}
