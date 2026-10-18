package session

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brdsmth/beethoven/internal/capability"
)

// TestCapabilityScript runs capabilities.sh with a made-up catalog, in dash
// (the sandbox's sh) when it is installed.
func TestCapabilityScript(t *testing.T) {
	sh, err := exec.LookPath("dash")
	if err != nil {
		sh = "sh"
	}
	home, ws := t.TempDir(), t.TempDir()
	for _, d := range []string{"app/.beethoven", "lib"} {
		os.MkdirAll(filepath.Join(ws, d), 0o755)
	}
	os.WriteFile(filepath.Join(ws, "app/.beethoven/capabilities"), []byte("three  # its build needs it\n\nnope one\n"), 0o644)
	os.WriteFile(filepath.Join(ws, "app/marker-four"), nil, 0o644)
	catalog := capability.ArchiveOf([]capability.Capability{
		{ID: "one", Name: "One", Install: `echo one >> "$HOME/installs" && mkdir -p "$HOME/one" && touch "$HOME/one/bin"`, Paths: []string{"one"},
			Detect: "false", Skill: "Use one.",
			Secrets: []capability.Secret{{Name: "ONE_TOKEN", Required: true, Why: "for one"}}, Env: map[string]string{"ONE_HOME": "$HOME/one"}},
		{ID: "two", Name: "Two", Install: "exit 3", Detect: "false", Skill: "Use two."},
		{ID: "three", Name: "Three", Install: `echo three >> "$HOME/installs"`, Prepare: "pwd > prepared", Detect: "false", Skill: "Use three."},
		{ID: "four", Name: "Four", Install: "true", Detect: "[ -f marker-four ]", Skill: "Use four."},
	})
	run := func(cmd string, env ...string) string {
		t.Helper()
		c := exec.Command(sh, "-c", "set -eu\n"+capabilityScript+"\ncap_unpack\n"+cmd+"\n")
		c.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "WORKSPACE=" + ws, "REPOS=app - -\nlib - -"}, env...)
		c.Stdin = bytes.NewReader(catalog)
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", cmd, err, out)
		}
		return string(out)
	}
	has := func(out string, lines ...string) {
		t.Helper()
		for _, l := range lines {
			if !strings.Contains("\n"+out, "\n"+l+"\n") {
				t.Errorf("output lacks %q:\n%s", l, out)
			}
		}
	}
	read := func(path string) string { b, _ := os.ReadFile(path); return string(b) }

	// Detected capabilities are installed too; dependencies come after.
	out := run("cap_setup\ncap_prepare", "CAPABILITIES=one two", "CAP_EXPORT=1")
	has(out, "::capability one", "::env ONE_HOME="+home+"/one", "::warn the One capability needs ONE_TOKEN, for one: add it as a secret",
		"::warn the Two capability failed to install", "::capability three", `::warn unknown capability "nope"; see Settings for the capabilities available`,
		"::repo app three", "::repo app four", "::capability four")
	if strings.Contains(out, "::capability two") || strings.Count(out, "::capability one") != 1 {
		t.Errorf("two failed but is reported, or one twice:\n%s", out)
	}
	if got := read(filepath.Join(home, "installs")); got != "one\nthree\n" {
		t.Errorf("installs = %q", got)
	}
	for _, repo := range []string{"app", "lib"} {
		if got := strings.TrimSpace(read(filepath.Join(ws, repo, "prepared"))); !strings.HasSuffix(got, "/"+repo) {
			t.Errorf("three prepared %s in %q", repo, got)
		}
	}
	// The fresh install of one, which has paths, was packed for the server.
	packed := filepath.Join(home, ".beethoven/export/one.tar.gz")
	if !strings.Contains(out, "::cache one ") || !strings.Contains(out, " "+packed+"\n") {
		t.Errorf("one wasn't packed:\n%s", out)
	}
	if list, err := exec.Command("tar", "-tzf", packed).Output(); err != nil || !strings.Contains(string(list), "one/bin") {
		t.Errorf("packed %q, %v", list, err)
	}
	if skill := read(filepath.Join(home, ".claude/skills/one/SKILL.md")); !strings.HasPrefix(skill, "---\nname: one\n") {
		t.Errorf("skill = %q", skill)
	}

	// Installed once per sandbox; secrets that are set don't warn.
	out = run("cap_setup\ncap_prepare", "CAPABILITIES=one", "ONE_TOKEN=x")
	if got := read(filepath.Join(home, "installs")); got != "one\nthree\n" || strings.Contains(out, "::phase capability one") ||
		strings.Contains(out, "needs ONE_TOKEN") {
		t.Errorf("second boot reinstalled or warned: installs %q\n%s", got, out)
	}
	has(out, "::capability one", "::capability three")

	// Added during a session: installed and recorded once in the file.
	record := filepath.Join(ws, "lib/.beethoven/capabilities")
	for range 2 {
		has(run("cap_add", "CAPABILITIES=four", "CAP_RECORD="+record), "::capability four", "::recorded "+record)
	}
	if got := read(record); got != "four\n" {
		t.Errorf("recorded %q", got)
	}
	if out := run("cap_add", "CAPABILITIES=two", "CAP_RECORD="+record); strings.Contains(out, "::recorded") {
		t.Errorf("recorded a capability that failed:\n%s", out)
	}
	has(run("cap_detect"), "::suggest four")
}
