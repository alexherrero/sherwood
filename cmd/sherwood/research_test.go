package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alexherrero/sherwood/internal/feed"
)

// Everything here is made up. No test reaches a real NAS, runs the real
// Claude CLI or uses a real feed token.

var (
	researchSkillDir = filepath.Join("..", "..", ".claude", "skills", "research")
	researchJobDir   = filepath.Join("..", "..", "deploy", "mac")
	researchJobPath  = filepath.Join(researchJobDir, "research-job.sh")
)

// fakeFeedToken stands in for the feed token. It must never reach the output.
const fakeFeedToken = "fake-feed-token-for-tests-only-0123456789" //nolint:gosec // a made-up value, not a credential

// asSherwoodEnv, when set, makes the test binary act as the sherwood command
// (see TestMain), so the wrapper script can run the real subcommands.
const asSherwoodEnv = "SHERWOOD_TEST_AS_SHERWOOD"

// fakeNAS stands in for the NAS's two machine routes. It answers only a
// request carrying the fake token, checks an upload with the same feed.Check
// the real route runs, and records what it saw.
type fakeNAS struct {
	t      *testing.T
	export []byte
	known  feed.Known

	mu         sync.Mutex
	exports    int
	uploads    [][]byte
	badAuth    int
	uploadType string
}

func newFakeNAS(t *testing.T) (*fakeNAS, *httptest.Server) {
	t.Helper()
	raw, err := readTestFile(filepath.Join(researchSkillDir, "example-export.json"))
	if err != nil {
		t.Fatal(err)
	}
	ex, err := feed.DecodeExport(raw)
	if err != nil {
		t.Fatal(err)
	}
	n := &fakeNAS{t: t, export: raw, known: ex.Known()}
	srv := httptest.NewServer(n)
	t.Cleanup(srv.Close)
	return n, srv
}

// seen returns what the fake NAS has received so far.
func (n *fakeNAS) seen() (exports int, uploads [][]byte, badAuth int, uploadType string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.exports, slices.Clone(n.uploads), n.badAuth, n.uploadType
}

func (n *fakeNAS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+fakeFeedToken {
		n.badAuth++
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == exportPath:
		n.exports++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(n.export)
	case r.Method == http.MethodPost && r.URL.Path == uploadPath:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read", http.StatusBadRequest)
			return
		}
		n.uploadType = r.Header.Get("Content-Type")
		f, err := feed.Check(body, n.known)
		if err != nil {
			http.Error(w, `{"error":"off schema"}`, http.StatusBadRequest)
			return
		}
		n.uploads = append(n.uploads, body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"accepted": len(f.Items), "dropped": f.Dropped})
	default:
		http.NotFound(w, r)
	}
}

// writeResearchConfig writes an owner-only config file pointing at url.
func writeResearchConfig(t *testing.T, dir, nasURL, token string) string {
	t.Helper()
	p := filepath.Join(dir, "research.yaml")
	body := "nas_url: \"" + nasURL + "\"\nfeed_token: \"" + token + "\"\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func assertNoToken(t *testing.T, where, s string) {
	t.Helper()
	if strings.Contains(s, fakeFeedToken) {
		t.Errorf("the feed token appears in %s", where)
	}
}

// --- the wrapper's tool list ---------------------------------------------

// researchTools are the only tools the research session may have: read its
// inputs, search and fetch public pages, and write (into its output folder).
var researchTools = []string{"Read", "Write", "WebFetch", "WebSearch"}

// researchJobProblems reads the wrapper script and lists every way its
// Claude session could get a shell, a tool that can send to the NAS, or
// writes outside its output folder.
func researchJobProblems(script string) []string {
	var problems []string
	var code []string // the script without comment lines
	for _, line := range strings.Split(script, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			code = append(code, line)
		}
	}
	body := strings.Join(code, "\n")

	assignment := func(name string) (string, bool) {
		re := regexp.MustCompile(`(?m)^` + name + `="([^"]*)"$`)
		m := re.FindAllStringSubmatch(body, -1)
		if len(m) != 1 {
			problems = append(problems, name+" must be set exactly once, as "+name+`="..."`)
			return "", false
		}
		return m[0][1], true
	}

	if tools, ok := assignment("TOOLS"); ok {
		for _, tool := range strings.Split(tools, ",") {
			tool = strings.TrimSpace(tool)
			switch {
			case strings.Contains(strings.ToLower(tool), "bash"), strings.Contains(strings.ToLower(tool), "shell"):
				problems = append(problems, "TOOLS gives the research session a shell: "+tool)
			case !slices.Contains(researchTools, tool):
				problems = append(problems, "TOOLS names "+tool+", which is not read, write, web fetch or web search")
			}
		}
	}
	if allowed, ok := assignment("ALLOWED_TOOLS"); ok {
		for _, rule := range strings.Split(allowed, ",") {
			switch rule = strings.TrimSpace(rule); rule {
			case "Read", "WebFetch", "WebSearch", `Edit(/$OUT_DIR/**)`:
			default:
				problems = append(problems, "ALLOWED_TOOLS pre-approves "+rule+"; only Read, WebFetch, WebSearch and Edit(/$OUT_DIR/**) may be")
			}
		}
	}
	if denied, ok := assignment("DENIED_TOOLS"); ok {
		rules := strings.Split(denied, ",")
		for _, want := range []string{"Bash", "WebFetch(domain:*.ts.net)"} {
			if !slices.Contains(rules, want) {
				problems = append(problems, "DENIED_TOOLS must refuse "+want)
			}
		}
	}

	// The one Claude call must use those lists, with no way to widen them.
	for _, want := range []string{
		`"$CLAUDE" -p`, "--restricted", "--strict-mcp-config", "--permission-mode dontAsk",
		`--tools "$TOOLS"`, `--allowedTools "$ALLOWED_TOOLS"`, `--disallowedTools "$DENIED_TOOLS"`,
	} {
		if !strings.Contains(body, want) {
			problems = append(problems, "the Claude call lacks "+want)
		}
	}
	if n := strings.Count(body, `"$CLAUDE"`); n != 1 {
		problems = append(problems, "the script must call Claude exactly once")
	}
	for _, bad := range []string{
		"bypassPermissions", "dangerously-skip-permissions", "acceptEdits",
		"--mcp-config", "--add-dir", "--settings", "--agents", "--plugin-dir", "--allow-dangerously",
	} {
		if strings.Contains(body, bad) {
			problems = append(problems, "the script uses "+bad+", which can widen what the session may do")
		}
	}
	return problems
}

func TestResearchJobToolListHasNoShellOrSender(t *testing.T) {
	raw, err := readTestFile(researchJobPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range researchJobProblems(string(raw)) {
		t.Error(p)
	}
}

// The checker must catch each way the tool list could be widened.
func TestResearchJobProblemsCatchesAWidenedToolList(t *testing.T) {
	raw, err := readTestFile(researchJobPath)
	if err != nil {
		t.Fatal(err)
	}
	good := string(raw)
	plants := map[string][2]string{
		"a shell in TOOLS":            {`TOOLS="Read,Write,WebFetch,WebSearch"`, `TOOLS="Read,Write,WebFetch,WebSearch,Bash"`},
		"an MCP tool in TOOLS":        {`TOOLS="Read,Write,WebFetch,WebSearch"`, `TOOLS="Read,Write,WebFetch,WebSearch,mcp__http__post"`},
		"edits anywhere":              {`TOOLS="Read,Write,WebFetch,WebSearch"`, `TOOLS="Read,Write,Edit,WebFetch,WebSearch"`},
		"a pre-approved shell":        {`Edit(/$OUT_DIR/**)"`, `Edit(/$OUT_DIR/**),Bash(curl *)"`},
		"unrestricted writes":         {`Edit(/$OUT_DIR/**)"`, `Edit"`},
		"no shell denial":             {`DENIED_TOOLS="Bash,`, `DENIED_TOOLS="`},
		"no tailnet denial":           {`,WebFetch(domain:*.ts.net)"`, `"`},
		"bypassed permissions":        {"--permission-mode dontAsk", "--permission-mode bypassPermissions"},
		"an MCP config":               {"--strict-mcp-config", "--strict-mcp-config --mcp-config nas.json"},
		"no restricted mode":          {"    --restricted \\\n", ""},
		"the tool list not passed on": {`--tools "$TOOLS"`, `--tools default`},
	}
	if len(researchJobProblems(good)) != 0 {
		t.Fatal("the real script must pass before the plants mean anything")
	}
	for name, p := range plants {
		if !strings.Contains(good, p[0]) {
			t.Errorf("%s: the script no longer contains %q", name, p[0])
			continue
		}
		if len(researchJobProblems(strings.Replace(good, p[0], p[1], 1))) == 0 {
			t.Errorf("%s: not caught", name)
		}
	}
}

// --- the dry run ----------------------------------------------------------

// fakeClaudeScript stands in for the Claude CLI. It records its arguments,
// its working folder and its prompt, then writes the example feed where the
// skill says to, as the real session would.
const fakeClaudeScript = `#!/bin/sh
printf '%s\n' "$@" > "$FAKE_CLAUDE_LOG/args"
pwd > "$FAKE_CLAUDE_LOG/pwd"
cat > "$FAKE_CLAUDE_LOG/prompt"
cp "$FAKE_CLAUDE_FEED" out/feed.json
`

func TestResearchJobDryRun(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	nas, srv := newFakeNAS(t)

	dir := t.TempDir()
	research := filepath.Join(dir, "research")
	logDir := filepath.Join(dir, "claude-log")
	for _, d := range []string{research, logDir} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeResearchConfig(t, research, srv.URL, fakeFeedToken)
	shim := filepath.Join(dir, "sherwood")
	claude := filepath.Join(dir, "claude")
	//nolint:gosec // test scripts must be executable
	if err := os.WriteFile(shim, []byte("#!/bin/sh\n"+asSherwoodEnv+"=1 exec '"+self+"' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // test scripts must be executable
	if err := os.WriteFile(claude, []byte(fakeClaudeScript), 0o700); err != nil {
		t.Fatal(err)
	}
	skill, err := filepath.Abs(researchSkillDir)
	if err != nil {
		t.Fatal(err)
	}
	exampleFeed := filepath.Join(skill, "example-feed.json")

	cmd := exec.CommandContext(context.Background(), bash, researchJobPath) //nolint:gosec // the repo's own script
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + dir,
		"RESEARCH_DIR=" + research,
		"SHERWOOD=" + shim,
		"CLAUDE=" + claude,
		"SKILL_DIR=" + skill,
		"FAKE_CLAUDE_LOG=" + logDir,
		"FAKE_CLAUDE_FEED=" + exampleFeed,
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("the job failed: %v\n%s", err, out.String())
	}
	assertNoToken(t, "the job's output", out.String())

	exports, uploads, badAuth, uploadType := nas.seen()

	// Step 1: the export was fetched with the token and written, owner-only.
	runDir := filepath.Join(research, "run")
	if exports != 1 || badAuth != 0 {
		t.Errorf("the NAS saw %d export requests and %d without the token, want 1 and 0", exports, badAuth)
	}
	got, err := readTestFile(filepath.Join(runDir, "export.json"))
	if err != nil || !bytes.Equal(got, nas.export) {
		t.Errorf("export.json is not the NAS's export (err %v)", err)
	}
	if info, err := os.Stat(filepath.Join(runDir, "export.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("export.json should be owner-only: %v %v", info.Mode(), err)
	}

	// Step 2: Claude ran in its own folder with only the allowed tools, and
	// wrote a valid feed there.
	pwd, _ := readTestFile(filepath.Join(logDir, "pwd"))
	realRun, _ := filepath.EvalSymlinks(runDir)
	if p := strings.TrimSpace(string(pwd)); p != runDir && p != realRun {
		t.Errorf("Claude ran in %s, want %s", p, runDir)
	}
	argsRaw, _ := readTestFile(filepath.Join(logDir, "args"))
	args := strings.Split(strings.TrimRight(string(argsRaw), "\n"), "\n")
	flagValue := func(name string) string {
		for i, a := range args {
			if a == name && i+1 < len(args) {
				return args[i+1]
			}
		}
		t.Errorf("Claude was not given %s; args: %q", name, args)
		return ""
	}
	if v := flagValue("--tools"); v != strings.Join(researchTools, ",") {
		t.Errorf("--tools %q, want %q", v, strings.Join(researchTools, ","))
	}
	if v := flagValue("--allowedTools"); v != "Read,WebFetch,WebSearch,Edit(/"+filepath.Join(runDir, "out")+"/**)" {
		t.Errorf("--allowedTools %q does not limit writes to the output folder", v)
	}
	if v := flagValue("--permission-mode"); v != "dontAsk" {
		t.Errorf("--permission-mode %q, want dontAsk", v)
	}
	for _, want := range []string{"-p", "--restricted", "--strict-mcp-config"} {
		if !slices.Contains(args, want) {
			t.Errorf("Claude was not given %s", want)
		}
	}
	assertNoToken(t, "Claude's arguments", string(argsRaw))
	prompt, _ := readTestFile(filepath.Join(logDir, "prompt"))
	if !strings.Contains(string(prompt), "research-skill.md") {
		t.Errorf("the prompt does not point Claude at the skill: %q", prompt)
	}
	skillCopy, _ := readTestFile(filepath.Join(runDir, "research-skill.md"))
	skillText, _ := readTestFile(filepath.Join(skill, "SKILL.md"))
	if len(skillCopy) == 0 || !bytes.Equal(skillCopy, skillText) {
		t.Error("the run folder does not hold a copy of the skill")
	}
	entries, _ := os.ReadDir(runDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "research.yaml") {
			t.Errorf("the config file is inside Claude's folder: %s", e.Name())
		}
	}

	// Step 3: the feed reached the NAS with the token, as a valid body.
	if len(uploads) != 1 {
		t.Fatalf("the NAS received %d uploads, want 1\n%s", len(uploads), out.String())
	}
	want, _ := readTestFile(exampleFeed)
	if !bytes.Equal(uploads[0], want) {
		t.Error("the uploaded body is not the feed file Claude wrote")
	}
	f, err := feed.Check(uploads[0], nas.known)
	if err != nil || len(f.Items) != 5 || len(f.Dropped) != 0 {
		t.Errorf("the uploaded feed: %d kept, %d dropped, err %v; want 5, 0, nil", len(f.Items), len(f.Dropped), err)
	}
	if uploadType != "application/json" {
		t.Errorf("upload Content-Type %q, want application/json", uploadType)
	}
	for _, line := range []string{"5 items pass the check, 0 would be dropped", `"accepted": 5`, "research-job: done"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("the job's output lacks %q:\n%s", line, out.String())
		}
	}
}

// The job stops before uploading when the research session wrote nothing,
// so yesterday's feed is never sent again.
func TestResearchJobUploadsNothingWithoutAFeed(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	nas, srv := newFakeNAS(t)
	dir := t.TempDir()
	research := filepath.Join(dir, "research")
	if err := os.MkdirAll(filepath.Join(research, "run", "out"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Last night's feed is still there; the job must clear it.
	stale := filepath.Join(research, "run", "out", "feed.json")
	if err := os.WriteFile(stale, []byte(`{"version":1,"items":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeResearchConfig(t, research, srv.URL, fakeFeedToken)
	shim := filepath.Join(dir, "sherwood")
	claude := filepath.Join(dir, "claude")
	//nolint:gosec // test scripts must be executable
	if err := os.WriteFile(shim, []byte("#!/bin/sh\n"+asSherwoodEnv+"=1 exec '"+self+"' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // test scripts must be executable
	if err := os.WriteFile(claude, []byte("#!/bin/sh\ncat >/dev/null\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	skill, _ := filepath.Abs(researchSkillDir)
	cmd := exec.CommandContext(context.Background(), bash, researchJobPath) //nolint:gosec // the repo's own script
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "RESEARCH_DIR=" + research,
		"SHERWOOD=" + shim, "CLAUDE=" + claude, "SKILL_DIR=" + skill}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err == nil {
		t.Fatalf("the job succeeded with no feed:\n%s", out.String())
	}
	if _, uploads, _, _ := nas.seen(); len(uploads) != 0 {
		t.Errorf("the NAS received %d uploads, want none", len(uploads))
	}
	if !strings.Contains(out.String(), "wrote no feed file") {
		t.Errorf("the job did not say why it stopped:\n%s", out.String())
	}
}

// --- the subcommands -------------------------------------------------------

func TestResearchExportWritesTheCheckedExport(t *testing.T) {
	nas, srv := newFakeNAS(t)
	dir := t.TempDir()
	config := writeResearchConfig(t, dir, srv.URL+"/", fakeFeedToken)
	outPath := filepath.Join(dir, "export.json")
	stdout, stderr, err := runCmd(t, env{}, researchExportCommand, "-config", config, "-out", outPath)
	if err != nil {
		t.Fatal(err)
	}
	assertNoToken(t, "stdout", stdout)
	assertNoToken(t, "stderr", stderr)
	if !strings.Contains(stdout, "3 stocks (2 held, 2 watched), signal date 2026-11-13, 1 signals") {
		t.Errorf("summary: %s", stdout)
	}
	got, _ := readTestFile(outPath)
	if !bytes.Equal(got, nas.export) {
		t.Error("the written export differs from the NAS's")
	}
}

func TestResearchExportRefusesABadExport(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   string
	}{
		"refused":       {http.StatusUnauthorized, `{"error":"unauthorized"}`, "401"},
		"unknown field": {http.StatusOK, `{"version":1,"generated_at":"2026-11-13T22:00:00Z","signal_date":"","symbols":[],"signals":[],"cash":"1.00"}`, "unknown field"},
		"wrong version": {http.StatusOK, `{"version":2,"generated_at":"2026-11-13T22:00:00Z","signal_date":"","symbols":[],"signals":[]}`, "version 2"},
		"redirect":      {http.StatusFound, ``, "302"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if c.status == http.StatusFound {
					http.Redirect(w, r, "/elsewhere", http.StatusFound)
					return
				}
				w.WriteHeader(c.status)
				_, _ = io.WriteString(w, c.body)
			}))
			defer srv.Close()
			dir := t.TempDir()
			config := writeResearchConfig(t, dir, srv.URL, fakeFeedToken)
			outPath := filepath.Join(dir, "export.json")
			_, _, err := runCmd(t, env{}, researchExportCommand, "-config", config, "-out", outPath)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want one mentioning %q", err, c.want)
			}
			assertNoToken(t, "the error", err.Error())
			if n := hits.Load(); n != 1 {
				t.Errorf("%d requests, want 1 (a redirect is not followed)", n)
			}
			if _, err := os.Stat(outPath); !os.IsNotExist(err) {
				t.Error("a refused export was written")
			}
		})
	}
}

func TestResearchConfigRefusals(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	cases := map[string]struct {
		body string
		mode os.FileMode
		want string
	}{
		"readable by others": {"nas_url: \"" + srv.URL + "\"\nfeed_token: \"" + fakeFeedToken + "\"\n", 0o644, "chmod 600"},
		"placeholder token":  {"nas_url: \"" + srv.URL + "\"\nfeed_token: \"replace-me-with-the-feed-token-from-the-nas\"\n", 0o600, "example's feed_token"},
		"short token":        {"nas_url: \"" + srv.URL + "\"\nfeed_token: \"short\"\n", 0o600, "shorter than"},
		"no token":           {"nas_url: \"" + srv.URL + "\"\n", 0o600, "no feed_token"},
		"no address":         {"feed_token: \"" + fakeFeedToken + "\"\n", 0o600, "no nas_url"},
		"plain http":         {"nas_url: \"http://sherwood.example-tailnet.ts.net\"\nfeed_token: \"" + fakeFeedToken + "\"\n", 0o600, "must use https"},
		"other scheme":       {"nas_url: \"ftp://sherwood.example-tailnet.ts.net\"\nfeed_token: \"" + fakeFeedToken + "\"\n", 0o600, "must use https"},
		"user in address":    {"nas_url: \"https://me@sherwood.example-tailnet.ts.net\"\nfeed_token: \"" + fakeFeedToken + "\"\n", 0o600, "is not an address"},
		"unknown key":        {"nas_url: \"" + srv.URL + "\"\nfeed_token: \"" + fakeFeedToken + "\"\nextra: 1\n", 0o600, "is not valid"},
		// A YAML error would quote the line; the token must not come with it.
		"broken yaml": {"nas_url: \"" + srv.URL + "\"\nfeed_token: [\"" + fakeFeedToken + "\"\n", 0o600, "is not valid"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "research.yaml")
			if err := os.WriteFile(p, []byte(c.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(p, c.mode); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{
				{researchExportCommand, "-config", p, "-out", filepath.Join(dir, "export.json")},
				{researchUploadCommand, "-config", p, "-export", "x", "-feed", "y"},
			} {
				stdout, stderr, err := runCmd(t, env{}, args...)
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Errorf("%s: err %v, want one mentioning %q", args[0], err, c.want)
					continue
				}
				assertNoToken(t, "the error", err.Error())
				assertNoToken(t, "stdout", stdout)
				assertNoToken(t, "stderr", stderr)
			}
		})
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("a refused config still reached the server %d times", n)
	}
	if _, _, err := runCmd(t, env{}, researchExportCommand, "-out", "x"); err == nil || !strings.Contains(err.Error(), "-config is required") {
		t.Errorf("no -config: %v", err)
	}
}

func TestResearchUploadSendsACheckedFeed(t *testing.T) {
	nas, srv := newFakeNAS(t)
	dir := t.TempDir()
	config := writeResearchConfig(t, dir, srv.URL, fakeFeedToken)
	exportPath := filepath.Join(researchSkillDir, "example-export.json")

	// One good item, one sourceless and one candidate on a held stock.
	feedPath := filepath.Join(dir, "feed.json")
	src := `[{"url":"https://news.example.com/a","title":"Example"}]`
	body := `{"version":1,"items":[
	  {"kind":"candidate","symbol":"CAND1","text":"Made up.","sources":` + src + `},
	  {"kind":"holding_note","symbol":"HELD1","text":"Made up.","sources":[]},
	  {"kind":"candidate","symbol":"HELD2","text":"Made up.","sources":` + src + `}]}`
	if err := os.WriteFile(feedPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runCmd(t, env{}, researchUploadCommand, "-config", config, "-export", exportPath, "-feed", feedPath)
	if err != nil {
		t.Fatal(err)
	}
	assertNoToken(t, "stdout", stdout)
	assertNoToken(t, "stderr", stderr)
	for _, want := range []string{
		"1 items pass the check, 2 would be dropped",
		"dropped item 1 (holding_note HELD1)",
		"dropped item 2 (candidate HELD2)",
		"the NAS answered 200 OK",
		`"accepted": 1`,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
	if _, uploads, _, _ := nas.seen(); len(uploads) != 1 || string(uploads[0]) != body {
		t.Error("the NAS did not receive the file as written")
	}

	// -check-only sends nothing and needs no config.
	stdout, _, err = runCmd(t, env{}, researchUploadCommand, "-check-only", "-export", exportPath, "-feed", feedPath)
	if _, uploads, _, _ := nas.seen(); err != nil || !strings.Contains(stdout, "nothing was sent") || len(uploads) != 1 {
		t.Errorf("-check-only: err %v, uploads %d, output %s", err, len(uploads), stdout)
	}
}

func TestResearchUploadSendsNothingOffSchema(t *testing.T) {
	nas, srv := newFakeNAS(t)
	dir := t.TempDir()
	config := writeResearchConfig(t, dir, srv.URL, fakeFeedToken)
	exportPath := filepath.Join(researchSkillDir, "example-export.json")
	for name, body := range map[string]string{
		"unknown field": `{"version":1,"items":[],"orders":[]}`,
		"not json":      `here is your feed`,
		"no items":      `{"version":1}`,
	} {
		p := filepath.Join(dir, "feed.json")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := runCmd(t, env{}, researchUploadCommand, "-config", config, "-export", exportPath, "-feed", p)
		if err == nil || !strings.Contains(err.Error(), "was not sent") {
			t.Errorf("%s: err %v, want a refusal", name, err)
		}
	}
	if _, uploads, _, _ := nas.seen(); len(uploads) != 0 {
		t.Errorf("an off-schema file reached the NAS: %d uploads", len(uploads))
	}
}

func TestResearchUploadReportsARefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"the feed token is wrong"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	dir := t.TempDir()
	config := writeResearchConfig(t, dir, srv.URL, fakeFeedToken)
	_, _, err := runCmd(t, env{}, researchUploadCommand, "-config", config,
		"-export", filepath.Join(researchSkillDir, "example-export.json"),
		"-feed", filepath.Join(researchSkillDir, "example-feed.json"))
	if err == nil || !strings.Contains(err.Error(), "refused the feed: 401") {
		t.Fatalf("err %v, want the NAS's refusal", err)
	}
	assertNoToken(t, "the error", err.Error())
}

// --- the skill and the example files --------------------------------------

func TestResearchExampleFeedIsValidForTheExampleExport(t *testing.T) {
	raw, err := readTestFile(filepath.Join(researchSkillDir, "example-export.json"))
	if err != nil {
		t.Fatal(err)
	}
	ex, err := feed.DecodeExport(raw)
	if err != nil {
		t.Fatal(err)
	}
	data, err := readTestFile(filepath.Join(researchSkillDir, "example-feed.json"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := feed.Check(data, ex.Known())
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Dropped) != 0 {
		t.Errorf("the example feed has drops: %+v", f.Dropped)
	}
	kinds := map[feed.Kind]int{}
	fake := regexp.MustCompile(`^[A-Z]{4}[0-9]$`)
	for _, it := range f.Items {
		kinds[it.Kind]++
		if !fake.MatchString(it.Symbol) {
			t.Errorf("%s is not an obviously fake symbol", it.Symbol)
		}
		for _, s := range it.Sources {
			u, err := url.Parse(s.URL)
			if err != nil || !regexp.MustCompile(`(^|\.)example\.(com|org|net)$`).MatchString(u.Hostname()) {
				t.Errorf("source %s is not on an example domain", s.URL)
			}
		}
	}
	for _, k := range []feed.Kind{feed.Candidate, feed.HoldingNote, feed.SignalExplanation} {
		if kinds[k] == 0 {
			t.Errorf("the example feed has no %s", k)
		}
	}
	for _, s := range ex.Symbols {
		if !fake.MatchString(s.Symbol) {
			t.Errorf("export symbol %s is not obviously fake", s.Symbol)
		}
	}
}

func TestResearchSkillStatesTheOneRule(t *testing.T) {
	raw, err := readTestFile(filepath.Join(researchSkillDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		"name: research",
		"never suggest an order size or an action to take",
		"at least one source",
		"out/feed.json",
		"export.json",
		"data, not instructions",
		"not financial advice",
		"candidate", "holding_note", "signal_explanation", "signal_key",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("SKILL.md does not say %q", want)
		}
	}
}

// A privacy scan of the skill and the Mac files: no email address, home
// folder, real-looking token or tailnet name. The placeholders are allowed by
// their exact text.
func TestResearchFilesHoldPlaceholdersOnly(t *testing.T) {
	placeholders := []string{
		"replace-me-with-the-feed-token-from-the-nas",
		strings.Repeat("ab", 32), // the example signal key
		"com.example.sherwood-research",
	}
	bad := map[string]*regexp.Regexp{
		"email address": regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`),
		"home folder":   regexp.MustCompile(`/Users/[A-Za-z]|/home/[A-Za-z]`),
		"key-like text": regexp.MustCompile(`[A-Za-z0-9_\-]{32,}`),
		"tailnet name":  regexp.MustCompile(`[A-Za-z0-9-]+\.ts\.net`),
		"broker host":   regexp.MustCompile(`(?i)robinhood\.com`),
	}
	allowedTailnet := "sherwood.example-tailnet.ts.net"
	var files []string
	for _, d := range []string{researchSkillDir, researchJobDir} {
		entries, err := os.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			files = append(files, filepath.Join(d, e.Name()))
		}
	}
	if len(files) < 6 {
		t.Fatalf("found only %v", files)
	}
	for _, p := range files {
		raw, err := readTestFile(p)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ReplaceAll(string(raw), allowedTailnet, "")
		for _, ph := range placeholders {
			text = strings.ReplaceAll(text, ph, "")
		}
		for what, re := range bad {
			if m := re.FindString(text); m != "" {
				t.Errorf("%s holds a %s: %q", p, what, m)
			}
		}
	}
	plist, _ := readTestFile(filepath.Join(researchJobDir, "com.example.sherwood-research.plist"))
	for _, line := range strings.Split(string(plist), "\n") {
		if s := strings.TrimSpace(line); strings.HasPrefix(s, "<string>/") && !strings.HasPrefix(s, "<string>/PATH/TO/") &&
			!strings.HasPrefix(s, "<string>/bin/bash<") && !strings.HasPrefix(s, "<string>/usr/bin:") {
			t.Errorf("the plist holds a real-looking path: %s", s)
		}
	}
}

// readTestFile reads a file this test wrote or one of the repo's own files.
func readTestFile(path string) ([]byte, error) {
	return os.ReadFile(path) //nolint:gosec // a test or repo file, not user input
}
