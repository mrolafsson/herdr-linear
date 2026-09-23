package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
)

// withHerdrConfig points herdrTheme at a config.toml holding text ("" = none).
func withHerdrConfig(t *testing.T, text string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if text != "" {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HERDR_CONFIG_PATH", path)
}

func TestEveryHerdrThemeHasAPalette(t *testing.T) {
	// herdr 0.9.1's THEME_NAMES (src/config/theme.rs).
	for _, name := range []string{"catppuccin", "catppuccin-latte", "terminal", "tokyo-night", "tokyo-night-day",
		"dracula", "nord", "gruvbox", "gruvbox-light", "one-dark", "one-light", "solarized", "solarized-light",
		"kanagawa", "kanagawa-lotus", "rose-pine", "rose-pine-dawn", "vesper"} {
		if _, ok := herdrPalettes[name]; !ok {
			t.Errorf("no palette for %q", name)
		}
	}
	if len(herdrPalettes) != 18 {
		t.Errorf("%d palettes, want 18", len(herdrPalettes))
	}
}

func TestParseColorMatchesHerdr(t *testing.T) {
	for in, want := range map[string]string{
		"#FF79C6": "#ff79c6", " #abc ": "#aabbcc", "rgb(255, 85, 85)": "#ff5555", "RGB(1,2,3)": "#010203",
		"reset": "", "Transparent": "", "none": "", "default": "",
		"red": "1", "purple": "5", "Grey": "7", "darkgray": "8", "lightcyan": "14", "white": "15",
		// herdr shows anything it can't read as cyan
		"#12345": "6", "#ggg": "6", "rgb(256,0,0)": "6", "rgb(1,2)": "6", "chartreuse": "6", "": "6",
	} {
		if got := parseColor(in); got != want {
			t.Errorf("parseColor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestThemeNamesResolveAsHerdrDoes(t *testing.T) {
	for in, want := range map[string]string{
		"catppuccin-mocha": "catppuccin", "Latte": "catppuccin-latte", "light": "catppuccin-latte",
		"tokyo_night": "tokyo-night", "Tokyo Night Day": "tokyo-night-day", "dawn": "rose-pine-dawn",
		"lotus": "kanagawa-lotus", "onedark": "one-dark", "dracula": "dracula",
	} {
		if got := canonicalThemeName(in); got != want {
			t.Errorf("canonicalThemeName(%q) = %q, want %q", in, got, want)
		}
	}
	if d, l := siblingThemeNames("Latte"); d != "catppuccin" || l != "catppuccin-latte" {
		t.Errorf("siblings of latte: %q, %q", d, l)
	}
	if d, l := siblingThemeNames("dracula"); d != "dracula" || l != "dracula" {
		t.Errorf("a theme with no sibling is its own: %q, %q", d, l)
	}
}

func TestHerdrThemeFollowsConfigToml(t *testing.T) {
	cases := []struct {
		name, toml string
		dark       bool
		want       func() palette
	}{
		{"no config: herdr's default", "", true, func() palette { return herdrPalettes["catppuccin"] }},
		{"a named theme", "[theme]\nname = \"dracula\"\n", false, func() palette { return herdrPalettes["dracula"] }},
		{"an alias", "[theme]\nname = \"TokyoNight\"\n", true, func() palette { return herdrPalettes["tokyo-night"] }},
		{"an unknown name", "[theme]\nname = \"tokio\"\n", true, func() palette { return herdrPalettes["catppuccin"] }},
		{"broken toml: defaults, as herdr", "[theme\nname = \"dracula\"", true, func() palette { return herdrPalettes["catppuccin"] }},
		{"auto_switch, light terminal: the sibling", "[theme]\nname = \"gruvbox\"\nauto_switch = true\n", false,
			func() palette { return herdrPalettes["gruvbox-light"] }},
		{"auto_switch, dark terminal", "[theme]\nname = \"gruvbox-light\"\nauto_switch = true\n", true,
			func() palette { return herdrPalettes["gruvbox"] }},
		{"auto_switch names win", "[theme]\nauto_switch = true\ndark_name = \"nord\"\nlight_name = \"one-light\"\n", false,
			func() palette { return herdrPalettes["one-light"] }},
		{"auto_switch, unknown light name", "[theme]\nauto_switch = true\nlight_name = \"lattee\"\n", false,
			func() palette { return herdrPalettes["catppuccin-latte"] }},
		{"custom tokens on top", "[theme]\nname = \"nord\"\n[theme.custom]\naccent = \"#f5c2e7\"\nred = \"rgb(255, 85, 85)\"\n", true,
			func() palette { p := herdrPalettes["nord"]; p.Accent, p.Red = "#f5c2e7", "#ff5555"; return p }},
		{"mode overrides only with auto_switch", "[theme.custom.dark]\naccent = \"#010203\"\n", true,
			func() palette { return herdrPalettes["catppuccin"] }},
		{"mode overrides last", "[theme]\nauto_switch = true\n[theme.custom]\naccent = \"#111111\"\n[theme.custom.light]\naccent = \"#222222\"\n", false,
			func() palette { p := herdrPalettes["catppuccin-latte"]; p.Accent = "#222222"; return p }},
		{"the other mode's overrides don't apply", "[theme]\nauto_switch = true\n[theme.custom.light]\naccent = \"#222222\"\n", true,
			func() palette { return herdrPalettes["catppuccin"] }},
		{"legacy ui.accent", "[ui]\naccent = \"magenta\"\n", true,
			func() palette { p := herdrPalettes["catppuccin"]; p.Accent = "5"; return p }},
		{"ui.accent loses to custom.accent", "[ui]\naccent = \"magenta\"\n[theme.custom]\naccent = \"#abcdef\"\n", true,
			func() palette { p := herdrPalettes["catppuccin"]; p.Accent = "#abcdef"; return p }},
		{"ui.accent cyan is the default, not an override", "[ui]\naccent = \"cyan\"\n", true,
			func() palette { return herdrPalettes["catppuccin"] }},
		{"reset clears a token", "[theme.custom]\nselection_bg = \"reset\"\n", true,
			func() palette { p := herdrPalettes["catppuccin"]; p.SelectionBG = ""; return p }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withHerdrConfig(t, c.toml)
			if got, want := herdrTheme(c.dark), c.want(); got != want {
				t.Errorf("got  %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestHerdrConfigPath(t *testing.T) {
	t.Setenv("HERDR_CONFIG_PATH", "")
	t.Setenv("XDG_CONFIG_HOME", "/x")
	if got := herdrConfigPath(); got != "/x/herdr/config.toml" {
		t.Errorf("got %q", got)
	}
	t.Setenv("HERDR_CONFIG_PATH", "/y/c.toml")
	if got := herdrConfigPath(); got != "/y/c.toml" {
		t.Errorf("got %q", got)
	}
}

func TestUseThemeRecoloursTheStyles(t *testing.T) {
	t.Cleanup(func() { useTheme(palette{}) })
	p := herdrPalettes["dracula"]
	useTheme(p)
	for name, got := range map[string]lipgloss.TerminalColor{
		"dim": styleDim.GetForeground(), "header": styleHeader.GetForeground(), "tab": styleTabOn.GetForeground(),
		"hint": styleHintHot.GetForeground(), "tree": styleTree.GetForeground(), "err": styleErr.GetForeground(),
		"urgent": styleUrgent.GetForeground(), "ok": styleOK.GetForeground(), "selected": styleSelected.GetBackground(),
	} {
		want := map[string]string{"dim": p.Overlay0, "header": p.Text, "tab": p.Accent, "hint": p.Accent, "tree": p.Accent,
			"err": p.Red, "urgent": p.Peach, "ok": p.Green, "selected": p.SelectionBG}[name]
		if got != lipgloss.Color(want) {
			t.Errorf("%s: %v, want %s", name, got, want)
		}
	}
	if !styleUrgent.GetBold() || !styleTabOn.GetUnderline() {
		t.Error("recolouring dropped the styles' other attributes")
	}
}

func TestTheTerminalThemeKeepsThePickersOwnLook(t *testing.T) {
	// herdr's "terminal" theme leaves most colours to the terminal: those
	// keep the picker's defaults (a selection must stay visible).
	t.Cleanup(func() { useTheme(palette{}) })
	useTheme(herdrPalettes["terminal"])
	if styleSelected.GetBackground() != defaultStyleSelected.GetBackground() {
		t.Error("selection lost its background")
	}
	if styleHeader.GetForeground() != defaultStyleHeader.GetForeground() {
		t.Error("header recoloured by an unset colour")
	}
	if styleTree.GetForeground() != lipgloss.Color("4") {
		t.Error("the terminal theme's ANSI accent wasn't used")
	}
}

func TestMarkdownTakesThePalette(t *testing.T) {
	p := herdrPalettes["dracula"]
	cfg := styles.DarkStyleConfig
	themeMarkdown(&cfg, p)
	for name, got := range map[string]*string{
		"text": cfg.Document.Color, "h2": cfg.H2.Color, "code": cfg.Code.Color, "code bg": cfg.Code.BackgroundColor,
		"link": cfg.Link.Color, "quote": cfg.BlockQuote.Color,
	} {
		want := map[string]string{"text": p.Text, "h2": p.Accent, "code": p.Peach, "code bg": p.Surface0,
			"link": p.Blue, "quote": p.Subtext0}[name]
		if got == nil || *got != want {
			t.Errorf("%s: %v, want %s", name, got, want)
		}
	}
	if cfg.H1.BackgroundColor != nil {
		t.Error("H1 kept glamour's own background")
	}
	// glamour's shared default must be untouched: the next render (another
	// theme, or none) starts from it.
	if c := styles.DarkStyleConfig.Document.Color; c != nil && *c == p.Text {
		t.Error("themeMarkdown wrote through glamour's shared style")
	}
	var empty ansi.StyleConfig
	themeMarkdown(&empty, palette{})
	if empty.Document.Color != nil || empty.Code.Color != nil {
		t.Error("an unset palette colour replaced glamour's")
	}
}

func TestMarkdownRendersWithTheme(t *testing.T) {
	t.Cleanup(func() { useTheme(palette{}) })
	useTheme(herdrPalettes["dracula"])
	md := newMarkdown(true)
	if md.pal != herdrPalettes["dracula"] {
		t.Fatal("markdown didn't take the palette in use")
	}
	if l := md.lines("k", "# Title\n\nSome `code` and [a link](https://example.com).", 60); len(l) == 0 {
		t.Fatal("nothing rendered")
	}
}
