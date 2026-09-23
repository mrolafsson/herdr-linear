package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// defaultClientID is the public OAuth client (PKCE, no secret) registered for
// this plugin. Override it with `client_id` in config.json to use your own app.
const defaultClientID = "446107c5f38f5735e179c8e06af26ca0"

type config struct {
	ClientID string `json:"client_id"`
	// Base is the ref new worktrees branch from. Empty = the remote's default
	// branch (origin/HEAD), falling back to origin/main.
	Base string `json:"base"`
	// StartPrompt is sent to the new worktree's agent by "start". {identifier},
	// {title} and {url} are substituted.
	StartPrompt string `json:"start_prompt"`
	// Repos maps a Linear team key to a checkout, for when the picker is opened
	// from a space that is not inside that team's repo.
	Repos map[string]string `json:"repos"`
	// AgentWaitSeconds bounds how long "start" waits for an agent to come up in
	// the new worktree before giving up on sending the prompt.
	AgentWaitSeconds int `json:"agent_wait_seconds"`
	// Theme for rendered Markdown: "dark", "light", or empty to ask the terminal.
	Theme string `json:"theme"`
}

func pluginID() string {
	if id := os.Getenv("HERDR_PLUGIN_ID"); id != "" {
		return id
	}
	return "herdr-linear"
}

func xdgDir(env, fallback string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback)
}

func configDir() string {
	if d := os.Getenv("HERDR_PLUGIN_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), "herdr", "plugins", "config", pluginID())
}

func stateDir() string {
	if d := os.Getenv("HERDR_PLUGIN_STATE_DIR"); d != "" {
		return d
	}
	return filepath.Join(xdgDir("XDG_STATE_HOME", ".local/state"), "herdr", "plugins", pluginID())
}

// loadConfig reads config.json. On error it still returns the defaults, so
// commands that don't depend on your settings (sign out, status) keep working
// while the file is broken.
func loadConfig() (config, error) {
	cfg, err := readConfig()
	if err != nil {
		cfg = config{}
	}
	return withDefaults(cfg), err
}

func readConfig() (config, error) {
	cfg := config{}
	data, err := os.ReadFile(filepath.Join(configDir(), "config.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return cfg, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return cfg, errors.New("config.json: " + err.Error())
		}
	}
	return cfg, nil
}

func withDefaults(cfg config) config {
	if cfg.ClientID == "" {
		cfg.ClientID = defaultClientID
	}
	if cfg.StartPrompt == "" {
		cfg.StartPrompt = "/ticket {identifier}"
	}
	if cfg.AgentWaitSeconds <= 0 {
		cfg.AgentWaitSeconds = 90
	}
	for k, v := range cfg.Repos {
		cfg.Repos[k] = expandHome(v)
	}
	return cfg
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}
