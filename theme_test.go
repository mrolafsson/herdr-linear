package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
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

func pal(name string) *palette {
	p := herdrPalettes[name]
	return &p
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
	// Spot values, copied by hand from herdr's state.rs: the generator's
	// RGB, named-colour and Reset handling.
	for _, c := range []struct{ got, want string }{
		{herdrPalettes["dracula"].Accent, "#bd93f9"},           // Rgb(189, 147, 249)
		{herdrPalettes["dracula"].SelectionBG, "#463f5d"},      // Rgb(70, 63, 93)
		{herdrPalettes["dracula"].SidebarBG, ""},               // Reset
		{herdrPalettes["catppuccin-latte"].PanelBG, "#eff1f5"}, // Rgb(239, 241, 245)
		{herdrPalettes["terminal"].Accent, "4"},                // Blue
		{herdrPalettes["terminal"].Overlay1, "15"},             // White
		{herdrPalettes["terminal"].ActiveRowBG, "8"},           // DarkGray
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestEveryConfigTokenReachesThePalette(t *testing.T) {
	fields := (&palette{}).tokens()
	ty := reflect.TypeOf(themeTokens{})
	if ty.NumField() != len(fields) || len(fields) != reflect.TypeOf(palette{}).NumField() {
		t.Fatalf("%d config tokens, %d mapped, %d palette fields", ty.NumField(), len(fields), reflect.TypeOf(palette{}).NumField())
	}
	for i := range ty.NumField() {
		if fields[ty.Field(i).Tag.Get("toml")] == nil {
			t.Errorf("token %s has no palette field", ty.Field(i).Tag.Get("toml"))
		}
	}
}

func TestParseColorMatchesHerdr(t *testing.T) {
	for in, want := range map[string]string{
		"#FF79C6": "#ff79c6", " #abc ": "#aabbcc", "rgb(255, 85, 85)": "#ff5555", "RGB(1,2,3)": "#010203",
		"reset": "", "Transparent": "", "none": "", "default": "",
		"red": "1", "purple": "5", "Grey": "7", "darkgray": "8", "lightcyan": "14", "white": "15",
		// Rust's u8 parser takes one leading +, per component
		"rgb(+1,2,3)": "#010203", "#+1+2+3": "#010203", "rgb(++1,2,3)": "6", "#+ab": "6",
		// herdr shows anything it can't read as cyan
		"#12345": "6", "#ggg": "6", "rgb(256,0,0)": "6", "rgb(1,2)": "6", "rgb(-1,2,3)": "6", "chartreuse": "6", "": "6",
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
	with := func(name string, f func(*palette)) func() palette {
		return func() palette { p := herdrPalettes[name]; f(&p); return p }
	}
	is := func(name string) func() palette { return with(name, func(*palette) {}) }
	cases := []struct {
		name, toml string
		dark       bool
		want       func() palette
	}{
		{"no config: herdr's default", "", true, is("catppuccin")},
		{"a named theme", "[theme]\nname = \"dracula\"\n", false, is("dracula")},
		{"an alias", "[theme]\nname = \"TokyoNight\"\n", true, is("tokyo-night")},
		{"an unknown name", "[theme]\nname = \"tokio\"\n", true, is("catppuccin")},
		{"broken toml: defaults, as herdr", "[theme\nname = \"dracula\"", true, is("catppuccin")},
		// Round 2: an untyped read kept Dracula and skipped just the bad value.
		{"a token that isn't a string: defaults, as herdr", "[theme]\nname = \"dracula\"\n[theme.custom]\nred = 5\n", true, is("catppuccin")},
		{"a name that isn't a string", "[theme]\nname = 5\n", true, is("catppuccin")},
		{"a BOM starting a later line, which herdr drops", "[theme]\n\ufeffname = \"nord\"\n", true, is("nord")},
		{"unknown keys are fine", "[theme]\nname = \"nord\"\nshiny = true\n[other]\nx = 1\n", true, is("nord")},
		{"auto_switch, light terminal: the sibling", "[theme]\nname = \"gruvbox\"\nauto_switch = true\n", false, is("gruvbox-light")},
		{"auto_switch, dark terminal", "[theme]\nname = \"gruvbox-light\"\nauto_switch = true\n", true, is("gruvbox")},
		{"auto_switch names win", "[theme]\nauto_switch = true\ndark_name = \"nord\"\nlight_name = \"one-light\"\n", false, is("one-light")},
		{"auto_switch, unknown light name", "[theme]\nauto_switch = true\nlight_name = \"lattee\"\n", false, is("catppuccin-latte")},
		{"custom tokens on top", "[theme]\nname = \"nord\"\n[theme.custom]\naccent = \"#f5c2e7\"\nred = \"rgb(255, 85, 85)\"\n", true,
			with("nord", func(p *palette) { p.Accent, p.Red = "#f5c2e7", "#ff5555" })},
		{"mode overrides only with auto_switch", "[theme.custom.dark]\naccent = \"#010203\"\n", true, is("catppuccin")},
		{"mode overrides last", "[theme]\nauto_switch = true\n[theme.custom]\naccent = \"#111111\"\n[theme.custom.light]\naccent = \"#222222\"\n", false,
			with("catppuccin-latte", func(p *palette) { p.Accent = "#222222" })},
		{"the other mode's overrides don't apply", "[theme]\nauto_switch = true\n[theme.custom.light]\naccent = \"#222222\"\n", true, is("catppuccin")},
		{"dark mode overrides", "[theme]\nauto_switch = true\n[theme.custom.dark]\nred = \"#333333\"\n", true,
			with("catppuccin", func(p *palette) { p.Red = "#333333" })},
		// Round 3: herdr reloads [theme] and [ui] separately, so a bad [ui]
		// loses only the legacy accent.
		{"a bad [ui] keeps the theme", "[theme]\nname = \"dracula\"\n[ui]\naccent = 5\n", true, is("dracula")},
		{"a bad [theme] keeps ui.accent", "[theme]\nname = 5\n[ui]\naccent = \"magenta\"\n", true,
			with("catppuccin", func(p *palette) { p.Accent = "5" })},
		{"legacy ui.accent", "[ui]\naccent = \"magenta\"\n", true, with("catppuccin", func(p *palette) { p.Accent = "5" })},
		{"ui.accent loses to custom.accent", "[ui]\naccent = \"magenta\"\n[theme.custom]\naccent = \"#abcdef\"\n", true,
			with("catppuccin", func(p *palette) { p.Accent = "#abcdef" })},
		{"ui.accent cyan is the default, not an override", "[ui]\naccent = \"cyan\"\n", true, is("catppuccin")},
		{"reset clears a token", "[theme.custom]\nselection_bg = \"reset\"\n", true, with("catppuccin", func(p *palette) { p.SelectionBG = "" })},
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
	home, _ := os.UserHomeDir()
	t.Setenv("HERDR_CONFIG_PATH", "") // registered, so both are restored after the test
	os.Unsetenv("HERDR_CONFIG_PATH")  //nolint:errcheck
	t.Setenv("XDG_CONFIG_HOME", "/x")
	if got := herdrConfigPath(); got != "/x/herdr/config.toml" {
		t.Errorf("got %q", got)
	}
	// set but empty still counts, as for herdr's env::var
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := herdrConfigPath(); got != "herdr/config.toml" {
		t.Errorf("empty XDG_CONFIG_HOME: got %q", got)
	}
	os.Unsetenv("XDG_CONFIG_HOME") //nolint:errcheck
	if got := herdrConfigPath(); got != filepath.Join(home, ".config/herdr/config.toml") {
		t.Errorf("got %q", got)
	}
	t.Setenv("HERDR_CONFIG_PATH", "/y/c.toml")
	if got := herdrConfigPath(); got != "/y/c.toml" {
		t.Errorf("got %q", got)
	}
}

func TestAThemeForTheOtherBackgroundIsNotUsed(t *testing.T) {
	// Round 2: herdr paints its panels with the theme's background; the
	// picker draws on the terminal's. Latte's dark text on a dark terminal
	// would be unreadable, so the picker keeps its own colours there.
	withHerdrConfig(t, "[theme]\nname = \"catppuccin-latte\"\n")
	if pickerTheme(true) != nil {
		t.Error("a light theme on a dark terminal")
	}
	if p := pickerTheme(false); p == nil || *p != herdrPalettes["catppuccin-latte"] {
		t.Error("a light theme on a light terminal")
	}
	withHerdrConfig(t, "[theme]\nname = \"dracula\"\n")
	if pickerTheme(false) != nil || pickerTheme(true) == nil {
		t.Error("dracula: only on a dark terminal")
	}
	withHerdrConfig(t, "[theme]\nname = \"terminal\"\n") // no background of its own: fits any
	if pickerTheme(false) == nil || pickerTheme(true) == nil {
		t.Error("the terminal theme fits any terminal")
	}
	withHerdrConfig(t, "[theme]\nname = \"gruvbox\"\nauto_switch = true\n") // herdr picks by appearance
	if pickerTheme(false) == nil || pickerTheme(true) == nil {
		t.Error("auto_switch always fits")
	}
	// Round 3: a named background used to count as unknown, so it always applied.
	withHerdrConfig(t, "[theme]\nname = \"catppuccin-latte\"\n[theme.custom]\npanel_bg = \"black\"\n")
	if pickerTheme(false) != nil || pickerTheme(true) == nil {
		t.Error("latte with a black panel_bg: only on a dark terminal")
	}
}

func TestEveryPaletteIsTheLightnessItsNameSays(t *testing.T) {
	light := map[string]bool{"catppuccin-latte": true, "gruvbox-light": true, "kanagawa-lotus": true,
		"one-light": true, "rose-pine-dawn": true, "solarized-light": true, "tokyo-night-day": true}
	for name, p := range herdrPalettes {
		got, known := isLight(p.PanelBG)
		if name == "terminal" {
			if known {
				t.Error("the terminal theme's background is the terminal's own: unknown")
			}
			continue
		}
		if !known || got != light[name] {
			t.Errorf("%s (%s): light %v, known %v", name, p.PanelBG, got, known)
		}
	}
}

func TestIsLight(t *testing.T) {
	for c, want := range map[string]bool{
		"#ffffff": true, "#000000": false,
		// Round 3: black text reads better on mid grey, so it's light; the
		// old halfway cut on gamma-encoded values called #7a7a7a dark.
		"#808080": true, "#7a7a7a": true, "#595959": false,
		// ANSI colours, as xterm's
		"15": true, "7": true, "0": false, "4": false, "8": true,
	} {
		if got, known := isLight(c); !known || got != want {
			t.Errorf("isLight(%q) = %v, %v; want %v", c, got, known, want)
		}
	}
	if _, known := isLight(""); known {
		t.Error("Reset is unknown")
	}
}

func TestUseThemeRecoloursTheStyles(t *testing.T) {
	t.Cleanup(func() { useTheme(nil) })
	p := pal("dracula")
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
	useTheme(nil)
	if styleErr.GetForeground() != defaultStyleErr.GetForeground() || theme != nil {
		t.Error("nil didn't restore the picker's own colours")
	}
}

func TestResetIsTheTerminalsColour(t *testing.T) {
	// Round 2: Reset kept the picker's old colours instead. It means the
	// terminal's own, as in herdr; only the selection keeps a background so
	// the selected row stays visible.
	t.Cleanup(func() { useTheme(nil) })
	p := pal("dracula")
	p.Red, p.Text, p.SelectionBG = "", "", ""
	useTheme(p)
	if styleErr.GetForeground() != (lipgloss.NoColor{}) || styleHeader.GetForeground() != (lipgloss.NoColor{}) {
		t.Error("a Reset foreground kept a colour")
	}
	if styleSelected.GetBackground() != defaultStyleSelected.GetBackground() {
		t.Error("the selection lost its background")
	}
	useTheme(pal("terminal"))
	if styleTree.GetForeground() != lipgloss.Color("4") {
		t.Error("the terminal theme's ANSI accent wasn't used")
	}
}

func TestMarkdownTakesThePalette(t *testing.T) {
	p := pal("dracula")
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
	if c := styles.DarkStyleConfig.Document.Color; c == nil || *c == p.Text {
		t.Error("themeMarkdown wrote through glamour's shared style")
	}
	reset := pal("terminal")
	cfg = styles.DarkStyleConfig
	themeMarkdown(&cfg, reset)
	if cfg.Document.Color != nil || cfg.Code.BackgroundColor != nil {
		t.Error("a Reset colour kept glamour's")
	}
	cfg = styles.DarkStyleConfig
	themeMarkdown(&cfg, nil)
	if cfg.Document.Color != styles.DarkStyleConfig.Document.Color {
		t.Error("no theme changed glamour's colours")
	}
	var empty ansi.StyleConfig
	themeMarkdown(&empty, nil)
}

func TestMarkdownRendersInTheThemesColours(t *testing.T) {
	t.Cleanup(func() { useTheme(nil) })
	lipgloss.SetColorProfile(termenv.TrueColor) // so colours reach the output
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.ANSI256) })
	useTheme(pal("dracula"))
	md := newMarkdown(true)
	if md.pal == nil || *md.pal != herdrPalettes["dracula"] {
		t.Fatal("markdown didn't take the palette in use")
	}
	out := ""
	for _, l := range md.lines("k", "## Title\n\nSome `code`.", 60) {
		out += l
	}
	// dracula's accent #bd93f9 as a truecolor foreground
	if !strings.Contains(out, "38;2;189;147;249") {
		t.Errorf("heading not in the accent colour: %q", out)
	}
}
