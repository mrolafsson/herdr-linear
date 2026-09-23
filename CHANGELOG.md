# Changelog

## 0.1.0 — unreleased

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
