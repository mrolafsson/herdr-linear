# Changelog

## 0.6.1 — 2026-09-26

- Signing out over SSH, where the keyring can't be reached, is refused
  instead of unlisting a workspace whose sign-in may still be in the
  keyring, valid at Linear with nothing left to sign out of it.
- Signed in on the desktop and again over SSH (two grants, one in the
  keyring and one in a file): the file's is no longer deleted unrevoked.
  Signing out revokes both, and `status` names both.
- A keyring that's locked, over SSH where its unlock prompt can't show, is
  caught before the sign-in link opens, with the way out (`token_store`
  `"file"`), not after you've approved.
- `logout --local` removes a token file other users can read, which it
  refused before.
- Pasting the sign-in link itself (what ctrl+y copies) no longer ends the
  sign-in; it's rejected and the popup keeps waiting. After a good paste the
  popup says it's signing in.
- ctrl+y is spelled out, bright, under the link: the popup holds the mouse,
  so selecting the link doesn't work.
- Over SSH, every copy (`y` for a branch too) goes to your terminal's
  clipboard (OSC 52) rather than the remote machine's, and "open in Linear"
  copies the link instead of opening a browser you couldn't see.
- WSL opens the browser again: no display there doesn't mean no browser.

## 0.6.0 — 2026-09-26

- Sign in on a remote machine. Over SSH, or on Linux with no display, no
  browser is opened; the link is shown, to open on any computer (ctrl+y
  copies it). Paste back the address Linear sends that browser to, and the
  sign-in finishes; forwarding port 47821 works too. `herdr-linear login`
  reads the pasted address from stdin.
- Linux without a keyring (over SSH, on a server): the sign-in is kept in a
  file readable only by you, in the state directory, instead of failing.
  A keyring that's there but locked still asks to be unlocked. The new
  `token_store` setting (`auto`, `keyring`, `file`) picks one; `status`
  says where each sign-in is kept.
- The link stays on screen for the whole sign-in. Before, when the browser
  couldn't be opened, the popup replaced it with the next line at once.

## 0.5.1 — 2026-09-25

- Opening the picker while an earlier one is still up (left open on another
  herdr client, where you can't see it) closes that one and opens it where
  you are, instead of failing with *a popup pane is already open*. Another
  plugin's popup is left alone, with a toast saying so.
- The popup's size is in the manifest, so it's the same however it's
  opened.

## 0.5.0 — 2026-09-24

- Filtering by an issue number (`1038`, or `eng-1038`) searches Linear for
  it in every team, so it's found even when it isn't in your issues:
  someone else's, unassigned or closed.

## 0.4.0 — 2026-09-24

- Linux. Tokens are kept in the Secret Service (GNOME Keyring, KWallet…),
  over D-Bus: a locked keyring prompts to unlock, and is never mistaken for
  being signed out. The browser opens with `xdg-open`; copying uses
  `wl-copy`, `xclip` or `xsel`. Releases include Linux binaries, and
  `scripts/build.sh` downloads them on machines without Go.
- Start a project: `s` on a project, or ctrl+s in the Projects list, opens
  its worktree and prompts a new one's agent (`project_start_prompt`).
- Keychain and keyring calls (reaching the session bus included), the
  browser and the clipboard have time limits, so none can hang the picker or
  hold the sign-in lock for long. On Linux no session bus is ever started for
  the plugin: without your desktop's, it says so.
- The sign-in page always reaches the browser before the sign-in's listener
  stops, and says Linear approved access rather than "signed in": herdr
  finishes the sign-in after it.

## 0.3.0 — 2026-09-24

- More than one Linear workspace. Sign in to each (**ctrl+t** → *Sign in to
  another workspace*, or `login` again); the picker shows the one that goes
  with the repo you open it from, asking the first time in each repo and
  remembering. **ctrl+t** switches. `"repos"` takes `"acme/ENG"` for a team in
  one workspace only.
- `logout` signs out of every workspace, or one: `logout acme`. `status`
  lists them.
- Tokens are stored per workspace; a sign-in from 0.2 moves over the first
  time the picker opens.
- Projects are grouped by status, like issues, the ones you lead first in
  each group.

## 0.2.1 — 2026-09-24

- A theme's background counts as light or dark by relative luminance, and a
  named `panel_bg` (like `"white"`) is judged too; before, it always applied,
  even a light background in a dark terminal.
- herdr's `[theme]` and `[ui]` are read separately, as herdr reloads them: a
  mistake in `[ui]` no longer drops the theme, only the legacy `ui.accent`.

## 0.2.0 — 2026-09-23

- The picker takes herdr's theme colours: the theme in herdr's `config.toml`
  (any of its 18 built-ins or their aliases), `auto_switch` with `dark_name`
  and `light_name`, and `[theme.custom]` overrides, resolved as herdr resolves
  them. Accents, the selection, dim text, errors and rendered Markdown follow
  it; Linear's own status and label colours stay Linear's. A theme made for
  the other background (a light one in a dark terminal) isn't applied.

## 0.1.0 — 2026-09-23

First release.

- Picker popup with **My issues** and **Projects** tabs. Issues are grouped by
  status with Linear's status icons and colours, work in flight first.
- Issue screen: status, priority, project, assignee, labels, cycle, due date,
  branch, and the description rendered as Markdown (scrollable).
- Project screen: status, progress, lead, dates and rendered content, one key
  from its open issues.
- Worktrees: open an issue's or project's worktree, creating it from a fresh
  `origin/HEAD` on Linear's own branch name when there isn't one.
- **Start**: move an issue to In Progress, claim it if unassigned, create its
  worktree and send the new worktree's agent `/ticket <ID>` (configurable).
- Change an issue's status from a picker with the team's states.
- Mouse: hover highlights, one click opens, wheel scrolls, footer hints and
  tabs are buttons.
- Sign in with OAuth 2.0 + PKCE; tokens in the macOS keychain, refreshed
  automatically; sign out revokes the grant.
- Demo mode on a fictional workspace, for trying it out and for screenshots.
