package main

// The research job's helper subcommands. The nightly job on the Mac
// (deploy/mac/research-job.sh) runs "research-export" to fetch the export
// from the NAS, then Claude headless with the research skill, which writes a
// feed file and has no way to reach the NAS, then "research-upload" to check
// that file with the same feed.Check the NAS runs and send it.
//
// Both read the NAS address and the feed token from a small file outside the
// repo that only its owner can read. The token is sent only as a bearer
// header, never printed, and never sent over plain http except to this
// machine's own loopback address (which only tests use).

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/alexherrero/sherwood/internal/config"
	"github.com/alexherrero/sherwood/internal/feed"
	"github.com/alexherrero/sherwood/internal/secret"
)

const (
	researchExportCommand = "research-export"
	researchUploadCommand = "research-upload"

	// The NAS's machine routes (internal/web, research-feed task 2).
	exportPath = "/research/export"
	uploadPath = "/feed/upload"

	// researchTimeout bounds each request to the NAS.
	researchTimeout = 60 * time.Second
	// maxAnswerBytes is the most of the NAS's upload answer that is read.
	maxAnswerBytes = 256 << 10
)

// researchConfig is the research job's config file on the Mac. It lives
// outside the repo and only its owner may read it.
type researchConfig struct {
	// NASURL is sherwood's Tailscale Serve address, such as
	// https://sherwood.example-tailnet.ts.net.
	NASURL string `yaml:"nas_url"`
	// FeedToken is the feed_token from the NAS settings file.
	FeedToken secret.Secret `yaml:"feed_token"`
}

// loadResearchConfig reads and checks the config file. It refuses a file
// that group or others can read, an unknown key, a missing value, a token
// shorter than the NAS accepts, the example's placeholder, and an http
// address that is not this machine's loopback.
func loadResearchConfig(command, path string) (researchConfig, *url.URL, error) {
	if path == "" {
		return researchConfig{}, nil, fmt.Errorf("%s: -config is required", command)
	}
	info, err := os.Stat(path)
	if err != nil {
		return researchConfig{}, nil, fmt.Errorf("%s: config file: %w", command, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return researchConfig{}, nil, fmt.Errorf("%s: config file %s can be read by others; run chmod 600 on it", command, path)
	}
	raw, err := os.ReadFile(path) //nolint:gosec // the operator's own config file
	if err != nil {
		return researchConfig{}, nil, fmt.Errorf("%s: config file: %w", command, err)
	}
	var c researchConfig
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		// The YAML error can quote a line of the file, which may hold the
		// token, so it is not passed on.
		return researchConfig{}, nil, fmt.Errorf("%s: config file %s is not valid: it needs exactly nas_url and feed_token", command, path)
	}
	tok := c.FeedToken.Reveal()
	switch {
	case c.NASURL == "":
		return researchConfig{}, nil, fmt.Errorf("%s: config file %s has no nas_url", command, path)
	case tok == "":
		return researchConfig{}, nil, fmt.Errorf("%s: config file %s has no feed_token", command, path)
	case strings.HasPrefix(tok, "replace-me"):
		return researchConfig{}, nil, fmt.Errorf("%s: config file %s still holds the example's feed_token; use the one in the NAS settings file", command, path)
	case len(tok) < config.MinFeedTokenLen:
		return researchConfig{}, nil, fmt.Errorf("%s: the feed_token in %s is shorter than the %d characters the NAS accepts", command, path, config.MinFeedTokenLen)
	}
	base, err := url.Parse(strings.TrimRight(c.NASURL, "/"))
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return researchConfig{}, nil, fmt.Errorf("%s: nas_url %q is not an address like https://sherwood.example-tailnet.ts.net", command, c.NASURL)
	}
	switch base.Scheme {
	case "https":
	case "http":
		if !isLoopback(base.Hostname()) {
			return researchConfig{}, nil, fmt.Errorf("%s: nas_url must use https, so the token is never sent in the clear", command)
		}
	default:
		return researchConfig{}, nil, fmt.Errorf("%s: nas_url must use https", command)
	}
	return c, base, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// researchClient talks to the NAS. It never follows a redirect, so the
// bearer token goes only to the address in the config file.
func researchClient() *http.Client {
	return &http.Client{
		Timeout: researchTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func nasRequest(ctx context.Context, method string, base *url.URL, path string, token secret.Secret, body []byte) (*http.Request, error) {
	u := *base
	u.Path = strings.TrimRight(u.Path, "/") + path
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token.Reveal())
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// runResearchExport downloads the research export from the NAS, checks it
// with feed.DecodeExport and writes it, owner-only, where the job asks.
func runResearchExport(ctx context.Context, e env, args []string) error {
	const cmd = researchExportCommand
	fs := flag.NewFlagSet("sherwood "+cmd, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	configPath := fs.String("config", "", "the research job's config file, readable only by you, with nas_url and feed_token (required)")
	outPath := fs.String("out", "", "file to write the export to, such as WORK/export.json (required)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: sherwood research-export -config FILE -out FILE")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%s: unexpected argument %q", cmd, fs.Arg(0))
	}
	if *outPath == "" {
		return fmt.Errorf("%s: -out is required", cmd)
	}
	c, base, err := loadResearchConfig(cmd, *configPath)
	if err != nil {
		return err
	}

	req, err := nasRequest(ctx, http.MethodGet, base, exportPath, c.FeedToken, nil)
	if err != nil {
		return fmt.Errorf("%s: %w", cmd, err)
	}
	resp, err := researchClient().Do(req)
	if err != nil {
		return fmt.Errorf("%s: asking the NAS for the export: %w", cmd, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, feed.MaxExportBytes+1))
	if err != nil {
		return fmt.Errorf("%s: reading the export: %w", cmd, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: the NAS answered %s%s", cmd, resp.Status, answerSnippet(body))
	}
	export, err := feed.DecodeExport(body)
	if err != nil {
		return fmt.Errorf("%s: %w", cmd, err)
	}
	if err := writeOwnerOnly(*outPath, body); err != nil {
		return fmt.Errorf("%s: %w", cmd, err)
	}

	held, watched := 0, 0
	for _, s := range export.Symbols {
		if s.Held {
			held++
		}
		if s.Watched {
			watched++
		}
	}
	date := export.SignalDate
	if date == "" {
		date = "none yet"
	}
	fmt.Fprintf(e.stdout, "export generated %s: %d stocks (%d held, %d watched), signal date %s, %d signals\n",
		export.GeneratedAt.UTC().Format(time.RFC3339), len(export.Symbols), held, watched, date, len(export.Signals))
	fmt.Fprintf(e.stdout, "wrote %s\n", *outPath)
	return nil
}

// runResearchUpload checks a feed file against the export it was written
// from, with the same feed.Check the NAS runs, prints what would be dropped
// and then sends the file to the NAS and prints its answer. -check-only
// stops before sending.
func runResearchUpload(ctx context.Context, e env, args []string) error {
	const cmd = researchUploadCommand
	fs := flag.NewFlagSet("sherwood "+cmd, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	configPath := fs.String("config", "", "the research job's config file, readable only by you, with nas_url and feed_token (required unless -check-only)")
	exportFile := fs.String("export", "", "the export the feed was written from, as research-export wrote it (required)")
	feedFile := fs.String("feed", "", "the feed file the research skill wrote (required)")
	checkOnly := fs.Bool("check-only", false, "check the feed file and print the drops, but send nothing")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: sherwood research-upload -config FILE -export FILE -feed FILE [-check-only]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%s: unexpected argument %q", cmd, fs.Arg(0))
	}
	if *exportFile == "" {
		return fmt.Errorf("%s: -export is required", cmd)
	}
	if *feedFile == "" {
		return fmt.Errorf("%s: -feed is required", cmd)
	}
	// The config is checked first, so a bad config fails before any work.
	var (
		c    researchConfig
		base *url.URL
	)
	if !*checkOnly {
		var err error
		if c, base, err = loadResearchConfig(cmd, *configPath); err != nil {
			return err
		}
	}

	exportRaw, err := readCapped(*exportFile, feed.MaxExportBytes)
	if err != nil {
		return fmt.Errorf("%s: export: %w", cmd, err)
	}
	export, err := feed.DecodeExport(exportRaw)
	if err != nil {
		return fmt.Errorf("%s: %w", cmd, err)
	}
	data, err := readCapped(*feedFile, feed.MaxFileBytes)
	if err != nil {
		return fmt.Errorf("%s: feed: %w", cmd, err)
	}
	checked, err := feed.Check(data, export.Known())
	if err != nil {
		// The NAS would refuse the whole file, so it is not sent.
		return fmt.Errorf("%s: %s was not sent: %w", cmd, *feedFile, err)
	}
	w := e.stdout
	fmt.Fprintf(w, "%s: %d items pass the check, %d would be dropped\n", *feedFile, len(checked.Items), len(checked.Dropped))
	for _, d := range checked.Dropped {
		fmt.Fprintf(w, "  dropped item %d (%s %s): %s\n", d.Index, d.Kind, d.Symbol, d.Reason)
	}
	if *checkOnly {
		fmt.Fprintln(w, "check only: nothing was sent")
		return nil
	}

	// The file goes as written; the NAS checks it again and drops the same
	// items, because it builds Known from the same export.
	req, err := nasRequest(ctx, http.MethodPost, base, uploadPath, c.FeedToken, data)
	if err != nil {
		return fmt.Errorf("%s: %w", cmd, err)
	}
	resp, err := researchClient().Do(req)
	if err != nil {
		return fmt.Errorf("%s: sending the feed: %w", cmd, err)
	}
	defer func() { _ = resp.Body.Close() }()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes))
	if err != nil {
		return fmt.Errorf("%s: reading the NAS's answer: %w", cmd, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s: the NAS refused the feed: %s%s", cmd, resp.Status, answerSnippet(answer))
	}
	fmt.Fprintf(w, "the NAS answered %s\n", resp.Status)
	var pretty bytes.Buffer
	if json.Indent(&pretty, answer, "", "  ") == nil {
		answer = pretty.Bytes()
	}
	if len(answer) > 0 {
		fmt.Fprintf(w, "%s\n", bytes.TrimRight(answer, "\n"))
	}
	return nil
}

// answerSnippet is the start of a refusal's body, on one line, for an error.
func answerSnippet(body []byte) string {
	s := strings.Join(strings.Fields(string(body)), " ")
	if s == "" {
		return ""
	}
	if r := []rune(s); len(r) > 300 {
		s = string(r[:300]) + "…"
	}
	return ": " + s
}

// readCapped reads a file, refusing one over limit bytes.
func readCapped(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // a file the job named
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, limit)
	}
	return b, nil
}

// writeOwnerOnly writes data to path, readable only by its owner, through a
// temporary file so a failed run never leaves half a file behind.
func writeOwnerOnly(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".export-*.json")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
