package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func write(t *testing.T, dir, body string, perm os.FileMode) {
	t.Helper()
	if err := os.WriteFile(Path(dir), []byte(body), perm); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAppliesProviderPreset(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{
	  "provider": "gmail",
	  "account": "a@example.com",
	  "pass_cmd": "echo secret"
	}`, 0o600)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Host != "imap.gmail.com" {
		t.Errorf("Host = %q, want the gmail preset", cfg.Host)
	}
	if cfg.Port != 993 {
		t.Errorf("Port = %d, want 993", cfg.Port)
	}
	if cfg.Mailbox != "INBOX" {
		t.Errorf("Mailbox = %q, want INBOX", cfg.Mailbox)
	}
	if got := cfg.Addr(); got != "imap.gmail.com:993" {
		t.Errorf("Addr = %q", got)
	}
}

func TestLoadRejectsBadConfig(t *testing.T) {
	cases := map[string]string{
		"unknown provider": `{"provider":"hotmail","account":"a@example.com","pass_cmd":"x"}`,
		"no account":       `{"provider":"gmail","pass_cmd":"x"}`,
		"no host":          `{"account":"a@example.com","pass_cmd":"x"}`,
		"negative since":   `{"provider":"gmail","account":"a@example.com","pass_cmd":"x","since_days":-1}`,
		"malformed json":   `{`,
	}

	for name, body := range cases {
		dir := t.TempDir()
		write(t, dir, body, 0o600)
		if _, err := Load(dir); err == nil {
			t.Errorf("%s: Load succeeded, want error", name)
		}
	}
}

func TestLoadMissingConfigMentionsInit(t *testing.T) {
	_, err := Load(t.TempDir())
	if err == nil {
		t.Fatal("Load succeeded on an empty directory")
	}
	if !strings.Contains(err.Error(), "remail init") {
		t.Errorf("error = %q, want it to point at 'remail init'", err)
	}
}

// pass_cmd is executed, so a config other users can edit is a code-execution
// vector and must be refused.
func TestLoadRefusesWorldWritableConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful here")
	}

	dir := t.TempDir()
	write(t, dir, `{"provider":"gmail","account":"a@example.com","pass_cmd":"echo x"}`, 0o600)
	// WriteFile's mode is masked by umask, so set the bits explicitly.
	if err := os.Chmod(Path(dir), 0o666); err != nil {
		t.Fatal(err)
	}

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load accepted a world-writable config")
	}
	if !strings.Contains(err.Error(), "chmod") {
		t.Errorf("error = %q, want it to say how to fix the permissions", err)
	}
}

func TestPasswordFromCommand(t *testing.T) {
	cfg := &Config{PassCmd: "printf 'hunter2\n'"}

	got, err := cfg.Password()
	if err != nil {
		t.Fatal(err)
	}
	// A trailing newline is what `security -w` and most password managers emit;
	// sending it as part of the password fails auth with no useful message.
	if got != "hunter2" {
		t.Errorf("Password = %q, want the trailing newline trimmed", got)
	}
}

func TestPasswordEnvOverridesCommand(t *testing.T) {
	t.Setenv(PasswordEnv, "from-env")
	cfg := &Config{PassCmd: "echo from-command"}

	got, err := cfg.Password()
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-env" {
		t.Errorf("Password = %q, want the environment to win", got)
	}
}

func TestPasswordCommandFailureIsReported(t *testing.T) {
	cfg := &Config{PassCmd: "echo 'nope' >&2; exit 1"}

	_, err := cfg.Password()
	if err == nil {
		t.Fatal("Password succeeded on a failing command")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error = %q, want the command's stderr included", err)
	}
}

func TestPasswordEmptyOutputIsAnError(t *testing.T) {
	cfg := &Config{PassCmd: "true"}
	if _, err := cfg.Password(); err == nil {
		t.Error("Password succeeded with no output, want an error")
	}
}

func TestSaveDoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Provider: "gmail", Account: "a@example.com", PassCmd: "echo x"}

	if err := cfg.Save(dir); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(dir); err == nil {
		t.Error("second Save overwrote an existing config")
	}

	info, err := os.Stat(filepath.Join(dir, Name))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("config written with mode %#o, want owner-only", perm)
	}
}
