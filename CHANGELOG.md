# Changelog

## 0.14.3 - 2026-11-04

- **A worktree is made from the checkout you picked**, exactly: one whose
  path ends in a space, or has a newline in it, is no longer mistaken for
  a sibling. Thanks, @EricGrill!

## 0.14.2 - 2026-11-01

- **Start says what it's doing** in the app: checking the issue in Linear,
  making its worktree (and on which machine), starting it, finding the
  agent, opening its space. A step that stalls gives up and says which,
  instead of a spinner that never stops.
- **Background jobs keep a log** and record their process, so one that
  stops without an answer is reported at once, with why.
- **The new issue's form:** Tab and shift+Tab move between the title and
  the description (`keys.json` binds them, as the app keeps Tab for
  itself); the project is a dropdown with a search field, not a row of
  every project.

## 0.14.1 - 2026-11-01

- **An issue's pull requests.** In the app, an issue's screen lists the
  pull requests Linear's GitHub integration linked to it; a click, or `g`,
  opens one in herdr-github's GitHub mod, and `esc` there comes back. The
  demo's HAL-212 has #482.
- **Denser lists in the app:** issues and projects one line each, in
  columns that line up, the chosen one kept in sight as you move; a
  project's issues leave out the project. Lists show at once, from last
  time, while fresh ones load. `esc` steps back through a project: an
  issue, its issues, the project, the projects.
- The README and herdr.dev have the app's screenshots.

## 0.14.0 - 2026-11-01

- **In the Herdr app.** A Linear mod (in `app/`) brings the popup to the
  Herdr app: ⌘L, your issues by status in Linear's colours, the
  workspace's projects and what you filed lately, as cards; an issue's
  screen with its status, priority, labels, cycle and description; change
  its status, `w` for its worktree in a space and `s` to start it (checked
  first, then the worktree, In Progress and yours, and Claude Code asked
  to work on it). File a new issue from a form; type a key to find any
  issue. It finds the machine you're signed in on (sharing the TUI's
  sign-in), offers the sign-in where there's none, makes worktrees on the
  machine of the space you're in, and fetches the herdr-linear it needs
  onto each by itself. Recap's and GitHub's issues open in it.
  `sh install.sh --app` installs it (`--tui` the plugin; no flag, both).

- **`herdr-linear app JSON`**: what the mod runs. One request in (status,
  list, find, detail, project, set-state, check-start, start, teams,
  states, create, worktree, or a background job's answer), one JSON answer
  out, from the plugin's own code: start's checks are the popup's.

## 0.13.0 - 2026-11-01

- **Look up issues for another program: `herdr-linear issues '["HAL-2066"]'`.**
  It prints, as JSON and in the order given, each issue's title, status,
  link and workspace, or `null` for a key none of your workspaces has. It's
  for the Herdr app's recap, which shows agents from all your machines and
  asks the one that's signed in to Linear. Answers are kept for a minute in
  `lookups.json`, so asking every few seconds doesn't keep asking Linear;
  `--force` asks again.

## 0.12.1 - 2026-10-20

- **herdr-vitals joins the plugins that work together.** The README's
  table has it: how your machines are doing, and in Recap's header, the
  machine's CPU and memory, a click from Vitals.

## 0.12.0 — 2026-10-13

- **Attach files to a new issue.** The form has a **Files** field: `ctrl+f`
  (or *+ Add new…*) opens herdr-file-transfer's popup in its place, to pick
  any file on the server or bring one over from your Mac or iPad, and comes
  back with the form as you left it. Files are uploaded when the issue's
  created: images shown in the description, anything else linked.
- herdr-file-transfer joins the plugins that work together, in the README's
  table.

## 0.11.2 — 2026-10-13

- **The README says what herdr-snooze can now wait for**: until an agent's
  done, or until its pull request is merged, which it reads from
  herdr-github's labels.

## 0.11.1 — 2026-10-12

- **herdr-snooze joins the plugins that work together.** The README's table
  has it, with Recap's Snoozed tab and its `ctrl+s` / `ctrl+w`; the shared
  look (`look.go`) is now kept the same in all four.
- **The screenshots script stops, rather than drawing an emoji as a box**,
  when the machine has no `rsvg-convert`, JetBrains Mono or emoji font, and
  says what to install.

## 0.11.0 — 2026-10-12

- "Linear" is no longer written before the tabs: the popup's border says it
  already, and the tabs now start in line with the keys along the bottom, as
  herdr-github's and herdr-recap's do.
- Every tab says how many it lists as soon as the popup opens, not only
  once you've been to it: all three lists load at the start.
- **The popup opens on the lists from last time** rather than on a
  spinner, marked *updating* until Linear's fresh ones replace them.
  `ctrl+r` keeps the list on screen, and usable, the same way. The lists
  are kept per workspace in `lists.json` in the plugin's state directory,
  readable only by you, and a workspace's are deleted when you sign out of
  it; see *Privacy and security* in the README.

## 0.10.0 — 2026-10-12

- **The lists, laid out as herdr-github's and herdr-recap's are**, an issue
  still to a line. The header names the popup and shows the tab as a pill,
  each tab with how many it lists; too wide for the popup, the name goes,
  then *My issues* shortens to *Mine*. The filter lines up with the rows and
  says how to change tabs.
- **Status headings in the status's colour, with counts**, the current one
  kept at the top while scrolling.
- **A row says things in words**: *urgent*, *worktree* and *you lead* in
  place of `!`, `⌥` and `◆`. Identifiers are padded to the longest listed,
  not to nine cells. In a narrow popup the title keeps its room and the edge
  gives way a piece at a time (the project first), where it used to go all
  at once; long project and assignee names are cut short.
- The line above the keys has the selected issue's priority and branch, or
  the selected project's target date and branch.
- **The selection stays on the top row until you pick one**, whatever the
  lists do as they load; after that a refresh keeps it on your pick.
- `esc` says what it will do (*clear*, *back*, *close*); an empty list says
  why it's empty; PgUp/PgDn move by what's on screen.

## 0.9.0 — 2026-10-11

- **Spaces and agents are tagged with their Linear issue.** A branch named
  for an issue (`me/eng-142-fix-login`) puts `$issue`, `$issue_state` and
  `$issue_title` on its space and on the agents working in it, as herdr
  tokens for your sidebar rows; herdr-recap shows them with each agent. Only
  identifiers of teams you have count, so `utf-8` isn't an issue.
  `"labels": false` turns it off. See *Sidebar labels* in the README.
- **The popup opens straight on an issue** when another plugin asks
  (`HERDR_LINEAR_ISSUE`), and `esc` goes back to the popup it came from:
  that's a click on an agent's issue in herdr-recap.

## 0.8.1 — 2026-10-10

- The Created tab is now called **Recent**.

## 0.8.0 — 2026-10-09

- **A Created tab**: the issues you created lately, newest first, in any
  status and anyone's, so what you just filed is easy to find even when it
  isn't assigned to you.
- Fixed: quick add's issues landed in Triage on teams with triage on, while
  the form showed the team's default status (Todo, say). Linear puts an
  issue made through its API in Triage unless it's given a status; the form
  now sends the one it shows.

## 0.7.1 — 2026-10-07

- Fixed: typing a long description in quick add's details scrolled it out
  of view, leaving the box blank while the text was still kept. It now wraps
  at the box's width and stays in sight as you type.

## 0.7.0 — 2026-10-06

- Quick add: ctrl+c in the list (`c` on a project's screen, as in Linear)
  opens a form for a new issue: title, details, team, project, status, priority and assignee.
  Enter on the title creates it with the defaults: yours, the team's default
  status, and the project you're on or have selected. Then view it, start it,
  or add another with the same choices.
- ctrl+c in the list now creates an issue rather than closing the popup; esc
  still closes it, and ctrl+c still closes from every other screen.
- **One look, shared with herdr-github and herdr-recap.** A title, a
  branch, an agent and a key are drawn the same in all three (see *Colours*
  in the README). Here: an issue's identifier wears its state's colour like
  its mark, titles are the brightest text, the project on the right and the
  group headings are dim, branches are mauve, and the issue screen shows its
  state as a pill.
- **Keys are pills.** The keys along the bottom are solid chips, coloured by
  what they do and grouped by colour; the one under the pointer underlines.
  Too many for the popup's width, and labels shorten (`^o open in Linear`
  becomes `^o open`), then the least missed keys are left out.
- **The `terminal` theme keeps its hierarchy.** Dim text no longer shares
  the body's colour, so a title stands out from the project beside it.
- Fixed: a click on `^y copy link` typed the key's name instead of pressing
  it.

## 0.6.2 — 2026-09-26

- Starting an issue no longer assumes a `/ticket` command. By default it
  types `/ticket ENG-123` only where Claude Code has a `ticket` skill or
  command (in the new worktree, its repo, or `~/.claude`), and otherwise
  `Work on the Linear issue ENG-123: <url>`, instead of an unknown command.
  `start_prompt` still overrides it.

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
