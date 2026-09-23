package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
)

// markdown renders Linear's Markdown (descriptions, project content) for the
// terminal. Rendering runs chroma for code blocks, so results are cached per
// text and width; the cache is a map, shared by every copy of the model.
type markdown struct {
	dark  bool
	cache map[string][]string
}

func newMarkdown(dark bool) *markdown {
	return &markdown{dark: dark, cache: map[string][]string{}}
}

func (md *markdown) lines(key, text string, width int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	ck := fmt.Sprintf("%s\x00%d\x00%d", key, width, len(text))
	if l, ok := md.cache[ck]; ok {
		return l
	}
	l := md.render(text, width)
	md.cache[ck] = l
	return l
}

func (md *markdown) render(text string, width int) []string {
	cfg := styles.DarkStyleConfig
	if !md.dark {
		cfg = styles.LightStyleConfig
	}
	// One column of margin lines the text up with the fields above it.
	margin := uint(1)
	cfg.Document.Margin = &margin
	cfg.Document.BlockPrefix, cfg.Document.BlockSuffix = "", ""
	// Headings are already bold and coloured; the "## " markers are noise here.
	for _, h := range []*string{&cfg.H2.Prefix, &cfg.H3.Prefix, &cfg.H4.Prefix, &cfg.H5.Prefix, &cfg.H6.Prefix} {
		*h = ""
	}

	r, err := glamour.NewTermRenderer(glamour.WithStyles(cfg), glamour.WithWordWrap(max(10, width-2)))
	if err == nil {
		var out string
		if out, err = r.Render(text); err == nil {
			return trimBlankEdges(strings.Split(out, "\n"))
		}
	}
	// Unrenderable: show the source, wrapped.
	return strings.Split(lipgloss.NewStyle().Width(max(10, width-1)).PaddingLeft(1).Render(text), "\n")
}

func trimBlankEdges(lines []string) []string {
	blank := func(s string) bool { return strings.TrimSpace(stripStyles(s)) == "" }
	for len(lines) > 0 && blank(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && blank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// stripStyles drops ANSI escapes: what the line shows.
func stripStyles(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			esc = true
		case esc && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'):
			esc = false
		case !esc:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// window shows lines[scroll:] in room lines. When it doesn't all fit, the last
// line says how to see the rest.
func window(lines []string, scroll, room int) string {
	if room <= 0 || len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	if len(lines) <= room {
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
		return b.String()
	}
	vis := room - 1
	scroll = min(max(0, scroll), len(lines)-vis)
	for _, l := range lines[scroll : scroll+vis] {
		b.WriteString(l + "\n")
	}
	if scroll+vis < len(lines) {
		b.WriteString(styleDim.Render(fmt.Sprintf(" ↓ %d more lines · scroll or ↓", len(lines)-scroll-vis)) + "\n")
	} else {
		b.WriteString(styleDim.Render(" ↑ end · scroll or ↑") + "\n")
	}
	return b.String()
}

// maxScroll is how far window can scroll lines in room.
func maxScroll(lines []string, room int) int {
	if len(lines) <= room || room <= 1 {
		return 0
	}
	return len(lines) - (room - 1)
}
