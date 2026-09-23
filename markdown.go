package main

import (
	"crypto/sha256"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
)

// markdown renders Linear's Markdown (descriptions, project content) for the
// terminal. Rendering runs chroma for code blocks, so results are cached per
// text and width; the cache is a map, shared by every copy of the model.
type markdown struct {
	dark  bool
	pal   *palette
	cache map[string][]string
}

func newMarkdown(dark bool) *markdown {
	return &markdown{dark: dark, pal: theme, cache: map[string][]string{}}
}

// themeMarkdown colours glamour's style with herdr's palette (nil: glamour's
// own colours). A Reset colour clears glamour's, leaving the terminal's. Only
// fresh pointers are assigned: the style's own point into glamour's shared
// defaults. Fenced code keeps its syntax colours.
func themeMarkdown(cfg *ansi.StyleConfig, p *palette) {
	if p == nil {
		return
	}
	set := func(dst **string, c string) {
		if c == "" {
			*dst = nil
		} else {
			*dst = &c
		}
	}
	set(&cfg.Document.Color, p.Text)
	for _, h := range []*ansi.StyleBlock{&cfg.Heading, &cfg.H1, &cfg.H2, &cfg.H3, &cfg.H4, &cfg.H5, &cfg.H6} {
		set(&h.Color, p.Accent)
	}
	cfg.H1.BackgroundColor = nil // glamour's purple block would clash with the theme
	set(&cfg.Code.Color, p.Peach)
	set(&cfg.Code.BackgroundColor, p.Surface0)
	set(&cfg.Link.Color, p.Blue)
	set(&cfg.LinkText.Color, p.Accent)
	set(&cfg.BlockQuote.Color, p.Subtext0)
	set(&cfg.HorizontalRule.Color, p.Overlay0)
}

func (md *markdown) lines(key, text string, width int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	// Keyed by the text itself (hashed), so an edited description re-renders.
	ck := fmt.Sprintf("%s\x00%d\x00%x", key, width, sha256.Sum256([]byte(text)))
	if l, ok := md.cache[ck]; ok {
		return l
	}
	l := md.render(text, width)
	md.cache[ck] = l
	return l
}

// numericEntity matches an HTML character reference like &#27; or &#x1b;.
var numericEntity = regexp.MustCompile(`&#(?:[xX][0-9a-fA-F]+|[0-9]+);?`)

// withoutControlEntities drops character references that decode to control
// characters. The renderer decodes entities after we've cleaned the text, so
// &#27;[8m would come out as a real "hide this text" escape: styling a
// collaborator wrote, indistinguishable from the renderer's own by the time
// screenSafe sees it. Removing such entities up front means every escape in
// the output is one the renderer made.
func withoutControlEntities(s string) string {
	return numericEntity.ReplaceAllStringFunc(s, func(ref string) string {
		if strings.ContainsFunc(html.UnescapeString(ref), isControl) {
			return ""
		}
		return ref
	})
}

func (md *markdown) render(text string, width int) []string {
	text = withoutControlEntities(text)
	cfg := styles.DarkStyleConfig
	if !md.dark {
		cfg = styles.LightStyleConfig
	}
	themeMarkdown(&cfg, md.pal)
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
			// Glamour decodes HTML entities (&#27;), so its output is
			// checked again, not only its input.
			return trimBlankEdges(strings.Split(screenSafe(out), "\n"))
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
