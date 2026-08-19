package config

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

	got, err := cfg.Password(t.Context())
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

	got, err := cfg.Password(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-env" {
		t.Errorf("Password = %q, want the environment to win", got)
	}
}

func TestPasswordCommandFailureIsReported(t *testing.T) {
	cfg := &Config{PassCmd: "echo 'nope' >&2; exit 1"}

	_, err := cfg.Password(t.Context())
	if err == nil {
		t.Fatal("Password succeeded on a failing command")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error = %q, want the command's stderr included", err)
	}
}

func TestPasswordEmptyOutputIsAnError(t *testing.T) {
	cfg := &Config{PassCmd: "true"}
	if _, err := cfg.Password(t.Context()); err == nil {
		t.Error("Password succeeded with no output, want an error")
	}
}

// A relative path in pass_cmd must resolve against the mailbox, not against
// wherever remail was invoked from.
func TestPasswordCommandRunsInTheMailDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secret"), []byte("hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	write(t, dir, `{"provider":"gmail","account":"a@example.com","pass_cmd":"cat secret"}`, 0o600)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Anywhere but the mail directory, so a cwd-relative read would fail.
	t.Chdir(t.TempDir())

	got, err := cfg.Password(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got != "hunter2" {
		t.Errorf("Password = %q, want pass_cmd to run in the mail directory", got)
	}
}

// A helper that stops to ask the user something must not wedge the caller.
func TestPasswordCommandIsCancellable(t *testing.T) {
	cfg := &Config{PassCmd: "sleep 60"}

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	done := make(chan error, 1)
	go func() { _, err := cfg.Password(ctx); done <- err }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Password succeeded on a cancelled context")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Password ignored the cancelled context")
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
