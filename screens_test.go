package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestWriteDemoScreens saves each README screen, as the popup draws it, from
// the demo workspace. It only runs for scripts/screenshots.sh, which sets
// SCREENS_DIR and turns the files into PNGs with freeze.
func TestWriteDemoScreens(t *testing.T) {
	dir := os.Getenv("SCREENS_DIR")
	if dir == "" {
		t.Skip("set SCREENS_DIR to write the README screenshots")
	}
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.ANSI256) })
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	save := func(name string, m model) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(m.View()+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// herdr's default theme, which freeze's background matches.
	catppuccin := herdrPalettes["catppuccin"]
	useTheme(&catppuccin)
	t.Cleanup(func() { useTheme(nil) })
	m := demoModel(t)
	m.width, m.height = 104, 30
	m.md = newMarkdown(true)
	m = drain(m, m.loadIssues())

	save("issues", m)

	filtered := m
	for _, r := range "search" {
		filtered = key(filtered, string(r))
	}
	save("filter", filtered)

	issue := press(press(m, "down"), "enter") // HAL-231: urgent, labels, a rich description
	save("issue", issue)
	save("status", press(issue, "c"))

	projects := press(m, "tab")
	save("projects", projects)
	project := press(projects, "enter")
	save("project", project)
	save("project-issues", press(project, "i"))
}
