// Package capability describes the development toolchains a sandbox can be
// given on demand: a mobile app stack, a language's toolchain, and so on.
// A capability is switched on in the global settings, for an environment,
// in a repository's .beethoven/capabilities file, or by the agent during a
// session, and the sandbox installs it when it boots. Each one brings
// instructions for the agent as a Claude Code skill.
package capability

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Secret is a variable a capability uses, set as a secret.
type Secret struct {
	Name     string `json:"name"`
	Required bool   `json:"required"` // without it the capability is of little use
	Why      string `json:"why"`
}

// Capability is one installable toolchain with what the agent needs to know
// to use it.
type Capability struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Install     string   `json:"install"`           // installs the toolchain, once per sandbox; runs in $HOME
	Prepare     string   `json:"prepare,omitempty"` // installs a project's dependencies, once per sandbox and repository
	Detect      string   `json:"-"`                 // a shell test, run in a repository: true if it looks like it needs this
	Skill       string   `json:"-"`                 // the skill's body, after its frontmatter
	Secrets     []Secret `json:"secrets"`
	// Env is set for the agent once the capability is installed. Values may
	// refer to $HOME, which the sandbox expands.
	Env      map[string]string `json:"env,omitempty"`
	CPUs     float64           `json:"cpus,omitempty"`      // the least it builds well with
	MemoryMB int               `json:"memory_mb,omitempty"` // likewise
}

// Hash identifies the capability's scripts: a sandbox installs it again
// when they change.
func (c Capability) Hash() string {
	h := sha256.Sum256([]byte(c.Install + "\x00" + c.Prepare))
	return hex.EncodeToString(h[:6])
}

// SkillFile is the skill as Claude Code reads it from
// ~/.claude/skills/<id>/SKILL.md.
func (c Capability) SkillFile() string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s", c.ID, c.Description, strings.TrimSpace(c.Skill)+"\n")
}

// Get returns the capability with id.
func Get(id string) (Capability, bool) {
	i := slices.IndexFunc(Catalog, func(c Capability) bool { return c.ID == id })
	if i < 0 {
		return Capability{}, false
	}
	return Catalog[i], true
}

// Unknown returns the ids in list that the catalog lacks.
func Unknown(list []string) []string {
	var out []string
	for _, id := range list {
		if _, ok := Get(id); !ok {
			out = append(out, id)
		}
	}
	return out
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Union joins lists in order, without repeats.
func Union(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		for _, id := range l {
			if id = strings.TrimSpace(id); id != "" && !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out
}

// Resources raises the CPU and memory limits a sandbox is created with to
// what ids need. A limit of 0 means the provider's default, which is left
// alone: on Docker that is no limit at all.
func Resources(ids []string, cpus float64, memoryMB int) (float64, int) {
	for _, id := range ids {
		c, ok := Get(id)
		if !ok {
			continue
		}
		if cpus > 0 && cpus < c.CPUs {
			cpus = c.CPUs
		}
		if memoryMB > 0 && memoryMB < c.MemoryMB {
			memoryMB = c.MemoryMB
		}
	}
	return cpus, memoryMB
}

// Archive is the catalog as the sandbox reads it: a tar archive holding a
// directory per capability with its name, scripts, skill, secrets and
// variables.
func Archive() []byte { return ArchiveOf(Catalog) }

// ArchiveOf archives capabilities as Archive does.
func ArchiveOf(list []Capability) []byte {
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	add := func(name, body string, mode int64) {
		w.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body)), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg})
		w.Write([]byte(body))
	}
	for _, c := range list {
		var secrets strings.Builder
		for _, s := range c.Secrets {
			need := "optional"
			if s.Required {
				need = "required"
			}
			fmt.Fprintf(&secrets, "%s %s %s\n", s.Name, need, s.Why)
		}
		add(c.ID+"/name", c.Name+"\n", 0o644)
		add(c.ID+"/hash", c.Hash()+"\n", 0o644)
		add(c.ID+"/install.sh", c.Install+"\n", 0o755)
		if c.Prepare != "" {
			add(c.ID+"/prepare.sh", c.Prepare+"\n", 0o755)
		}
		add(c.ID+"/detect.sh", c.Detect+"\n", 0o755)
		add(c.ID+"/SKILL.md", c.SkillFile(), 0o644)
		add(c.ID+"/secrets", secrets.String(), 0o644)
		var env strings.Builder
		for _, k := range slices.Sorted(maps.Keys(c.Env)) {
			fmt.Fprintf(&env, "%s=%s\n", k, c.Env[k])
		}
		add(c.ID+"/env", env.String(), 0o644)
	}
	w.Close()
	return buf.Bytes()
}

// Check reports what is wrong with the catalog, for tests.
func Check() error {
	seen := map[string]bool{}
	secretName := regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	for _, c := range Catalog {
		switch {
		case !idPattern.MatchString(c.ID) || seen[c.ID]:
			return fmt.Errorf("capability %q: the id is malformed or taken", c.ID)
		case c.Name == "" || c.Description == "" || strings.ContainsAny(c.Description, "\n:"):
			return fmt.Errorf("capability %s: needs a name and a one-line description without colons", c.ID)
		case strings.TrimSpace(c.Install) == "" || strings.TrimSpace(c.Detect) == "" || strings.TrimSpace(c.Skill) == "":
			return fmt.Errorf("capability %s: needs install, detect and skill", c.ID)
		}
		for k, v := range c.Env {
			if !secretName.MatchString(k) || strings.ContainsAny(v, "\n\"`\\") {
				return fmt.Errorf("capability %s: variable %q is malformed", c.ID, k)
			}
		}
		for _, s := range c.Secrets {
			if !secretName.MatchString(s.Name) || s.Why == "" || strings.Contains(s.Why, "\n") {
				return fmt.Errorf("capability %s: secret %q is malformed", c.ID, s.Name)
			}
		}
		seen[c.ID] = true
	}
	return nil
}
