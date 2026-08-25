package cli

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/dreikanter/remail/internal/atomicfile"
)

//go:embed skill.md
var skillDoc string

const skillJSONShape = `JSON shape when printing:

  {"name": "remail", "description": "...", "body": "...markdown without frontmatter..."}

JSON shape with --install:

  {"actions": [{"agent": "claude", "path": "/abs/SKILL.md",
                "action": "create|overwrite|skip|conflict", "error": "..."}]}`

// Skill is the agent-facing document describing this CLI.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`

	// raw is the embedded file as written. Markdown returns it verbatim rather
	// than reassembling it, so the installed file, the source, and stdout
	// cannot drift apart.
	raw string
}

// Markdown returns the full document, frontmatter included.
func (s Skill) Markdown() string { return s.raw }

// loadSkill parses the embedded document. A malformed one is a build mistake,
// not a runtime condition, so it panics.
func loadSkill() Skill {
	const open, close = "---\n", "\n---\n"

	if !strings.HasPrefix(skillDoc, open) {
		panic("skill.md: missing opening frontmatter delimiter")
	}
	rest := skillDoc[len(open):]
	before, after, ok := strings.Cut(rest, close)
	if !ok {
		panic("skill.md: missing closing frontmatter delimiter")
	}

	var meta struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(before), &meta); err != nil {
		panic("skill.md: invalid frontmatter: " + err.Error())
	}
	if meta.Name == "" || meta.Description == "" {
		panic("skill.md: frontmatter needs both name and description")
	}

	return Skill{
		Name:        meta.Name,
		Description: meta.Description,
		Body:        strings.TrimPrefix(after, "\n"),
		raw:         skillDoc,
	}
}

const skillDirName = "remail"

// sharedAgent is the cross-client skills directory. Codex, pi, and others read
// it in addition to their own, so one copy there serves all of them.
const sharedAgent = "agents"

// agentTarget is one agent this skill can be installed for. SkillsDir is a
// function so an agent with a different layout, or an environment override, can
// still be added.
type agentTarget struct {
	Name      string
	SkillsDir func() (string, error)

	// SharesAgentsDir marks an agent that also reads the shared directory.
	// Installing to both would register the same skill with it twice.
	SharesAgentsDir bool
}

// homeAgent describes an agent that reads <home>/<dir>/skills/<name>/SKILL.md,
// which is the layout every supported agent uses today.
func homeAgent(name, dir string) agentTarget {
	return agentTarget{
		Name: name,
		SkillsDir: func() (string, error) {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("resolve home directory: %w", err)
			}
			return filepath.Join(home, dir, "skills"), nil
		},
	}
}

// PathFor is where this agent's copy of the skill belongs.
func (a agentTarget) PathFor() (string, error) {
	dir, err := a.SkillsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, skillDirName, "SKILL.md"), nil
}

// Detect reports whether the agent's skills directory exists.
func (a agentTarget) Detect() (bool, error) {
	dir, err := a.SkillsDir()
	if err != nil {
		return false, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", dir, err)
	}
	return info.IsDir(), nil
}

// envDir resolves a skills directory from an environment variable, falling back
// to a path under the home directory. Both Codex and pi let the user relocate
// their configuration, so neither can be hardcoded.
func envDir(env string, fallback ...string) func() (string, error) {
	return func() (string, error) {
		if v := os.Getenv(env); v != "" {
			return filepath.Join(v, "skills"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		return filepath.Join(home, filepath.Join(fallback...), "skills"), nil
	}
}

var agents = []agentTarget{
	homeAgent("claude", ".claude"),

	// Codex still reads $CODEX_HOME/skills, but treats it as superseded.
	{Name: "codex", SkillsDir: envDir("CODEX_HOME", ".codex"), SharesAgentsDir: true},

	// pi keeps its agent configuration under ~/.pi/agent, not ~/.agent.
	{Name: "pi", SkillsDir: envDir("PI_CODING_AGENT_DIR", ".pi", "agent"), SharesAgentsDir: true},

	homeAgent(sharedAgent, ".agents"),
}

// installAction is what happened, or would happen, for one agent.
//
//   - create    — nothing was there; the file is written
//   - overwrite — content differed and --force was given
//   - skip      — content is already byte-identical; the file is left alone
//   - conflict  — content differs and --force was not given
type installAction struct {
	Agent  string `json:"agent"`
	Path   string `json:"path"`
	Action string `json:"action,omitempty"`
	Error  string `json:"error,omitempty"`
}

func newSkill() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill [options]",
		Short: "print or install the agent skill for this CLI",
		Long: `Prints a self-contained Markdown document that teaches an agent to read a
mailbox with remail.

With --install it is written into a detected agent's skills directory
instead. An existing file with identical content is reported as skip and
left untouched; one that differs is a conflict unless --force is given.

` + skillJSONShape,
		Example: `  remail skill
  remail skill --install
  remail skill --install --agent claude
  remail skill --install --dry-run`,
		Args: cobra.NoArgs,
		RunE: runSkill,
	}
	cmd.Flags().Bool("install", false, "write the skill into an agent's skills directory")
	cmd.Flags().String("agent", "", "install for one agent (supported: "+agentNames()+")")
	cmd.Flags().Bool("force", false, "overwrite an existing skill whose content differs")
	cmd.Flags().BoolP("dry-run", "n", false, "preview install actions without writing")
	return cmd
}

func runSkill(cmd *cobra.Command, _ []string) error {
	install, _ := cmd.Flags().GetBool("install")
	agent, _ := cmd.Flags().GetString("agent")
	force, _ := cmd.Flags().GetBool("force")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	if !install && (agent != "" || force) {
		return errors.New("--agent and --force only apply with --install")
	}

	skill := loadSkill()
	if !install {
		if jsonMode(cmd) {
			return emitJSON(out(cmd), skill)
		}
		_, err := fmt.Fprint(out(cmd), skill.Markdown())
		return err
	}

	targets, err := resolveTargets(agent)
	if err != nil {
		return err
	}

	actions := planInstall(targets, skill.Markdown(), force)
	if !dryRun {
		applyInstall(actions, skill.Markdown())
	}

	if err := reportInstall(cmd, actions); err != nil {
		return err
	}
	if installFailed(actions) {
		// The per-action lines above already say what went wrong.
		return errSilent
	}
	return nil
}

func resolveTargets(agent string) ([]agentTarget, error) {
	if agent != "" {
		for _, a := range agents {
			if a.Name == agent {
				return []agentTarget{a}, nil
			}
		}
		return nil, fmt.Errorf("unknown agent %q (supported: %s)", agent, agentNames())
	}

	var detected []agentTarget
	for _, a := range agents {
		ok, err := a.Detect()
		if err != nil {
			return nil, err
		}
		if ok {
			detected = append(detected, a)
		}
	}
	if len(detected) == 0 {
		return nil, fmt.Errorf("no supported agent found; pass --agent (supported: %s)", agentNames())
	}
	return dropCoveredBySharedDir(detected), nil
}

// dropCoveredBySharedDir keeps auto-detection from installing two copies that
// one agent would both load. Naming an agent explicitly still uses its own
// directory.
func dropCoveredBySharedDir(detected []agentTarget) []agentTarget {
	var hasShared bool
	for _, a := range detected {
		if a.Name == sharedAgent {
			hasShared = true
		}
	}
	if !hasShared {
		return detected
	}

	kept := detected[:0]
	for _, a := range detected {
		if !a.SharesAgentsDir {
			kept = append(kept, a)
		}
	}
	return kept
}

// planInstall decides what to do without touching the filesystem, so a dry run
// reports exactly what a real run would do.
func planInstall(targets []agentTarget, doc string, force bool) []installAction {
	actions := make([]installAction, 0, len(targets))
	for _, target := range targets {
		actions = append(actions, planOne(target, doc, force))
	}
	return actions
}

func planOne(target agentTarget, doc string, force bool) installAction {
	action := installAction{Agent: target.Name}

	path, err := target.PathFor()
	if err != nil {
		action.Error = err.Error()
		return action
	}
	action.Path = path

	// Creating the agent's own skills directory would invent a configuration
	// for a tool the user may not have, so a missing one is reported.
	skillsDir := filepath.Dir(filepath.Dir(path))
	if info, err := os.Stat(skillsDir); err != nil || !info.IsDir() {
		action.Error = fmt.Sprintf("no skills directory at %s", skillsDir)
		return action
	}

	existing, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		action.Action = "create"
	case err != nil:
		action.Error = err.Error()
	case bytes.Equal(existing, []byte(doc)):
		action.Action = "skip"
	case force:
		action.Action = "overwrite"
	default:
		action.Action = "conflict"
	}
	return action
}

func applyInstall(actions []installAction, doc string) {
	for i := range actions {
		switch actions[i].Action {
		case "create", "overwrite":
			if err := atomicfile.Write(actions[i].Path, []byte(doc), 0o644); err != nil {
				actions[i].Error = err.Error()
			}
		}
	}
}

func reportInstall(cmd *cobra.Command, actions []installAction) error {
	if jsonMode(cmd) {
		return emitJSON(out(cmd), struct {
			Actions []installAction `json:"actions"`
		}{actions})
	}

	width := 0
	for _, a := range actions {
		if n := len(a.Agent); n > width {
			width = n
		}
	}

	for _, a := range actions {
		verb, reason := describe(a)
		fmt.Fprintf(out(cmd), "%-*s  %-9s %s", width, a.Agent, verb, shortenHome(a.Path))
		if reason != "" {
			fmt.Fprintf(out(cmd), "  (%s)", reason)
		}
		fmt.Fprintln(out(cmd))
	}
	return nil
}

// describe pairs an action with why it happened. Without the reason, "skip" and
// "conflict" leave the user guessing at what the command decided.
//
// The wording stays tense-neutral so a dry run reads the same as a real one.
func describe(a installAction) (verb, reason string) {
	switch {
	case a.Error != "":
		return "error", a.Error
	case a.Action == "skip":
		return a.Action, "already up to date"
	case a.Action == "overwrite":
		return a.Action, "replacing local changes"
	case a.Action == "conflict":
		return a.Action, "edited locally, pass --force to replace"
	default:
		return a.Action, ""
	}
}

// shortenHome trims the home directory to ~ so a long install path does not
// crowd out the reason beside it.
func shortenHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return path
}

func installFailed(actions []installAction) bool {
	for _, a := range actions {
		if a.Error != "" || a.Action == "conflict" {
			return true
		}
	}
	return false
}

func agentNames() string {
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		names = append(names, a.Name)
	}
	return strings.Join(names, ", ")
}
