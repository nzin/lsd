package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"lsd/docs"
)

func TestGetDocumentationExtractsEverything(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "docs") // does not exist yet
	var out bytes.Buffer
	if err := extractDocumentation(dir, false, &out); err != nil {
		t.Fatal(err)
	}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		return string(b)
	}
	if !strings.HasPrefix(read("install.pdf"), "%PDF-") {
		t.Error("install.pdf is not a PDF")
	}
	if !strings.Contains(read("install.md"), "# Linux Simple Diagnostic installation guide") {
		t.Error("install.md has the wrong content")
	}
	if !strings.Contains(read("systemd/lsd-server.service"), "ExecStart=/opt/lsd/server/lsd server") {
		t.Error("server unit looks wrong")
	}
	if !strings.Contains(read("systemd/lsd-agent.service"), "AmbientCapabilities=CAP_BPF") {
		t.Error("agent unit looks wrong")
	}
	if !strings.Contains(read("config/agent.env"), "LSD_SERVER=") || !strings.Contains(read("config/server.env"), "LSD_RETENTION") {
		t.Error("example configs look wrong")
	}
	if got := strings.Count(out.String(), "wrote "); got != len(bundledFiles()) {
		t.Errorf("reported %d files, want %d", got, len(bundledFiles()))
	}
}

func TestGetDocumentationNeverOverwritesWithoutForce(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "install.md")
	if err := os.WriteFile(existing, []byte("my own notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := extractDocumentation(dir, false, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "install.md") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("want a refusal naming install.md and --force, got %v", err)
	}
	if b, _ := os.ReadFile(existing); string(b) != "my own notes" {
		t.Fatal("the existing file was modified")
	}
	if _, err := os.Stat(filepath.Join(dir, "install.pdf")); err == nil {
		t.Fatal("a refusal must not write anything")
	}
	if err := extractDocumentation(dir, true, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(existing); string(b) == "my own notes" {
		t.Fatal("--force should overwrite")
	}
}

func TestGetDocumentationCommand(t *testing.T) {
	c := getDocumentationCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{}) // --directory is required
	if err := c.Execute(); err == nil {
		t.Fatal("expected an error without --directory")
	}
	c = getDocumentationCmd()
	c.SetOut(&out)
	c.SetArgs([]string{"--directory", t.TempDir()})
	if err := c.Execute(); err != nil || !strings.Contains(out.String(), "install.pdf") {
		t.Fatalf("err=%v out=%q", err, out.String())
	}
}

// The guide must not drift from the code: every LSD_* variable and --flag it mentions has to exist,
// and every variable / agent+server flag the code has has to be documented.
func TestInstallGuideMatchesTheCode(t *testing.T) {
	md, err := fs.ReadFile(docs.Files, "install.md")
	if err != nil {
		t.Fatal(err)
	}
	guide := string(md)
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}

	envs := map[string]bool{}
	for _, m := range regexp.MustCompile(`env(?:Bool|Dur|List)?\("([A-Z_]+)"`).FindAllStringSubmatch(string(src), -1) {
		envs["LSD_"+m[1]] = true
	}
	if len(envs) < 15 {
		t.Fatalf("only found %d env vars in main.go, the pattern is probably stale", len(envs))
	}
	for _, v := range regexp.MustCompile(`\bLSD_[A-Z_]+\b`).FindAllString(guide, -1) {
		if !envs[v] {
			t.Errorf("install.md mentions %s, which the program does not read", v)
		}
	}
	for v := range envs {
		if !strings.Contains(guide, v) {
			t.Errorf("install.md does not document %s", v)
		}
	}

	var logLevel string
	known, documented := map[string]bool{"log-level": true}, map[string]bool{"log-level": true}
	for name, c := range map[string]*cobra.Command{
		"agent": agentCmd(&logLevel), "server": serverCmd(&logLevel), "loadgen": loadgenCmd(&logLevel),
		"get-documentation": getDocumentationCmd(), "healthcheck": healthcheckCmd(),
	} {
		c.Flags().VisitAll(func(f *pflag.Flag) {
			known[f.Name] = true
			if name == "agent" || name == "server" || name == "get-documentation" {
				documented[f.Name] = true
			}
		})
	}
	// flags of other tools that the guide shows: docker compose, useradd, systemctl
	foreign := map[string]bool{"build": true, "system": true, "no-create-home": true, "shell": true, "now": true}
	for _, f := range regexp.MustCompile(`--[a-z][a-z-]*`).FindAllString(guide, -1) {
		if name := strings.TrimPrefix(f, "--"); !known[name] && !foreign[name] {
			t.Errorf("install.md mentions %s, which is not a flag of this program", f)
		}
	}
	for name := range documented {
		if !strings.Contains(guide, "--"+name) {
			t.Errorf("install.md does not document --%s", name)
		}
	}
}
