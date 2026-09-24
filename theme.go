package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/lipgloss"
)

// palette is herdr's theme: the tokens of its Palette (src/app/state.rs). A
// colour is "#rrggbb", an ANSI colour number, or "" for Reset: the
// terminal's own colour, as herdr's "terminal" theme mostly is.
type palette struct {
	Accent, PanelBG, SidebarBG, ActiveRowBG, SelectionBG     string
	Surface0, Surface1, SurfaceDim, Overlay0, Overlay1, Text string
	Subtext0, Mauve, Green, Yellow, Red, Blue, Teal, Peach   string
}

// tokens maps herdr's config names ([theme.custom]) to the palette's fields.
func (p *palette) tokens() map[string]*string {
	return map[string]*string{
		"accent": &p.Accent, "panel_bg": &p.PanelBG, "sidebar_bg": &p.SidebarBG,
		"active_row_bg": &p.ActiveRowBG, "selection_bg": &p.SelectionBG, "surface0": &p.Surface0,
		"surface1": &p.Surface1, "surface_dim": &p.SurfaceDim, "overlay0": &p.Overlay0,
		"overlay1": &p.Overlay1, "text": &p.Text, "subtext0": &p.Subtext0, "mauve": &p.Mauve,
		"green": &p.Green, "yellow": &p.Yellow, "red": &p.Red, "blue": &p.Blue, "teal": &p.Teal,
		"peach": &p.Peach,
	}
}

// themeTokens is herdr's CustomThemeColors / ModeThemeColors: every token an
// optional string, so a value of another type fails the whole file, as it
// does for herdr.
type themeTokens struct {
	Accent      *string `toml:"accent"`
	PanelBG     *string `toml:"panel_bg"`
	SidebarBG   *string `toml:"sidebar_bg"`
	ActiveRowBG *string `toml:"active_row_bg"`
	SelectionBG *string `toml:"selection_bg"`
	Surface0    *string `toml:"surface0"`
	Surface1    *string `toml:"surface1"`
	SurfaceDim  *string `toml:"surface_dim"`
	Overlay0    *string `toml:"overlay0"`
	Overlay1    *string `toml:"overlay1"`
	Text        *string `toml:"text"`
	Subtext0    *string `toml:"subtext0"`
	Mauve       *string `toml:"mauve"`
	Green       *string `toml:"green"`
	Yellow      *string `toml:"yellow"`
	Red         *string `toml:"red"`
	Blue        *string `toml:"blue"`
	Teal        *string `toml:"teal"`
	Peach       *string `toml:"peach"`
}

type customTheme struct {
	themeTokens
	Light *themeTokens `toml:"light"`
	Dark  *themeTokens `toml:"dark"`
}

// herdrConfig is the part of herdr's config.toml the picker reads.
type herdrConfig struct {
	Theme struct {
		Name       *string      `toml:"name"`
		AutoSwitch bool         `toml:"auto_switch"`
		DarkName   *string      `toml:"dark_name"`
		LightName  *string      `toml:"light_name"`
		Custom     *customTheme `toml:"custom"`
	}
	UI struct {
		Accent *string `toml:"accent"`
	}
}

// herdrConfigPath is where herdr reads its config (src/config/io.rs): a set
// variable counts even when it's empty, as it does for herdr.
func herdrConfigPath() string {
	if p, ok := os.LookupEnv("HERDR_CONFIG_PATH"); ok {
		return p
	}
	if dir, ok := os.LookupEnv("XDG_CONFIG_HOME"); ok {
		return filepath.Join(dir, "herdr", "config.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "herdr", "config.toml")
}

// readHerdrConfig reads config.toml as a running herdr does on a reload
// (src/config/io.rs load_live_config_from_str): [theme] and [ui] each on
// their own, so a bad value in one section doesn't discard the other, and a
// bad [ui] only loses the legacy accent. Missing, unparseable or invalid
// means herdr's defaults. herdr drops a byte-order mark at the start of any
// line before parsing; so does this.
//
// herdr can still differ: on a reload it keeps the last good theme when the
// file or [theme] turns invalid, and at startup any invalid section means
// defaults throughout. The picker can't see either, so it goes by the file.
func readHerdrConfig() herdrConfig {
	var cfg herdrConfig
	data, err := os.ReadFile(herdrConfigPath())
	if err != nil {
		return cfg
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, "\ufeff")
	}
	var sections map[string]toml.Primitive
	md, err := toml.Decode(strings.Join(lines, "\n"), &sections)
	if err != nil {
		return cfg
	}
	if s, ok := sections["theme"]; ok {
		if err := md.PrimitiveDecode(s, &cfg.Theme); err != nil {
			cfg.Theme = herdrConfig{}.Theme
		}
	}
	if s, ok := sections["ui"]; ok {
		if err := md.PrimitiveDecode(s, &cfg.UI); err != nil {
			cfg.UI = herdrConfig{}.UI
		}
	}
	return cfg
}

// herdrTheme resolves the palette herdr itself shows, as herdr does
// (src/app/mod.rs resolve_effective_theme): the named theme, or with
// auto_switch the dark or light one for the terminal's appearance; then
// [theme.custom], a non-default ui.accent, and with auto_switch
// [theme.custom.dark] or [theme.custom.light] on top.
func herdrTheme(dark bool) palette {
	cfg := readHerdrConfig()
	t := cfg.Theme
	manual := "catppuccin"
	if t.Name != nil {
		manual = *t.Name
	}
	custom := t.Custom
	if custom == nil {
		custom = &customTheme{}
	}
	name, fallback := manual, "catppuccin"
	var mode *themeTokens
	if t.AutoSwitch {
		siblingDark, siblingLight := siblingThemeNames(manual)
		if dark {
			name, mode = orDefault(t.DarkName, siblingDark), custom.Dark
		} else {
			name, fallback, mode = orDefault(t.LightName, siblingLight), "catppuccin-latte", custom.Light
		}
	}
	p, ok := herdrPalettes[canonicalThemeName(name)]
	if !ok {
		p = herdrPalettes[fallback]
	}
	p.override(&custom.themeTokens)
	if a := cfg.UI.Accent; a != nil && *a != "cyan" && custom.Accent == nil {
		p.Accent = parseColor(*a)
	}
	p.override(mode)
	return p
}

// pickerTheme is the palette the picker uses, or nil for its own colours.
// herdr paints its own panels with the theme's background, but the picker
// draws on the terminal's, so a light theme on a dark terminal (or the other
// way round) would put dark text on dark: then the picker keeps its own.
func pickerTheme(dark bool) *palette {
	p := herdrTheme(dark)
	if light, known := isLight(p.PanelBG); known && light == dark {
		return nil
	}
	return &p
}

// ansiRGB is xterm's default RGB for the 16 ANSI colours: the best guess at
// what a named colour looks like, since each terminal sets its own.
var ansiRGB = [16]string{
	"#000000", "#cd0000", "#00cd00", "#cdcd00", "#0000ee", "#cd00cd", "#00cdcd", "#e5e5e5",
	"#7f7f7f", "#ff0000", "#00ff00", "#ffff00", "#5c5cff", "#ff00ff", "#00ffff", "#ffffff",
}

// isLight tells whether a background is light: whether black text reads
// better on it than white, by relative luminance (WCAG), which puts the line
// at about 0.18 rather than halfway. An ANSI colour is taken as xterm's;
// Reset (the terminal's own) is unknown.
func isLight(c string) (light, known bool) {
	if n, err := strconv.Atoi(c); err == nil && n >= 0 && n < len(ansiRGB) {
		c = ansiRGB[n]
	}
	if len(c) != 7 || c[0] != '#' {
		return false, false
	}
	v, err := strconv.ParseUint(c[1:], 16, 32)
	if err != nil {
		return false, false
	}
	linear := func(u uint64) float64 {
		s := float64(u&0xff) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	l := 0.2126*linear(v>>16) + 0.7152*linear(v>>8) + 0.0722*linear(v)
	// contrast with black, (l+0.05)/0.05, beats contrast with white, 1.05/(l+0.05)
	return (l+0.05)*(l+0.05) > 0.05*1.05, true
}

// override applies the tokens that are set.
func (p *palette) override(t *themeTokens) {
	if t == nil {
		return
	}
	fields := p.tokens()
	v, ty := reflect.ValueOf(t).Elem(), reflect.TypeOf(*t)
	for i := range v.NumField() {
		if s := v.Field(i).Interface().(*string); s != nil {
			*fields[ty.Field(i).Tag.Get("toml")] = parseColor(*s)
		}
	}
}

func orDefault(s *string, def string) string {
	if s != nil {
		return *s
	}
	return def
}

func normalizeThemeName(name string) string {
	return strings.NewReplacer(" ", "-", "_", "-").Replace(strings.ToLower(name))
}

// themeAliases is herdr's canonical_theme_name (src/config/theme.rs).
var themeAliases = map[string]string{
	"catppuccin-mocha": "catppuccin", "latte": "catppuccin-latte", "light": "catppuccin-latte",
	"tokyonight": "tokyo-night", "tokyo-day": "tokyo-night-day", "tokyonight-day": "tokyo-night-day",
	"gruvbox-dark": "gruvbox", "onedark": "one-dark", "onelight": "one-light",
	"solarized-dark": "solarized", "lotus": "kanagawa-lotus", "rosepine": "rose-pine",
	"rosepine-dawn": "rose-pine-dawn", "dawn": "rose-pine-dawn",
}

func canonicalThemeName(name string) string {
	n := normalizeThemeName(name)
	if alias, ok := themeAliases[n]; ok {
		return alias
	}
	return n
}

// siblingThemeNames is herdr's pairing of a theme with its light or dark
// counterpart, the defaults for dark_name and light_name.
func siblingThemeNames(name string) (string, string) {
	for _, pair := range [][2]string{
		{"catppuccin", "catppuccin-latte"}, {"tokyo-night", "tokyo-night-day"}, {"gruvbox", "gruvbox-light"},
		{"one-dark", "one-light"}, {"solarized", "solarized-light"}, {"kanagawa", "kanagawa-lotus"},
		{"rose-pine", "rose-pine-dawn"},
	} {
		if c := canonicalThemeName(name); c == pair[0] || c == pair[1] {
			return pair[0], pair[1]
		}
	}
	return name, name
}

var namedColors = map[string]string{
	"black": "0", "red": "1", "green": "2", "yellow": "3", "blue": "4", "magenta": "5", "purple": "5",
	"cyan": "6", "gray": "7", "grey": "7", "darkgray": "8", "darkgrey": "8", "lightred": "9",
	"lightgreen": "10", "lightyellow": "11", "lightblue": "12", "lightmagenta": "13", "lightcyan": "14",
	"white": "15",
}

// parseU8 is Rust's u8::from_str_radix: one leading "+" is allowed.
func parseU8(s string, base int) (uint64, bool) {
	if len(s) > 1 && s[0] == '+' {
		s = s[1:]
	}
	if s == "" || s[0] == '+' || s[0] == '-' {
		return 0, false
	}
	n, err := strconv.ParseUint(s, base, 8)
	return n, err == nil
}

// parseColor reads a colour as herdr does (src/config/theme.rs parse_color):
// #rrggbb, #rgb, rgb(r, g, b), a name, or reset/default/none/transparent
// (""). herdr shows anything else as cyan, so the picker does too.
func parseColor(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "reset", "default", "none", "transparent":
		return ""
	}
	if hex, ok := strings.CutPrefix(s, "#"); ok {
		switch len(hex) {
		case 6:
			r, okR := parseU8(hex[0:2], 16)
			g, okG := parseU8(hex[2:4], 16)
			b, okB := parseU8(hex[4:6], 16)
			if okR && okG && okB {
				return fmt.Sprintf("#%02x%02x%02x", r, g, b)
			}
		case 3:
			var rgb [3]uint64
			valid := true
			for i := range 3 {
				n, ok := parseU8(hex[i:i+1], 16)
				rgb[i], valid = n*17, valid && ok
			}
			if valid {
				return fmt.Sprintf("#%02x%02x%02x", rgb[0], rgb[1], rgb[2])
			}
		}
	}
	if inner, ok := strings.CutPrefix(s, "rgb("); ok {
		if inner, ok := strings.CutSuffix(inner, ")"); ok {
			if parts := strings.Split(inner, ","); len(parts) == 3 {
				var rgb [3]uint64
				valid := true
				for i, part := range parts {
					n, ok := parseU8(strings.TrimSpace(part), 10)
					rgb[i], valid = n, valid && ok
				}
				if valid {
					return fmt.Sprintf("#%02x%02x%02x", rgb[0], rgb[1], rgb[2])
				}
			}
		}
	}
	if c, ok := namedColors[s]; ok {
		return c
	}
	return "6"
}

// theme is the palette in use, nil for the picker's own colours; newMarkdown
// reads it for the Markdown colours.
var theme *palette

// useTheme recolours the picker with p, or with nil restores its own
// colours. A Reset colour is the terminal's own; only the selection keeps
// the picker's background then, so the selected row stays visible. Linear's
// own colours (workflow states, project status, labels) stay Linear's.
func useTheme(p *palette) {
	theme = p
	styleDim, styleHeader, styleTabOn, styleHintHot = defaultStyleDim, defaultStyleHeader, defaultStyleTabOn, defaultStyleHintHot
	styleTree, styleErr, styleUrgent, styleOK = defaultStyleTree, defaultStyleErr, defaultStyleUrgent, defaultStyleOK
	styleSelected = defaultStyleSelected
	if p == nil {
		return
	}
	fg := func(s lipgloss.Style, c string) lipgloss.Style {
		if c == "" {
			return s.Foreground(lipgloss.NoColor{})
		}
		return s.Foreground(lipgloss.Color(c))
	}
	styleDim = fg(styleDim, p.Overlay0)
	styleHeader = fg(styleHeader, p.Text)
	styleTabOn = fg(styleTabOn, p.Accent)
	styleHintHot = fg(styleHintHot, p.Accent)
	styleTree = fg(styleTree, p.Accent)
	styleErr = fg(styleErr, p.Red)
	styleUrgent = fg(styleUrgent, p.Peach)
	styleOK = fg(styleOK, p.Green)
	if p.SelectionBG != "" {
		styleSelected = styleSelected.Background(lipgloss.Color(p.SelectionBG))
	}
}
