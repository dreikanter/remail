package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run executes the CLI in process and returns stdout and the error from
// Execute's point of view.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()

	root := newRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)

	err := root.Execute()
	return out.String(), err
}

// sandbox prepares a home with only Claude installed, and returns where its
// copy of the skill belongs.
func sandbox(t *testing.T) string {
	t.Helper()
	home := sandboxHome(t, ".claude/skills")
	return filepath.Join(home, ".claude", "skills", skillDirName, "SKILL.md")
}

// sandboxHome points HOME at a temp dir and creates the given skills
// directories inside it, so installs never touch the real home directory.
func sandboxHome(t *testing.T, dirs ...string) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	// Neither override may leak in from the developer's own environment.
	t.Setenv("CODEX_HOME", "")
	t.Setenv("PI_CODING_AGENT_DIR", "")

	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestSkillPrintsDocument(t *testing.T) {
	out, err := run(t, "skill")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(out, "---\nname: remail\ndescription: ") {
		t.Errorf("document does not start with the expected frontmatter:\n%.120s", out)
	}
	if !strings.Contains(out, "\n---\n\n") {
		t.Error("frontmatter is not terminated")
	}
	if !strings.HasSuffix(out, "\n") {
		t.Error("document does not end with a newline")
	}

	// The document is only useful if it covers the read path an agent needs.
	for _, want := range []string{"### list", "### read", "### files", "### sync", "--json", `"error"`} {
		if !strings.Contains(out, want) {
			t.Errorf("document is missing %q", want)
		}
	}
}

// Every command must appear in the skill, or an agent will not know it exists.
func TestSkillCoversEveryCommand(t *testing.T) {
	out, err := run(t, "skill")
	if err != nil {
		t.Fatal(err)
	}

	for _, cmd := range newRoot().Commands() {
		if cmd.Hidden {
			continue
		}
		if !strings.Contains(out, "remail "+cmd.Name()) {
			t.Errorf("skill document never mentions the %q command", cmd.Name())
		}
	}
}

func TestSkillIsDeterministic(t *testing.T) {
	first, err := run(t, "skill")
	if err != nil {
		t.Fatal(err)
	}
	second, err := run(t, "skill")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Error("two invocations produced different output")
	}
}

func TestSkillJSONShape(t *testing.T) {
	out, err := run(t, "skill", "--json")
	if err != nil {
		t.Fatal(err)
	}

	var got Skill
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if got.Name != "remail" {
		t.Errorf("name = %q", got.Name)
	}
	if !strings.HasPrefix(got.Description, "Use when ") {
		t.Errorf("description should say when to use the skill, got %q", got.Description)
	}
	// body is the document without frontmatter.
	if strings.HasPrefix(got.Body, "---") || got.Body == "" {
		t.Error("body should be the document with frontmatter removed")
	}
}

func TestSkillInstallCreates(t *testing.T) {
	path := sandbox(t)

	out, err := run(t, "skill", "--install")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "claude") || !strings.Contains(out, "create") {
		t.Errorf("output = %q, want a create action for claude", out)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("skill was not installed: %v", err)
	}

	// The strongest guarantee this command makes: what lands on disk is exactly
	// what `remail skill` prints.
	stdout, err := run(t, "skill")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != stdout {
		t.Error("installed file differs from `remail skill` output")
	}
}

// Re-installing an unchanged skill must not rewrite the file.
func TestSkillInstallSkipsIdentical(t *testing.T) {
	path := sandbox(t)

	if _, err := run(t, "skill", "--install"); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "skill", "--install")
	if err != nil {
		t.Fatalf("skip should exit zero, got %v", err)
	}
	// The reason is the point: "skip" alone leaves the user guessing.
	if !strings.Contains(out, "skip") || !strings.Contains(out, "already up to date") {
		t.Errorf("output = %q, want skip to say why", out)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("skip rewrote the file")
	}
}

func TestSkillInstallConflictsWithoutForce(t *testing.T) {
	path := sandbox(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("hand-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "skill", "--install")
	if err == nil {
		t.Error("a conflict should fail the command")
	}
	if !strings.Contains(out, "conflict") || !strings.Contains(out, "--force") {
		t.Errorf("output = %q, want conflict to name the way out", out)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hand-edited\n" {
		t.Error("conflict overwrote the existing file")
	}
}

func TestSkillInstallForceOverwrites(t *testing.T) {
	path := sandbox(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "skill", "--install", "--force")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "overwrite") {
		t.Errorf("output = %q, want an overwrite action", out)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == "stale\n" {
		t.Error("--force did not replace the file")
	}
}

// A dry run must report exactly what a real run would do, and write nothing.
func TestSkillInstallDryRun(t *testing.T) {
	path := sandbox(t)

	dry, err := run(t, "skill", "--install", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("dry run wrote the file")
	}

	real, err := run(t, "skill", "--install")
	if err != nil {
		t.Fatal(err)
	}
	if dry != real {
		t.Errorf("dry run reported %q but the real run reported %q", dry, real)
	}
}

func TestSkillInstallUnknownAgent(t *testing.T) {
	sandbox(t)

	if _, err := run(t, "skill", "--install", "--agent", "emacs"); err == nil {
		t.Error("unknown agent should be an error")
	} else if !strings.Contains(err.Error(), "supported") {
		t.Errorf("error = %q, should list supported agents", err)
	}
}

func TestSkillInstallNoAgentDetected(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no ~/.claude/skills

	_, err := run(t, "skill", "--install")
	if err == nil {
		t.Fatal("install with no detectable agent should fail")
	}
	if !strings.Contains(err.Error(), "--agent") {
		t.Errorf("error = %q, should suggest passing --agent", err)
	}
}

// Naming an agent whose skills directory is absent is reported per target, and
// must not create the agent's own directory.
func TestSkillInstallMissingSkillsDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	out, err := run(t, "skill", "--install", "--agent", "claude")
	if err == nil {
		t.Error("a missing skills directory should fail the command")
	}
	if !strings.Contains(out, "no skills directory") {
		t.Errorf("output = %q, want a per-target error", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("install created the agent's own directory")
	}
}

func TestSkillInstallJSON(t *testing.T) {
	sandbox(t)

	out, err := run(t, "skill", "--install", "--json")
	if err != nil {
		t.Fatal(err)
	}

	var got struct {
		Actions []installAction `json:"actions"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if len(got.Actions) != 1 {
		t.Fatalf("actions = %v, want one", got.Actions)
	}
	if got.Actions[0].Action != "create" || got.Actions[0].Agent != "claude" {
		t.Errorf("action = %+v", got.Actions[0])
	}
	if !filepath.IsAbs(got.Actions[0].Path) {
		t.Errorf("path %q should be absolute", got.Actions[0].Path)
	}
}

func TestSkillRejectsInstallOnlyFlags(t *testing.T) {
	for _, args := range [][]string{
		{"skill", "--agent", "claude"},
		{"skill", "--force"},
	} {
		if _, err := run(t, args...); err == nil {
			t.Errorf("%v should require --install", args)
		}
	}
}

// Auto-detection installs to every agent it finds.
func TestSkillInstallDetectsMultipleAgents(t *testing.T) {
	home := sandboxHome(t, ".claude/skills", ".codex/skills")

	out, err := run(t, "skill", "--install")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "create") != 2 {
		t.Errorf("output = %q, want one create per detected agent", out)
	}
	for _, rel := range []string{".claude/skills", ".codex/skills"} {
		path := filepath.Join(home, filepath.FromSlash(rel), skillDirName, "SKILL.md")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("not installed at %s: %v", rel, err)
		}
	}
}

// Codex and pi read the shared ~/.agents/skills directory as well as their own.
// Auto-detection must not hand them the same skill twice.
func TestSkillInstallPrefersSharedDir(t *testing.T) {
	home := sandboxHome(t, ".agents/skills", ".codex/skills", ".pi/agent/skills")

	out, err := run(t, "skill", "--install")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(out, "codex") || strings.Contains(out, "pi ") {
		t.Errorf("output = %q, want codex and pi covered by the shared directory", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", skillDirName, "SKILL.md")); err != nil {
		t.Errorf("shared directory not installed: %v", err)
	}
	for _, rel := range []string{".codex/skills", ".pi/agent/skills"} {
		path := filepath.Join(home, filepath.FromSlash(rel), skillDirName, "SKILL.md")
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("duplicate copy written to %s", rel)
		}
	}
}

// Naming an agent explicitly still uses its own directory.
func TestSkillInstallExplicitAgentIgnoresSharedDir(t *testing.T) {
	home := sandboxHome(t, ".agents/skills", ".codex/skills")

	if _, err := run(t, "skill", "--install", "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", skillDirName, "SKILL.md")); err != nil {
		t.Errorf("explicit --agent codex did not install to ~/.codex: %v", err)
	}
}

// Both agents let the user relocate their configuration directory.
func TestSkillInstallHonorsEnvOverrides(t *testing.T) {
	cases := []struct {
		agent, env string
	}{
		{"codex", "CODEX_HOME"},
		{"pi", "PI_CODING_AGENT_DIR"},
	}

	for _, c := range cases {
		t.Run(c.agent, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			root := t.TempDir()
			t.Setenv(c.env, root)
			if err := os.MkdirAll(filepath.Join(root, "skills"), 0o755); err != nil {
				t.Fatal(err)
			}

			if _, err := run(t, "skill", "--install", "--agent", c.agent); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, "skills", skillDirName, "SKILL.md")); err != nil {
				t.Errorf("%s did not follow %s: %v", c.agent, c.env, err)
			}
		})
	}
}

// pi keeps its configuration under ~/.pi/agent; ~/.agent is not a path it uses.
func TestSkillPiUsesNestedAgentDir(t *testing.T) {
	home := sandboxHome(t, ".pi/agent/skills")

	if _, err := run(t, "skill", "--install", "--agent", "pi"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", "skills", skillDirName, "SKILL.md")); err != nil {
		t.Errorf("pi skill not installed under ~/.pi/agent: %v", err)
	}
}
