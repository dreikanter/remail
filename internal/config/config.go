// Package config loads the per-directory remail.json file and resolves the
// account password.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Name is the config file stored in the mail directory.
const Name = "remail.json"

// PasswordEnv overrides pass_cmd when set. Intended for headless and scheduled
// runs where a keychain prompt would block with no way to answer it.
const PasswordEnv = "REMAIL_PASSWORD"

// Config is the on-disk remail.json. Only account and pass_cmd are required;
// everything else is filled in from the provider preset.
type Config struct {
	Provider  string `json:"provider,omitempty"`
	Account   string `json:"account"`
	PassCmd   string `json:"pass_cmd"`
	SinceDays int    `json:"since_days,omitempty"`

	// Escape hatches for non-preset servers. Omitted from a generated config.
	Host    string `json:"host,omitempty"`
	Port    int    `json:"port,omitempty"`
	Mailbox string `json:"mailbox,omitempty"`
}

type preset struct {
	host string
	port int
}

var presets = map[string]preset{
	"gmail":    {host: "imap.gmail.com", port: 993},
	"fastmail": {host: "imap.fastmail.com", port: 993},
	"icloud":   {host: "imap.mail.me.com", port: 993},
}

// Providers lists the known preset names, sorted for stable help output.
func Providers() []string {
	return []string{"fastmail", "gmail", "icloud"}
}

// Path returns the config file path for a mail directory.
func Path(dir string) string { return filepath.Join(dir, Name) }

// Load reads and validates remail.json from dir, applying provider defaults.
func Load(dir string) (*Config, error) {
	path := Path(dir)
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no %s in %s: run 'remail init' first", Name, dir)
		}
		return nil, err
	}
	// pass_cmd is executed, so a config anyone can edit is a code-execution
	// vector. Same reasoning as ssh refusing a group-writable private key.
	if mode := info.Mode().Perm(); mode&0o022 != 0 {
		return nil, fmt.Errorf("%s is writable by group or others (%#o): chmod 600 it", path, mode)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.applyDefaults(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) applyDefaults() error {
	if c.Provider != "" {
		p, ok := presets[c.Provider]
		if !ok {
			return fmt.Errorf("unknown provider %q (known: %s)", c.Provider, strings.Join(Providers(), ", "))
		}
		if c.Host == "" {
			c.Host = p.host
		}
		if c.Port == 0 {
			c.Port = p.port
		}
	}
	if c.Mailbox == "" {
		c.Mailbox = "INBOX"
	}
	if c.Port == 0 {
		c.Port = 993
	}

	if c.Account == "" {
		return errors.New("account is required")
	}
	if c.Host == "" {
		return errors.New("host is required when provider is not set")
	}
	if c.PassCmd == "" && os.Getenv(PasswordEnv) == "" {
		return fmt.Errorf("pass_cmd is required (or set %s)", PasswordEnv)
	}
	if c.SinceDays < 0 {
		return errors.New("since_days cannot be negative")
	}
	return nil
}

// Addr is the IMAP dial target.
func (c *Config) Addr() string { return fmt.Sprintf("%s:%d", c.Host, c.Port) }

// Save writes the config to dir. It does not overwrite an existing file.
func (c *Config) Save(dir string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	f, err := os.OpenFile(Path(dir), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // close error surfaced by the write below
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Close()
}

// Password resolves the account password: the REMAIL_PASSWORD environment
// variable when set, otherwise the output of pass_cmd.
//
// pass_cmd runs through "sh -c" so ordinary shell syntax works. That makes
// remail.json executable configuration, which is why Load refuses to read a
// config that is writable by group or others.
func (c *Config) Password() (string, error) {
	if v := os.Getenv(PasswordEnv); v != "" {
		return v, nil
	}

	cmd := exec.Command("sh", "-c", c.PassCmd)
	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("pass_cmd failed: %s", msg)
		}
		return "", fmt.Errorf("pass_cmd failed: %w", err)
	}

	// `security find-generic-password -w` and most password managers emit a
	// trailing newline. Sending it as part of the password fails authentication
	// with no useful diagnostic, so trim it.
	pw := strings.Trim(string(out), " \t\r\n")
	if pw == "" {
		return "", errors.New("pass_cmd produced no output")
	}
	return pw, nil
}
