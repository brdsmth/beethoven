package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPluginScript runs plugins.sh against a stand-in claude that fails for
// one plugin.
func TestPluginScript(t *testing.T) {
	sh, err := exec.LookPath("dash")
	if err != nil {
		sh = "sh"
	}
	home, bin := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\necho \"$*\" >> \"$HOME/calls\"\n[ \"$3\" != broken ]\n"), 0o755)
	run := func() string {
		t.Helper()
		c := exec.Command(sh, "-c", "set -eu\nphase() { echo \"::phase $*\"; }\n"+pluginScript+"\nplugins_install\n")
		c.Env = []string{"PATH=" + bin + ":" + os.Getenv("PATH"), "HOME=" + home,
			"PLUGINS=compound-engineering@EveryInc/compound-engineering-plugin\n\nbroken@git@github.com:acme/plugins.git"}
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return string(out)
	}
	out := run()
	if !strings.Contains(out, "::phase plugins\n") || !strings.Contains(out, "::warn couldn't install the Claude Code plugin broken@git@github.com:acme/plugins.git\n") {
		t.Errorf("output:\n%s", out)
	}
	// Installed plugins aren't installed again; failed ones are retried.
	run()
	calls, _ := os.ReadFile(filepath.Join(home, "calls"))
	want := "plugin install compound-engineering --marketplace EveryInc/compound-engineering-plugin\n" +
		"plugin install broken --marketplace git@github.com:acme/plugins.git\n" +
		"plugin install broken --marketplace git@github.com:acme/plugins.git\n"
	if string(calls) != want {
		t.Errorf("calls =\n%s\nwant\n%s", calls, want)
	}
}
