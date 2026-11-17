package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"simplemonitor/docs"
	"simplemonitor/packaging"
)

// bundled is one embedded file and where it lands in the target directory.
type bundled struct {
	fsys fs.FS
	src  string
	dest string // slash-separated, relative to the target directory
}

// bundledFiles lists everything `get-documentation` extracts.
func bundledFiles() []bundled {
	return []bundled{
		{docs.Files, "install.pdf", "install.pdf"},
		{docs.Files, "install.md", "install.md"},
		{packaging.Files, "server/simplemonitor-server.service", "systemd/simplemonitor-server.service"},
		{packaging.Files, "agent/simplemonitor-agent.service", "systemd/simplemonitor-agent.service"},
		{packaging.Files, "server/server.env", "config/server.env"},
		{packaging.Files, "agent/agent.env", "config/agent.env"},
	}
}

func getDocumentationCmd() *cobra.Command {
	var dir string
	var force bool
	c := &cobra.Command{
		Use:   "get-documentation",
		Short: "Extract the bundled installation guide (PDF + Markdown), systemd units and example configs",
		Long: `Extract the documentation that is built into this program into a directory:

  install.pdf, install.md          the installation and configuration guide
  systemd/*.service                the systemd units used by the Debian packages
  config/*.env                     the example configuration files

Existing files are never overwritten unless --force is given.`,
		Example: "  simplemonitor get-documentation --directory ./sm-docs",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return extractDocumentation(dir, force, cmd.OutOrStdout())
		},
	}
	c.Flags().StringVar(&dir, "directory", "", "directory to extract into (created if needed)")
	c.Flags().BoolVar(&force, "force", false, "overwrite files that already exist")
	_ = c.MarkFlagRequired("directory")
	return c
}

// extractDocumentation writes the bundled files below dir. It reads everything first and checks for
// existing files before writing anything, so a refusal leaves the directory untouched.
func extractDocumentation(dir string, force bool, out io.Writer) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("--directory is required")
	}
	files := bundledFiles()
	data := make([][]byte, len(files))
	for i, f := range files {
		b, err := fs.ReadFile(f.fsys, f.src)
		if err != nil {
			return fmt.Errorf("reading bundled %s: %w", f.src, err)
		}
		data[i] = b
	}

	if !force {
		var existing []string
		for _, f := range files {
			if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(f.dest))); err == nil {
				existing = append(existing, f.dest)
			}
		}
		if len(existing) > 0 {
			return fmt.Errorf("%s already contains %s: nothing was written (use --force to overwrite)",
				dir, strings.Join(existing, ", "))
		}
	}

	for i, f := range files {
		dest := filepath.Join(dir, filepath.FromSlash(f.dest))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(dest), err)
		}
		if err := os.WriteFile(dest, data[i], 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", dest, err)
		}
		fmt.Fprintf(out, "wrote %s\n", dest)
	}
	return nil
}
