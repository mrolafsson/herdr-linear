# Screenshots and the README GIF, from the demo workspace: no account, nothing
# real on screen. Regenerate with `scripts/screenshots.sh` (needs `brew install vhs`).

Output docs/images/demo.gif

Set Shell "bash"
Set FontFamily "JetBrains Mono"
Set FontSize 16
Set Width 1200
Set Height 720
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms

# The demo's Markdown colours: fixed, so every run looks the same.
Env HERDR_PLUGIN_CONFIG_DIR "docs/demo-config"

Hide
Type "clear && bin/herdr-linear picker --demo"
Enter
Sleep 1s
Show

# My issues
Sleep 1.2s
Screenshot docs/images/issues.png
Down@300ms 2
Sleep 600ms

# An issue: rendered description
Up@300ms 1
Enter
Sleep 1.2s
Screenshot docs/images/issue.png
Down@150ms 6
Sleep 800ms

# Change its status
Type "c"
Sleep 800ms
Screenshot docs/images/status.png
Down@300ms 1
Sleep 500ms
Escape
Sleep 400ms
Escape
Sleep 500ms

# Filter
Type "search"
Sleep 900ms
Screenshot docs/images/filter.png
Escape
Sleep 400ms

# Projects, then one project's page
Tab
Sleep 1s
Screenshot docs/images/projects.png
Enter
Sleep 1.2s
Screenshot docs/images/project.png
Type "i"
Sleep 1s
Screenshot docs/images/project-issues.png
Sleep 800ms

Ctrl+C
