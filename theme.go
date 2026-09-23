package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/lipgloss"
)

// palette is herdr's theme: the tokens of its Palette (src/app/state.rs). A
// colour is "#rrggbb", an ANSI colour number, or "" for none set, which
// leaves the picker's own default in place (herdr's "terminal" theme is
// mostly that, so it keeps the look the picker had before themes).
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

// herdrConfig is the part of herdr's config.toml the picker reads.
type herdrConfig struct {
	Theme struct {
		Name       *string        `toml:"name"`
		AutoSwitch bool           `toml:"auto_switch"`
		DarkName   *string        `toml:"dark_name"`
		LightName  *string        `toml:"light_name"`
		Custom     map[string]any `toml:"custom"`
	} `toml:"theme"`
	UI struct {
		Accent *string `toml:"accent"`
	} `toml:"ui"`
}

// herdrConfigPath is where herdr reads its config (src/config/io.rs).
func herdrConfigPath() string {
	if p := os.Getenv("HERDR_CONFIG_PATH"); p != "" {
		return p
	}
	return filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), "herdr", "config.toml")
}

// herdrTheme resolves the palette herdr itself shows, as herdr does
// (src/app/mod.rs resolve_effective_theme): the named theme, or with
// auto_switch the dark or light one for the terminal's appearance; then
// [theme.custom], a non-default ui.accent, and with auto_switch
// [theme.custom.dark] or [theme.custom.light] on top. A missing or invalid
// config.toml means herdr's defaults, as it does for herdr.
func herdrTheme(dark bool) palette {
	var cfg herdrConfig
	if data, err := os.ReadFile(herdrConfigPath()); err == nil {
		if _, err := toml.Decode(string(data), &cfg); err != nil {
			cfg = herdrConfig{}
		}
	}
	t := cfg.Theme
	manual := "catppuccin"
	if t.Name != nil {
		manual = *t.Name
	}
	name, fallback := manual, "catppuccin"
	var mode any
	if t.AutoSwitch {
		siblingDark, siblingLight := siblingThemeNames(manual)
		if dark {
			name, mode = orDefault(t.DarkName, siblingDark), t.Custom["dark"]
		} else {
			name, fallback, mode = orDefault(t.LightName, siblingLight), "catppuccin-latte", t.Custom["light"]
		}
	}
	p, ok := herdrPalettes[canonicalThemeName(name)]
	if !ok {
		p = herdrPalettes[fallback]
	}
	p.override(t.Custom)
	if a := cfg.UI.Accent; a != nil && *a != "cyan" {
		if _, set := t.Custom["accent"]; !set {
			p.Accent = parseColor(*a)
		}
	}
	if m, ok := mode.(map[string]any); ok {
		p.override(m)
	}
	return p
}

// override applies [theme.custom]-style tokens. Anything that isn't a
// token with a string value (the nested light/dark tables) is skipped.
func (p *palette) override(custom map[string]any) {
	fields := p.tokens()
	for key, value := range custom {
		if s, ok := value.(string); ok && fields[key] != nil {
			*fields[key] = parseColor(s)
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
			if _, err := strconv.ParseUint(hex, 16, 32); err == nil {
				return "#" + hex
			}
		case 3:
			if _, err := strconv.ParseUint(hex, 16, 16); err == nil {
				return "#" + string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
			}
		}
	}
	if inner, ok := strings.CutPrefix(s, "rgb("); ok {
		if inner, ok := strings.CutSuffix(inner, ")"); ok {
			if parts := strings.Split(inner, ","); len(parts) == 3 {
				var rgb [3]uint64
				valid := true
				for i, part := range parts {
					n, err := strconv.ParseUint(strings.TrimSpace(part), 10, 8)
					rgb[i], valid = n, valid && err == nil
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

// theme is the palette in use; newMarkdown reads it for the Markdown colours.
var theme palette

// useTheme recolours the picker with p. Linear's own colours (workflow
// states, project status) stay Linear's.
func useTheme(p palette) {
	theme = p
	fg := func(s lipgloss.Style, c string) lipgloss.Style {
		if c == "" {
			return s
		}
		return s.Foreground(lipgloss.Color(c))
	}
	styleDim = fg(defaultStyleDim, p.Overlay0)
	styleHeader = fg(defaultStyleHeader, p.Text)
	styleTabOn = fg(defaultStyleTabOn, p.Accent)
	styleHintHot = fg(defaultStyleHintHot, p.Accent)
	styleTree = fg(defaultStyleTree, p.Accent)
	styleErr = fg(defaultStyleErr, p.Red)
	styleUrgent = fg(defaultStyleUrgent, p.Peach)
	styleOK = fg(defaultStyleOK, p.Green)
	styleSelected = defaultStyleSelected
	if p.SelectionBG != "" {
		styleSelected = styleSelected.Background(lipgloss.Color(p.SelectionBG))
	}
}
