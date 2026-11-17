package docs

import (
	"bytes"
	"testing"
)

// The PDF is built with pdflatex (no system fonts, works on a stock Ubuntu 24.04), which cannot typeset
// arbitrary Unicode such as arrows or typographic dashes. Keep the guide plain ASCII.
func TestGuideIsPlainASCII(t *testing.T) {
	md, err := Files.ReadFile("install.md")
	if err != nil {
		t.Fatal(err)
	}
	line := 1
	for _, b := range md {
		if b == '\n' {
			line++
		}
		if b >= 0x80 {
			t.Fatalf("install.md line %d contains a non-ASCII byte: the pdflatex build would fail", line)
		}
	}
}

func TestEmbeddedGuide(t *testing.T) {
	md, err := Files.ReadFile("install.md")
	if err != nil || !bytes.Contains(md, []byte("# Linux Simple Diagnostic installation guide")) {
		t.Fatalf("install.md missing or wrong: %v", err)
	}
	pdf, err := Files.ReadFile("install.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || len(pdf) < 10_000 {
		t.Fatalf("install.pdf is not a real PDF (%d bytes): run `make docs`", len(pdf))
	}
}
