package api

import (
	"regexp"
	"strings"

	"github.com/brdsmth/beethoven/internal/store"
)

type DotenvVar struct {
	Name, Value string
	Line        int
}

type Skipped struct {
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

var dotenvLine = regexp.MustCompile(`^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$`)

// ParseDotenv reads KEY=value lines. It skips blanks and # comments,
// accepts an "export " prefix, strips one pair of matching quotes, and
// expands \n in double-quoted values. A repeated key keeps its last value.
func ParseDotenv(text string) ([]DotenvVar, []Skipped) {
	var vars []DotenvVar
	var skipped []Skipped
	index := map[string]int{}
	for i, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := dotenvLine.FindStringSubmatch(line)
		if m == nil {
			skipped = append(skipped, Skipped{Line: i + 1, Reason: "not a KEY=value line"})
			continue
		}
		name, value := m[1], strings.TrimSpace(m[2])
		switch {
		case len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"':
			value = strings.ReplaceAll(value[1:len(value)-1], `\n`, "\n")
		case len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'':
			value = value[1 : len(value)-1]
		default:
			if j := strings.Index(value, " #"); j >= 0 { // trailing comment on an unquoted value
				value = strings.TrimSpace(value[:j])
			}
		}
		if err := store.ValidateSecret(name, value); err != nil {
			skipped = append(skipped, Skipped{Line: i + 1, Reason: strings.TrimPrefix(err.Error(), store.ErrInvalid.Error()+": ")})
			continue
		}
		if j, ok := index[name]; ok {
			vars[j] = DotenvVar{Name: name, Value: value, Line: i + 1}
			continue
		}
		index[name] = len(vars)
		vars = append(vars, DotenvVar{Name: name, Value: value, Line: i + 1})
	}
	return vars, skipped
}
