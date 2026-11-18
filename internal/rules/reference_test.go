package rules

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// referencePage is the wiki page that documents every rule type.
const referencePage = "../../wiki/reference/rule-library-and-back-tests.md"

// readReference reads the reference page from the repo.
func readReference(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(referencePage)
	if err != nil {
		t.Fatalf("reading the rule library reference: %v", err)
	}
	return string(raw)
}

// referenceSections splits the page's "## Rule types" section into its
// "### `type`" subsections, keyed by rule type, in page order.
func referenceSections(page string) (order []string, body map[string]string) {
	body = map[string]string{}
	inTypes := false
	current := ""
	for line := range strings.SplitSeq(page, "\n") {
		switch {
		case strings.HasPrefix(line, "## "):
			inTypes = strings.TrimSpace(line) == "## Rule types"
			current = ""
		case inTypes && strings.HasPrefix(line, "### "):
			current = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "### ")), "`")
			order = append(order, current)
		case inTypes && current != "":
			body[current] += line + "\n"
		}
	}
	return order, body
}

// referenceProblems lists every way the page disagrees with defs: a rule type
// with no section, a section for a type the code does not have, and a
// section missing its kind, its summary or a setting row with the exact
// accepted values Setting.Range gives.
func referenceProblems(page string, defs []Definition) []string {
	order, body := referenceSections(page)
	var out []string
	known := make([]string, 0, len(defs))
	for _, d := range defs {
		known = append(known, d.Type)
		text, ok := body[d.Type]
		if !ok {
			out = append(out, fmt.Sprintf("rule type %q has no \"### `%s`\" section under \"## Rule types\"", d.Type, d.Type))
			continue
		}
		if kind := fmt.Sprintf("**Kind:** %s rule.", d.Kind); !strings.Contains(text, kind) {
			out = append(out, fmt.Sprintf("%s: the section does not say %q", d.Type, kind))
		}
		if !strings.Contains(text, d.Summary) {
			out = append(out, fmt.Sprintf("%s: the section does not give the summary %q", d.Type, d.Summary))
		}
		for _, s := range d.Settings {
			row := fmt.Sprintf("| `%s` | %s |", s.Name, s.Range())
			if !strings.Contains(text, row) {
				out = append(out, fmt.Sprintf("%s: no settings row starting %q", d.Type, row))
			}
		}
		for _, line := range strings.Split(text, "\n") {
			name, ok := settingRowName(line)
			if ok && !slices.ContainsFunc(d.Settings, func(s Setting) bool { return s.Name == name }) {
				out = append(out, fmt.Sprintf("%s: the page lists a setting %q the rule type does not take", d.Type, name))
			}
		}
	}
	for _, t := range order {
		if !slices.Contains(known, t) {
			out = append(out, fmt.Sprintf("the page has a section for %q, which is not a rule type in the code", t))
		}
	}
	return out
}

// settingRowName returns the setting name of a settings-table row such as
// "| `days` | a whole number from 2 to 300 | ... |".
func settingRowName(line string) (string, bool) {
	if !strings.HasPrefix(line, "| `") {
		return "", false
	}
	rest := strings.TrimPrefix(line, "| `")
	name, _, ok := strings.Cut(rest, "`")
	return name, ok
}

// TestReferencePageCoversEveryRule fails when a rule type or setting in the
// code is missing from the wiki's rule library reference, or when the page
// documents one the code no longer has.
func TestReferencePageCoversEveryRule(t *testing.T) {
	for _, p := range referenceProblems(readReference(t), Definitions()) {
		t.Error(p)
	}
}

// TestReferenceProblemsCatchesDrift proves the check fails on a page that
// has fallen behind the code in each way it looks for.
func TestReferenceProblemsCatchesDrift(t *testing.T) {
	page := readReference(t)
	cases := []struct {
		name string
		page string
		defs []Definition
		want string
	}{
		{
			name: "a rule type with no section",
			page: strings.Replace(page, "### `breakout`", "### `breakout_renamed`", 1),
			defs: Definitions(),
			want: `rule type "breakout" has no`,
		},
		{
			name: "a section for a type the code lacks",
			page: page,
			defs: slices.DeleteFunc(Definitions(), func(d Definition) bool { return d.Type == "pullback" }),
			want: `section for "pullback", which is not a rule type`,
		},
		{
			name: "a missing setting",
			page: strings.Replace(page, "| `exit_days` |", "| exit days |", 1),
			defs: Definitions(),
			want: "breakout: no settings row starting \"| `exit_days` |",
		},
		{
			name: "a changed range",
			page: strings.Replace(page, "| `fast_days` | a whole number from 2 to 299 |", "| `fast_days` | a whole number from 2 to 250 |", 1),
			defs: Definitions(),
			want: "moving_average_cross: no settings row starting \"| `fast_days` | a whole number from 2 to 299 |",
		},
		{
			name: "a setting the code dropped",
			page: page,
			defs: withoutSetting(Definitions(), "momentum_ranking", "exit_rank"),
			want: `momentum_ranking: the page lists a setting "exit_rank"`,
		},
		{
			name: "a wrong kind",
			page: strings.Replace(page, "**Kind:** ranking rule.", "**Kind:** signal rule.", 1),
			defs: Definitions(),
			want: `momentum_ranking: the section does not say "**Kind:** ranking rule."`,
		},
	}
	if got := referenceProblems(page, Definitions()); len(got) != 0 {
		t.Fatalf("the real page should have no problems, got %q", got)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := referenceProblems(c.page, c.defs)
			if !slices.ContainsFunc(got, func(p string) bool { return strings.Contains(p, c.want) }) {
				t.Errorf("want a problem containing %q, got %q", c.want, got)
			}
		})
	}
}

// withoutSetting returns a copy of defs with one setting removed from one type.
func withoutSetting(defs []Definition, ruleType, setting string) []Definition {
	out := make([]Definition, len(defs))
	for i, d := range defs {
		if d.Type == ruleType {
			d.Settings = slices.DeleteFunc(slices.Clone(d.Settings), func(s Setting) bool { return s.Name == setting })
		}
		out[i] = d
	}
	return out
}
