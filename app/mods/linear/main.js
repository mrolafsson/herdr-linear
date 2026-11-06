// Your Linear issues and projects in a popup: yours grouped by status, the
// workspace's projects, and what you filed lately. Open one for its status,
// priority, labels and description; change its status, and one key to a
// worktree for it in a space of its own, or start it: In Progress, yours,
// and an agent in the worktree told to work on it. File a new issue without
// leaving the window.
//
// The work is herdr-linear's (`app`), the TUI plugin's own code: Linear's
// side on the machine you're signed in on, a worktree's on the machine of
// the space you're in. The mod fetches the herdr-linear release it needs onto
// each machine it asks, the first time.

import { View, div } from "gpui-kit";
import * as fs from "fs/promises";
import { v_flex, h_flex, InputState, Input, TextView, Scrollbar, v_virtual_list, VirtualListScrollHandle, Popup } from "gpui-base";
import { agents, panes, machines, spaces, run, exec, theme, appHome, props, closeOverlay, openOverlay, nextCommand, knownAgents } from "herdr";
import { tones, tint, countPill, stateIcon, projectIcon, PRIORITIES, priorityIcon, hue, icon, chip, words, key, MONO } from "./look.js";

// The herdr-linear this mod is part of (its release; the repo's tests hold it
// to herdr-plugin.toml's version).
export const LINEAR_VERSION = "0.14.4";

// Puts that release on a machine, once: into a folder of its own version, so
// it never touches a herdr-linear installed for the TUI (it shares its
// sign-in). The archive is checked against the release's checksums before
// it's used. Prints the binary's path.
const PROVISION = String.raw`
v=$1
dest="$HOME/.local/share/herdr-linear/app/$v"
bin="$dest/herdr-linear"
[ -x "$bin" ] && { printf '%s' "$bin"; exit 0; }
case "$(uname -s)" in Darwin) os=darwin ;; Linux) os=linux ;; *) echo "no herdr-linear for $(uname -s)" >&2; exit 1 ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) echo "no herdr-linear for $(uname -m)" >&2; exit 1 ;; esac
name="herdr-linear_$v""_$os""_$arch.tar.gz"
url="https://github.com/mrolafsson/herdr-linear/releases/download/v$v"
tmp=$(mktemp -d) || exit 1
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$url/$name" -o "$tmp/$name" && curl -fsSL "$url/checksums.txt" -o "$tmp/sums" || { echo "couldn't download herdr-linear $v" >&2; exit 1; }
want=$(grep " $name\$" "$tmp/sums" | cut -d' ' -f1)
if command -v sha256sum >/dev/null 2>&1; then have=$(sha256sum "$tmp/$name" | cut -d' ' -f1); else have=$(shasum -a 256 "$tmp/$name" | cut -d' ' -f1); fi
[ -n "$want" ] && [ "$want" = "$have" ] || { echo "herdr-linear $v didn't match its checksum" >&2; exit 1; }
tar -xzf "$tmp/$name" -C "$tmp" herdr-linear || exit 1
mkdir -p "$dest" && mv "$tmp/herdr-linear" "$bin.new" && chmod 755 "$bin.new" && mv "$bin.new" "$bin" || exit 1
printf '%s' "$bin"
`;

const binaries = new Map();
function binary(machine) {
  const k = machine ?? "";
  if (!binaries.has(k)) {
    const p = exec("sh", ["-c", PROVISION, "sh", LINEAR_VERSION], { on: machine }).then((r) => {
      if (r.code !== 0 || !r.stdout.trim()) throw new Error(r.stderr.trim().split("\n").pop() || `herdr-linear: exit ${r.code}`);
      return r.stdout.trim();
    });
    p.catch(() => binaries.delete(k));
    binaries.set(k, p);
  }
  return binaries.get(k);
}

class AppError extends Error {
  constructor(res) {
    super(res.error);
    this.signedOut = Boolean(res.signedOut);
    this.noRepo = Boolean(res.noRepo);
  }
}

// One request to herdr-linear on a machine (null: this one).
async function ask(req, machine) {
  const bin = await binary(machine);
  const r = await exec("sh", ["-c", 'exec "$0" app "$1"', bin, JSON.stringify(req)], { on: machine });
  let res;
  try {
    res = JSON.parse(r.stdout);
  } catch {
    throw new Error(r.stderr.trim().split("\n").pop() || `herdr-linear: exit ${r.code}`);
  }
  if (res?.error) throw new AppError(res);
  return res;
}

// A request that can outlast a command (a worktree's fetch): run as a job
// there, and asked after until it's done.
async function job(req, machine, limitMs = 5 * 60000) {
  const { job: id } = await ask({ ...req, bg: true }, machine);
  const until = Date.now() + limitMs;
  while (Date.now() < until) {
    await exec("sleep", ["1"]).catch(() => {});
    const res = await ask({ op: "job", job: id }, machine);
    if (res.done) {
      if (res.result?.error) throw new AppError(res.result);
      return res.result;
    }
  }
  throw new Error("it's taking too long; it carries on in the background");
}

const TABS = [
  { id: "mine", label: "My issues", short: "Mine", icon: "person" },
  { id: "projects", label: "Projects", icon: "project" },
  { id: "recent", label: "Recent", icon: "calendar" },
];

// An issue's key typed in the filter ("HAL-212", or just 212): looked up in
// the whole workspace, not only your issues.
const ISSUE_KEY = /^(?:[A-Za-z][A-Za-z0-9_]*-)?\d{1,9}$/;
const MIN_SEARCH = 3; // minSearchLen in linear.go
// Where the lists are kept between openings.
const CACHE = `${appHome()}/linear-cache.json`;

// The list's heights: a heading's, a row's (34 and a gap), and the most it
// takes before it scrolls.
const HEADING_H = 30;
const ROW_H = 36;
const LIST_H = 560;

const machineOf = (address) => (address?.includes("/") ? address.split("/")[0] : null);

function place(s) {
  const tabs = new Set((s.tabs ?? []).map((t) => t.id));
  return panes().find((p) => tabs.has(p.tab) && p.cwd)?.cwd || s.folder || null;
}

export default class Linear extends View {
  init(_props, cx) {
    const p = props() ?? {};
    this.demo = Boolean(p.demo);
    this.from = p.back ?? null;
    // Where esc goes back to, when another mod opened it on an issue: that
    // mod, on what it showed (GitHub, on the pull request).
    this.backTo = p.backTo ?? null;
    // With herdr-github's mod installed, an issue's pull requests open there.
    this.github = false;
    cx.spawn(async (cx) => {
      this.github = (await fs.readFile(`${appHome()}/mods/github/main.js`, "utf8").catch(() => null)) !== null;
      cx.notify();
    });
    this.screen = "list";
    this.tab = localStorage.getItem("tab") ?? "mine";
    this.lists = {};
    this.drilled = null; // a project whose issues are listed
    this.found = null; // { query, issues }: what Linear has for the filter
    this.finding = null; // the filter it's being asked about
    this.filter = "";
    this.at = 0;
    this.cur = null;
    this.detail = null;
    this.menu = null;
    this.flash = this.err = this.busy = "";
    this.workspaces = [];
    this.ws = localStorage.getItem("workspace") ?? "";
    this.on = localStorage.getItem("on");
    this.trees = JSON.parse(localStorage.getItem("trees") ?? "{}");
    this.width = 900;

    this.keys = cx.focus_handle();
    // The list's scroll position, so the chosen row is kept in sight.
    this.scroll = VirtualListScrollHandle.new();
    this.input = InputState.new({ placeholder: "Type to filter, or an issue's key…  ← → to change tabs" });
    this.input.on("change", (_e, cx) => {
      this.filter = this.input.value();
      this.at = 0;
      this.lookUp(cx);
      cx.notify();
    });
    this.input.on("submit", (_e, cx) => this.enter(cx));
    this.input.on("blur", (_e, cx) => {
      cx.timer.after(40, () => {
        if (this.screen === "list" && !this.signedOut && !this.closing) this.input.focus();
      });
    });
    this.title = InputState.new({ placeholder: "Title" });
    this.title.on("focus", () => (this.formField = "title"));
    this.title.on("submit", (_e, cx) => this.submit(cx));
    this.desc = InputState.new({ placeholder: "Description (Markdown), optional" });
    this.desc.on("focus", () => (this.formField = "desc"));
    // The new issue's project: a dropdown with a search field.
    this.projSearch = InputState.new({ placeholder: "Search projects…" });
    this.projSearch.on("change", (_e, cx) => cx.notify());
    this.projSearch.on("submit", (_e, cx) => {
      const first = this.projectMatches()[0];
      if (first) this.pickProject(first.id, cx);
    });
    this.desc.on("submit", (_e, cx) => this.submit(cx));
    this.input.focus();
    cx.timer.after(400, () => this.screen === "list" && this.input.focus());

    cx.spawn(async (cx) => {
      await this.readCache(cx);
      await this.start(cx);
      if (p.issue) await this.openByKey(p.issue, cx);
      cx.notify();
    });
    cx.timer.every(60000, (cx) => this.screen === "list" && !this.busy && this.loadAll(cx));

    // Key bindings and `herdr-app do`: linear.escape, linear.demo,
    // linear.next, linear.prev, linear.reload.
    cx.spawn(async (cx) => {
      for (;;) {
        const c = await nextCommand();
        if (c === "escape") this.escape(cx);
        // Tab (keys.json binds it here, over the app's own): on the new
        // issue's form, from the title to the description and back.
        else if ((c === "tab" || c === "backtab") && this.screen === "create" && !this.projOpen) {
          const next = this.formField === "title" ? this.desc : this.title;
          cx.timer.after(0, () => next.focus());
        }
        else if (c === "demo") {
          this.demo = !this.demo;
          this.lists = {};
          this.drilled = this.found = null;
          this.screen = "list";
          this.trees = this.demo ? {} : JSON.parse(localStorage.getItem("trees") ?? "{}");
          await this.start(cx);
        } else if (c === "next" || c === "prev") this.step(c === "next" ? 1 : -1, cx);
        else if (c === "reload") this.reload(cx);
        cx.notify();
      }
    });
  }

  // ── where things run ───────────────────────────────────────────────────

  // The machine you're signed in to Linear on: the one that answered last,
  // else this one, else each connected one in turn.
  async findLinear() {
    const order = [this.on, "", ...machines().filter((m) => m.name && m.layout !== "unavailable").map((m) => m.name)];
    const tried = new Set();
    let last;
    for (const m of order) {
      if (m == null || tried.has(m)) continue;
      tried.add(m);
      try {
        const s = await ask({ op: "status" }, m || null);
        if (!s.workspaces.length) continue;
        this.on = m;
        this.workspaces = s.workspaces;
        if (!this.workspaces.some((w) => w.id === this.ws)) this.ws = this.workspaces[0].id;
        localStorage.setItem("on", m);
        return;
      } catch (e) {
        last = e;
      }
    }
    this.on = null;
    if (last) throw last;
    const e = new Error("Not signed in to Linear on any machine.");
    e.signedOut = true;
    throw e;
  }

  async linear(req) {
    if (this.demo) return ask({ ...req, demo: true }, null);
    if (this.on == null) await this.findLinear();
    try {
      return await ask({ ...req, workspace: this.ws }, this.on || null);
    } catch (e) {
      if (e instanceof AppError) throw e;
      await this.findLinear();
      return ask({ ...req, workspace: this.ws }, this.on || null);
    }
  }

  workspace() {
    return this.workspaces.find((w) => w.id === this.ws) ?? this.workspaces[0] ?? null;
  }

  async start(cx) {
    this.err = "";
    this.signedOut = false;
    try {
      if (this.demo) this.workspaces = [{ id: "w-halcyon", name: "Halcyon", urlKey: "halcyon" }];
      else await this.findLinear();
    } catch (e) {
      this.signedOut = Boolean(e?.signedOut);
      this.err = this.signedOut ? "" : String(e?.message ?? e);
      if (this.signedOut) this.keys.focus();
      return cx.notify();
    }
    this.loadAll(cx);
  }

  // Signs in in a terminal of its own, on this computer, where the browser
  // is: herdr-linear's own sign-in, which keeps the tokens where the TUI's
  // are.
  signIn(cx) {
    cx.spawn(async (cx) => {
      try {
        const bin = await binary(null);
        this.dismiss();
        run("space.new", { name: "Linear sign-in", argv: [bin, "login"] });
      } catch (e) {
        this.say(String(e?.message ?? e), cx, true);
      }
    });
  }

  switchWorkspace(cx) {
    if (this.demo || this.workspaces.length < 2) return;
    const i = this.workspaces.findIndex((w) => w.id === this.ws);
    this.ws = this.workspaces[(i + 1) % this.workspaces.length].id;
    localStorage.setItem("workspace", this.ws);
    this.lists = {};
    this.drilled = this.found = null;
    this.at = 0;
    this.seedAll();
    this.loadAll(cx);
    cx.notify();
  }

  // ── lists ──────────────────────────────────────────────────────────────

  loadAll(cx) {
    for (const t of TABS) this.load(t.id, cx);
    if (this.drilled) this.loadProject(this.drilled, cx);
  }

  // The lists as they were last time, for the workspace shown, at once on
  // opening while fresh ones are asked for: a file in the app's folder.
  // Never the demo's.
  async readCache(cx) {
    if (this.demo) return;
    // Only a convenience: one that can't be read must not stop the popup.
    try {
      this.cache = JSON.parse((await fs.readFile(CACHE, "utf8").catch(() => null)) ?? "{}");
    } catch {
      this.cache = {};
    }
    if (!this.workspaces.length && this.cache.workspaces) this.workspaces = this.cache.workspaces;
    this.seedAll();
    cx.notify();
  }

  seedAll() {
    const kept = this.cache?.lists?.[this.ws];
    if (!kept || this.demo) return;
    for (const t of TABS) {
      const l = this.lists[t.id];
      if (kept[t.id] && !(l?.loaded && !l.cached)) this.lists[t.id] = { ...l, items: kept[t.id], loaded: true, cached: true, loading: l?.loading };
    }
  }

  keep(tab, items) {
    if (this.demo || tab === "project") return;
    const c = (this.cache ??= {});
    c.workspaces = this.workspaces;
    ((c.lists ??= {})[this.ws] ??= {})[tab] = items;
    this.saveCache();
  }

  // One write at a time: each tab's list arrives separately, and writes that
  // overlap can leave the file with the tail of a longer one after its end.
  async saveCache() {
    this.saveAgain = this.saving;
    if (this.saving) return;
    this.saving = true;
    do {
      this.saveAgain = false;
      await fs.writeFile(CACHE, JSON.stringify(this.cache)).catch(() => {});
    } while (this.saveAgain);
    this.saving = false;
  }

  load(tab, cx, req = { op: "list", tab }) {
    const l = (this.lists[tab] ??= { items: [], loaded: false });
    if (l.loading) return;
    l.loading = true;
    cx.notify();
    const ws = this.ws;
    cx.spawn(async (cx) => {
      try {
        const res = await this.linear(req);
        if (ws !== this.ws) {
          // The workspace changed under it: this answer is the wrong one's.
          l.loading = false;
          if (this.lists[tab] === l) this.load(tab, cx, req);
          return;
        }
        l.items = res.issues ?? res.projects ?? [];
        l.warning = res.warning ?? "";
        l.error = "";
        l.cached = false;
        this.keep(tab, l.items);
      } catch (e) {
        l.error = e?.signedOut ? "Signed out of this workspace: sign in again." : String(e?.message ?? e);
      }
      l.loading = false;
      l.loaded = true;
      cx.notify();
    });
  }

  loadProject(p, cx) {
    this.lists.project = null;
    this.load("project", cx, { op: "list", tab: "project", project: p.id });
  }

  reload(cx) {
    if (this.screen === "issue") return this.loadDetail(cx);
    this.loadAll(cx);
  }

  // What your own list has nothing for, shortly after you stop typing: an
  // issue's key is looked up in the whole workspace; if it isn't one, or
  // that finds nothing, Linear is searched for it.
  lookUp(cx) {
    const q = this.filter.trim();
    this.found = this.finding = null;
    if (this.tab !== "mine" || this.drilled) return;
    const key = ISSUE_KEY.test(q);
    const listed = () => (this.lists.mine?.items ?? []).some((x) => this.matches(x));
    if ((!key && q.length < MIN_SEARCH) || listed()) return;
    this.finding = q;
    const ws = this.ws;
    const current = () => this.finding === q && this.filter.trim() === q && this.ws === ws;
    cx.timer.after(key ? 300 : 500, (cx) => {
      if (!current()) return;
      if (listed()) { // your list came in meanwhile, and has it
        this.finding = null;
        return cx.notify();
      }
      cx.spawn(async (cx) => {
        try {
          let issues = key ? (await this.linear({ op: "find", key: q })).issues : [];
          if (!issues.length && q.length >= MIN_SEARCH && current()) issues = (await this.linear({ op: "search", query: q })).issues;
          if (current()) this.found = { query: q, issues };
        } catch (e) {
          if (current()) this.say(String(e?.message ?? e), cx, true);
        }
        if (this.finding === q) this.finding = null;
        cx.notify();
      });
    });
  }

  step(d, cx) {
    if (this.drilled) return;
    const i = TABS.findIndex((t) => t.id === this.tab);
    this.tab = TABS[(i + d + TABS.length) % TABS.length].id;
    localStorage.setItem("tab", this.tab);
    this.at = 0;
    this.found = this.finding = null;
    cx.notify();
  }

  matches(x) {
    const hay = [x.identifier, x.title, x.name, x.state?.name, x.status?.name, x.project?.name, x.assignee, x.lead, ...(x.teams ?? [])]
      .filter(Boolean).join(" ").toLowerCase();
    return this.filter.toLowerCase().split(/\s+/).filter(Boolean).every((w) => hay.includes(w));
  }

  // The rows shown: issues or projects, each with the heading it goes under.
  shownRows() {
    const key = this.drilled ? "project" : this.tab;
    const l = this.lists[key];
    const rows = [];
    const found = this.found?.query === this.filter.trim() ? this.found : null;
    for (const x of (l?.items ?? []).filter((x) => this.matches(x))) {
      if (key === "projects") rows.push({ project: x, group: x.status.name, color: x.status.color });
      else rows.push({ issue: x, group: x.group, color: x.state.color });
    }
    if (found) {
      const listed = new Set(rows.map((r) => r.issue?.id));
      for (const is of found.issues) if (!listed.has(is.id)) rows.push({ issue: is, group: "Not in your issues", color: null });
    }
    return rows;
  }

  enter(cx) {
    const r = this.shown?.[this.at];
    if (r?.issue) this.openIssue(r.issue, cx);
    else if (r?.project) this.openProject(r.project, cx);
  }

  // ── an issue ───────────────────────────────────────────────────────────

  focusKeys(cx) {
    cx.timer.after(30, () => this.keys.focus());
  }

  openIssue(is, cx, direct = false) {
    this.screen = "issue";
    this.cur = is;
    this.detail = null;
    this.menu = null;
    this.err = this.flash = "";
    this.direct = direct;
    this.loadDetail(cx);
    this.focusKeys(cx);
    cx.notify();
  }

  // From another mod: an issue by its key, in the workspace its link names.
  async openByKey({ key, workspaceKey }, cx) {
    if (!this.demo && workspaceKey) {
      const w = this.workspaces.find((w) => w.urlKey === workspaceKey);
      if (w) this.ws = w.id;
    }
    try {
      const res = await this.linear({ op: "find", key });
      const is = res.issues.find((i) => i.identifier === key.toUpperCase()) ?? res.issues[0];
      if (is) return this.openIssue(is, cx, true);
      this.say(`${key} isn't in ${this.workspace()?.name ?? "this workspace"}.`, cx, true);
    } catch (e) {
      this.say(String(e?.message ?? e), cx, true);
    }
  }

  loadDetail(cx) {
    const is = this.cur;
    cx.spawn(async (cx) => {
      try {
        const d = await this.linear({ op: "detail", issue: is.id });
        if (this.cur?.id !== is.id) return;
        this.detail = d;
        this.cur = d.issue;
        this.replaceRow(d.issue);
      } catch (e) {
        if (this.cur?.id === is.id) this.err = String(e?.message ?? e);
      }
      cx.notify();
    });
  }

  replaceRow(is) {
    for (const l of Object.values(this.lists)) {
      const i = l?.items.findIndex((x) => x.id === is.id) ?? -1;
      if (i >= 0) l.items[i] = { ...is, group: l.items[i].group === "" ? "" : is.group };
    }
  }

  // ── a project ──────────────────────────────────────────────────────────

  openProject(p, cx) {
    this.screen = "project";
    this.curProject = p;
    this.projDetail = null;
    this.menu = null;
    this.err = this.flash = "";
    cx.spawn(async (cx) => {
      try {
        this.projDetail = await this.linear({ op: "project", project: p.id });
      } catch (e) {
        this.err = String(e?.message ?? e);
      }
      cx.notify();
    });
    this.focusKeys(cx);
    cx.notify();
  }

  // A project's issues, in the list, with ← or esc back to the projects.
  drill(p, cx) {
    // From the project's screen, esc goes back there; from the list, to it.
    this.drillFrom = this.screen === "project";
    this.drilled = p;
    this.screen = "list";
    this.at = 0;
    this.input.set_value("");
    this.filter = "";
    this.loadProject(p, cx);
    cx.timer.after(30, () => this.input.focus());
    cx.notify();
  }

  // ── leaving ────────────────────────────────────────────────────────────

  back(cx) {
    if (this.direct && this.from) {
      this.dismiss();
      if (this.backTo) return openOverlay({ mod: this.backTo.mod, kind: "dialog", width: 0.8, height: 0.8, props: this.backTo.props ?? {} });
      if (this.demo) return openOverlay({ mod: this.from, kind: "dialog", width: 0.8, height: 0.8, props: { demo: true } });
      return run("mod.open", { name: this.from });
    }
    this.direct = false;
    const id = this.screen === "project" ? this.curProject?.id : this.cur?.id;
    this.screen = "list";
    this.menu = null;
    this.err = this.flash = "";
    const i = this.shownRows().findIndex((r) => (r.issue ?? r.project).id === id);
    if (i >= 0) this.at = i;
    cx.timer.after(30, () => this.input.focus());
    cx.notify();
  }

  escape(cx) {
    if (this.projOpen) {
      this.projOpen = false;
      return cx.notify();
    }
    if (this.menu) {
      this.menu = null;
      return cx.notify();
    }
    if (this.screen !== "list") return this.back(cx);
    if (this.drilled) return this.undrill(cx);
    this.dismiss();
  }

  // Out of a project's issues: back to its screen when that's where they
  // were opened from, else to the projects, on it.
  undrill(cx) {
    const p = this.drilled;
    this.drilled = null;
    this.input.set_value("");
    this.filter = "";
    if (this.drillFrom) {
      this.drillFrom = false;
      return this.openProject(p, cx);
    }
    this.at = Math.max(0, this.shownRows().findIndex((r) => r.project?.id === p.id));
    cx.notify();
  }

  dismiss() {
    this.closing = true;
    closeOverlay(props().overlay);
  }

  open(url, cx) {
    if (!url) return;
    if (this.demo) return this.say(`Demo: would open ${url}`, cx);
    cx.open_url(url);
  }

  copy(text, cx) {
    if (!text) return;
    cx.write_to_clipboard(text);
    this.say(`Copied ${text}`, cx);
  }

  say(text, cx, isErr = false) {
    this.flash = isErr ? "" : text;
    this.err = isErr ? text : "";
    cx.notify();
  }

  // ── changing it ────────────────────────────────────────────────────────

  // The status menu: the team's statuses in Linear's order, the current one
  // chosen.
  statusMenu(cx) {
    const d = this.detail;
    if (!d) return this.say("Still loading…", cx);
    const items = d.states.map((s) => ({
      label: s.name,
      icon: stateIcon(s),
      color: s.color,
      run: (cx) => {
        if (s.id === d.issue.state.id) return;
        this.busy = `Moving ${d.issue.identifier} to ${s.name}…`;
        cx.notify();
        cx.spawn(async (cx) => {
          try {
            const res = await this.linear({ op: "set-state", issue: d.issue.id, state: s.id });
            this.say(res.note, cx);
          } catch (e) {
            this.say(String(e?.message ?? e), cx, true);
          }
          this.busy = "";
          this.loadDetail(cx);
          this.loadAll(cx);
        });
      },
    }));
    this.menu = { title: `Move ${d.issue.identifier} to`, items, at: Math.max(0, d.states.findIndex((s) => s.id === d.issue.state.id)) };
    cx.notify();
  }

  // An issue's pull request (Linear's GitHub integration links them): on
  // its screen in the GitHub mod, esc there coming back to this issue; else
  // in the browser. With several, which.
  openPR(pr, cx) {
    if (!pr) {
      const prs = this.detail?.prs ?? [];
      if (!prs.length) return this.say("No pull request is linked to it in Linear.", cx);
      if (prs.length > 1) {
        this.menu = { title: `${this.cur.identifier}'s pull requests`, items: prs.map((p) => ({ label: `#${p.number} ${p.title}`, run: (cx) => this.openPR(p, cx) })), at: 0 };
        return cx.notify();
      }
      pr = prs[0];
    }
    if (!this.github) return this.open(pr.url, cx);
    const is = this.cur;
    this.dismiss();
    openOverlay({ mod: "github", kind: "dialog", width: 0.8, height: 0.8, props: {
      pr: { host: pr.host, repo: pr.repo, number: pr.number }, demo: this.demo || undefined,
      backTo: { mod: "linear", label: is.identifier, props: { issue: { key: is.identifier, workspaceKey: this.workspace()?.urlKey }, demo: this.demo || undefined, backTo: this.backTo ?? undefined, back: this.from ?? undefined } },
    } });
  }

  // ── worktrees ──────────────────────────────────────────────────────────

  spaceAt(path, machine) {
    const inside = (p) => p === path || p?.startsWith(path + "/");
    return spaces().find((s) => (s.machine ?? null) === (machine ?? null) && inside(place(s)));
  }

  treeOf(branch) {
    if (!branch) return null;
    if (this.trees[branch]) return this.trees[branch];
    if (this.demo) return null;
    const s = spaces().find((s) => s.branch === branch);
    return s ? { path: place(s), machine: s.machine ?? null } : null;
  }

  // Opens the worktree for an issue (or a project), made if there's none, in
  // the repo of the space you're in (or the team's, from config.json), on
  // that space's machine. start: an issue is checked first (never a closed
  // one, or a coworker's), the worktree made, then the issue moved to In
  // Progress and made yours, and an agent started in it with /ticket (only
  // in a new worktree: an existing one's agent may be busy).
  worktree(cx, start, what = this.screen === "project" ? { project: this.curProject } : { issue: this.cur }) {
    const is = what.issue;
    const pr = what.project;
    if (!is && !pr) return;
    const name = is ? is.identifier : pr.name;
    this.busy = start ? `Starting ${name}…` : `Opening ${name}'s worktree…`;
    this.menu = null;
    cx.notify();
    cx.spawn(async (cx) => {
      try {
        let fresh = is;
        // Each step says what it's doing, and gives up with why if it stalls.
        const step = (label, p, ms = 45000) => {
          this.busy = label;
          cx.notify();
          return Promise.race([p, new Promise((_, no) => cx.timer.after(ms, () => no(new Error(`${label.replace(/…$/, "")} took too long`))))]);
        };
        if (start && is) fresh = (await step(`Checking ${is.identifier} in Linear…`, this.linear({ op: "check-start", issue: is.id }))).issue;
        const workspaceKey = this.workspace()?.urlKey ?? "";
        const target = is
          ? { branch: fresh.branch, label: `${fresh.identifier} ${fresh.title}`, teamKey: fresh.teamKey, workspaceKey, identifier: fresh.identifier, title: fresh.title, url: fresh.url }
          : { branch: pr.branch, label: pr.name, teamKey: pr.teamKey, workspaceKey, project: pr.name, url: pr.url };
        if (this.demo) {
          const res = await ask({ op: "worktree", demo: true, target }, null);
          let note = res.note;
          if (start && is) {
            const s = await this.linear({ op: "start", issue: is.id, branch: target.branch, teamKey: target.teamKey });
            note += `; ${s.note}, and an agent would be asked: ${res.prompt}`;
            this.loadAll(cx);
            if (this.screen === "issue") this.loadDetail(cx);
          }
          this.trees[target.branch] = { path: res.path, machine: "build-1" };
          this.busy = "";
          return this.say(note, cx);
        }
        const active = spaces().find((s) => s.active);
        const machine = active?.machine ?? null;
        const res = await step(`Making its worktree on ${machine ?? "this computer"}…`,
          job({ op: "worktree", target, dirs: [active && place(active), active?.folder].filter(Boolean) }, machine), 4 * 60000);
        this.trees[target.branch] = { path: res.path, machine };
        localStorage.setItem("trees", JSON.stringify(this.trees));
        let note = "";
        if (start && is) {
          try {
            note = (await step(`Starting ${is.identifier} in Linear…`, this.linear({ op: "start", issue: is.id, branch: target.branch, teamKey: target.teamKey }))).note;
          } catch (e) {
            this.busy = "";
            return this.say(`The worktree is ready, but ${is.identifier} wasn't started: ${e?.message ?? e}`, cx, true);
          }
        }
        this.busy = "";
        const s = this.spaceAt(res.path, machine);
        if (s) {
          run("space.select", { id: s.id });
          if (start) return this.say(`${note ? note + ". " : ""}Its worktree already had a space, so no prompt was sent: its agent may be busy.`, cx);
          return this.dismiss();
        }
        const spec = { folder: res.path };
        if (machine) spec.machine = machine;
        if (start) {
          const known = await step("Finding the agent to start…", knownAgents({ on: machine })).catch(() => []);
          const agent = known.find((a) => a.installed && a.key === "claude") ?? known.find((a) => a.installed);
          if (agent) {
            spec.argv = agent.run;
            if (res.created) spec.task = res.prompt;
          }
        }
        this.busy = "Opening its space…";
        cx.notify();
        run("space.new", spec);
        this.busy = "";
        this.dismiss();
      } catch (e) {
        this.busy = "";
        this.say(String(e?.message ?? e), cx, true);
      }
    });
  }

  // The agents working on an issue: those in a pane on its branch.
  agentsOf(is) {
    if (!is?.branch || this.demo) return [];
    const ps = new Map(panes().map((p) => [p.pane, p]));
    return agents().filter((a) => ps.get(a.pane)?.branch === is.branch)
      .map((a) => ({ name: a.title || a.tab_name, status: a.status, machine: machineOf(a.address), address: a.address }));
  }

  // ── a new issue ────────────────────────────────────────────────────────

  // The form: title, description, team, status, priority, project, and
  // whether it's yours. In a project (listed, or open), the issue goes in it.
  create(cx) {
    const project = this.screen === "project" ? this.curProject : this.drilled;
    this.form = { team: null, state: null, priority: 0, project: project?.id ?? null, assignToMe: true, teams: null, states: {} };
    this.screen = "create";
    this.menu = null;
    this.err = this.flash = "";
    this.title.set_value("");
    this.desc.set_value("");
    cx.spawn(async (cx) => {
      try {
        const res = await this.linear({ op: "teams" });
        this.form.teams = res.teams;
        const key = project?.teams?.[0];
        const t = res.teams.find((t) => t.key === key) ?? res.teams.find((t) => t.key === localStorage.getItem("team")) ?? res.teams[0];
        if (t) this.pickTeam(t, cx);
      } catch (e) {
        this.say(String(e?.message ?? e), cx, true);
      }
      cx.notify();
    });
    cx.timer.after(30, () => this.title.focus());
    cx.notify();
  }

  pickTeam(t, cx) {
    const f = this.form;
    f.team = t;
    f.state = t.defaultIssueState?.id ?? null;
    localStorage.setItem("team", t.key);
    if (f.project && !this.projectsFor(t).some((p) => p.id === f.project)) f.project = null;
    if (!f.states[t.id]) {
      cx.spawn(async (cx) => {
        try {
          f.states[t.id] = (await this.linear({ op: "states", team: t.id })).states;
        } catch {}
        cx.notify();
      });
    }
    cx.notify();
  }

  projectsFor(t) {
    return (this.lists.projects?.items ?? []).filter((p) => !t || p.teams.includes(t.key));
  }

  projectMatches() {
    const q = this.projSearch.value().trim().toLowerCase();
    return this.projectsFor(this.form?.team).filter((p) => !q || p.name.toLowerCase().includes(q));
  }

  pickProject(id, cx) {
    this.form.project = id;
    this.projOpen = false;
    this.projSearch.set_value("");
    cx.timer.after(30, () => this.title.focus());
    cx.notify();
  }

  // The project field: what's chosen, and a click lists them all, with a
  // search field on top.
  projectField(o) {
    const f = this.form;
    const cur = f.project ? this.projectsFor(null).find((p) => p.id === f.project) : null;
    const trigger = h_flex().id("proj-pick").flex_none().h(26).pl(8).pr(6).gap(6).items_center().rounded(6).cursor_pointer()
      .border_1().border_color(this.projOpen ? o.accent : o.line).hover((s) => s.bg(o.raised))
      .on_click((_e, cx) => {
        this.projOpen = !this.projOpen;
        if (this.projOpen) cx.timer.after(30, () => this.projSearch.focus());
        cx.notify();
      })
      .when(Boolean(cur), (el) => el.child(icon(projectIcon(cur.status.type), hue(o, cur.status.color), 12)))
      .child(div().text_size(12).text_color(cur ? o.fg : o.muted).child(cur ? cur.name : "No project"))
      .child(icon("chevron", o.dim, 11));
    const matches = this.projOpen ? this.projectMatches() : [];
    const row = (id, label, lead, on) => h_flex().id(`pj-opt-${id ?? "none"}`).h(28).px(8).gap(8).items_center().rounded(5).cursor_pointer()
      .when(on, (el) => el.bg(tint(o.accent, 0.16)))
      .when(!on, (el) => el.hover((s) => s.bg(o.raised)))
      .on_click((_e, cx) => this.pickProject(id, cx))
      .child(lead ?? div().w(12))
      .child(div().flex_1().min_w(0).truncate().text_size(12.5).text_color(o.fg).child(label));
    const list = v_flex().w(320).p(6).gap(2).rounded(8).border_1().border_color(o.line).bg(o.surface).shadow_md()
      .on_mouse_down_out((_e, cx) => {
        this.projOpen = false;
        cx.notify();
      })
      .child(h_flex().h(30).px(8).gap(6).items_center().rounded(6).bg(o.sunk).border_1().border_color(o.line)
        .child(icon("search", o.dim, 12))
        .child(Input.new(this.projSearch).flex_1().h_full().text_size(12.5).text_color(o.fg)))
      .when(!this.projSearch.value().trim(), (el) => el.child(row(null, "No project", null, !f.project)))
      .children(matches.slice(0, 8).map((p) => row(p.id, p.name, icon(projectIcon(p.status.type), hue(o, p.status.color), 12), p.id === f.project)))
      .when(matches.length > 8, (el) => el.child(div().px(8).py(4).text_size(11.5).text_color(o.dim).child(`${matches.length - 8} more: type to narrow them`)))
      .when(matches.length === 0 && Boolean(this.projSearch.value().trim()), (el) => el.child(div().px(8).py(4).text_size(12).text_color(o.dim).child("No project matches.")));
    return Popup.new("proj-popup", trigger).anchor("top_left").when(Boolean(this.projOpen), (el) => el.content(list));
  }

  submit(cx) {
    const f = this.form;
    if (!f || this.busy) return;
    const title = this.title.value().trim();
    if (!title) return this.say("Give it a title first.", cx, true);
    if (!f.team) return this.say("Still loading your teams; try again in a moment.", cx, true);
    this.busy = `Creating ${title}…`;
    cx.notify();
    cx.spawn(async (cx) => {
      try {
        const res = await this.linear({ op: "create", new: { team: f.team.id, title, description: this.desc.value(), project: f.project ?? "", state: f.state ?? "", priority: f.priority, assignToMe: f.assignToMe } });
        this.busy = "";
        this.lists.recent = null;
        this.loadAll(cx);
        this.openIssue(res.issue, cx);
        this.say(res.note, cx);
      } catch (e) {
        this.busy = "";
        this.say(String(e?.message ?? e), cx, true);
      }
    });
  }

  // ── keys ───────────────────────────────────────────────────────────────

  menuKey(k, cx) {
    const m = this.menu;
    const n = m.items.length;
    if (k === "down") m.at = (m.at + 1) % n;
    else if (k === "up") m.at = (m.at + n - 1) % n;
    else if (k === "enter") this.choose(m.items[m.at], cx);
    else if (/^[1-9]$/.test(k) && Number(k) <= n) this.choose(m.items[Number(k) - 1], cx);
    else return false;
    return true;
  }

  choose(item, cx) {
    this.menu = null;
    item.run(cx);
    cx.notify();
  }

  issueKey(k, cx) {
    if (this.menu) return this.menuKey(k, cx);
    const is = this.cur;
    switch (k) {
      case "left":
      case "backspace":
        this.back(cx);
        break;
      case "w":
        this.worktree(cx, false);
        break;
      case "s":
        this.worktree(cx, true);
        break;
      case "c":
        this.statusMenu(cx);
        break;
      case "n":
        this.create(cx);
        break;
      case "o":
        this.open(is.url, cx);
        break;
      case "y":
        this.copy(is.identifier, cx);
        break;
      case "b":
        this.copy(is.branch, cx);
        break;
      case "g":
        this.openPR(null, cx);
        break;
      case "r":
        this.loadDetail(cx);
        break;
      default:
        return false;
    }
    return true;
  }

  projectKey(k, cx) {
    const p = this.curProject;
    switch (k) {
      case "left":
      case "backspace":
        this.back(cx);
        break;
      case "enter":
      case "i":
        this.drill(p, cx);
        break;
      case "w":
        this.worktree(cx, false, { project: p });
        break;
      case "s":
        this.worktree(cx, true, { project: p });
        break;
      case "n":
        this.create(cx);
        break;
      case "o":
        this.open(p.url, cx);
        break;
      default:
        return false;
    }
    return true;
  }

  listKey(k, cx) {
    if (this.signedOut) return false;
    const n = this.shown?.length ?? 0;
    const r = this.shown?.[this.at];
    if (k === "down" && n) (this.at = Math.min(n - 1, this.at + 1)), this.follow();
    else if (k === "up" && n) (this.at = Math.max(0, this.at - 1)), this.follow();
    else if (k === "pagedown" && n) (this.at = Math.min(n - 1, this.at + 12)), this.follow();
    else if (k === "pageup" && n) (this.at = Math.max(0, this.at - 12)), this.follow();
    else if (k === "right" && !this.filter) this.step(1, cx);
    else if (k === "left" && !this.filter) this.drilled ? this.escape(cx) : this.step(-1, cx);
    else if (k === "ctrl-s" && r) this.worktree(cx, true, r);
    else if (k === "ctrl-o" && r) this.open((r.issue ?? r.project).url, cx);
    else if (k === "ctrl-n") this.create(cx);
    else if (k === "ctrl-t") this.switchWorkspace(cx);
    else return false;
    return true;
  }

  // ── drawing ────────────────────────────────────────────────────────────

  // An issue's row, one line: its status icon in Linear's colour, key and
  // title; then, quietly, its project, who has it (when not you), its
  // worktree and agents, and its priority at the end. The chosen one lifts,
  // with an edge in its status's colour.
  issueCard(o, is, on, i) {
    const color = hue(o, is.state.color);
    const prio = priorityIcon(is.priority);
    const ag = this.agentsOf(is);
    const busy = ag.find((a) => a.status === "working");
    const meta = (text, ic, c) => h_flex().flex_none().gap(4).items_center().text_size(12).text_color(c ?? o.dim)
      .when(Boolean(ic), (el) => el.child(icon(ic, c ?? o.dim, 12))).child(div().child(text));
    return h_flex()
      .id(`is-${is.id}`)
      .flex_none()
      .h(34)
      .pl(10)
      .pr(12)
      .gap(10)
      .items_center()
      .rounded(7)
      .when(on, (el) => el.bg(o.raised).shadow_sm())
      .when(!on, (el) => el.hover((el) => el.bg(o.raised)))
      .cursor_pointer()
      .child(div().flex_none().w(3).h(16).rounded(2).when(on, (el) => el.bg(color)))
      .child(icon(stateIcon(is.state), color, 14))
      .child(div().flex_none().w(64).font_family(MONO).text_size(11.5).text_color(o.muted).truncate().child(is.identifier))
      .child(div().flex_1().min_w(0).truncate().text_size(13.5).when(on, (el) => el.font_medium()).text_color(o.fg).child(is.title))
      .when(ag.length > 0, (el) => el.child(busy
        ? h_flex().flex_none().gap(4).items_center().child(icon("work", o.work, 12).spin(1100)).child(div().text_size(12).text_color(o.work).child(ag.length === 1 ? "agent" : `${ag.length} agents`))
        : meta(ag.length === 1 ? "agent" : `${ag.length} agents`, "person")))
      .when(Boolean(this.treeOf(is.branch)), (el) => el.child(icon("worktree", o.accent, 13)))
      // The project and who has it, in columns of their own so the rows line
      // up: the project not in a project's own list, where it's the same on
      // every row.
      .when(this.width >= 760 && !this.drilled, (el) => el.child(div().flex_none().w(150).truncate().text_size(12).text_color(o.dim).child(is.project?.name ?? "")))
      .child(h_flex().flex_none().w(this.width >= 700 ? 130 : 96).justify_end().overflow_hidden()
        .when(Boolean(is.assignee && !is.mine), (el) => el.child(meta(is.assignee ?? "", "person")))
        .when(!is.assignee, (el) => el.child(meta("unassigned"))))
      .child(div().flex_none().w(14).child(prio ? icon(prio, is.priority === 1 ? o.fix : o.muted, 13) : div()));
  }

  // A project's row, one line, as an issue's: its status icon, name and
  // teams; then a bar of how far along it is, its lead and target date, in
  // columns so the rows line up.
  projectCard(o, p, on, i) {
    const color = hue(o, p.status.color);
    const pct = Math.round((p.progress ?? 0) * 100);
    const wide = this.width >= 760;
    return h_flex()
      .id(`pj-${p.id}`)
      .flex_none()
      .h(34)
      .pl(10)
      .pr(12)
      .gap(10)
      .items_center()
      .rounded(7)
      .when(on, (el) => el.bg(o.raised).shadow_sm())
      .when(!on, (el) => el.hover((el) => el.bg(o.raised)))
      .cursor_pointer()
      .child(div().flex_none().w(3).h(16).rounded(2).when(on, (el) => el.bg(color)))
      .child(icon(projectIcon(p.status.type), color, 14))
      .child(div().flex_1().min_w(0).truncate().text_size(13.5).when(on, (el) => el.font_medium()).text_color(o.fg).child(p.name))
      .when(Boolean(this.treeOf(p.branch)), (el) => el.child(icon("worktree", o.accent, 13)))
      .child(div().flex_none().w(64).truncate().font_family(MONO).text_size(11.5).text_color(o.dim).child(p.teams.join(" ")))
      .child(h_flex().flex_none().w(96).gap(6).items_center()
        .child(div().flex_none().w(52).h(4).rounded(2).bg(o.line).overflow_hidden().child(div().h_full().w(Math.round(0.52 * pct)).bg(color)))
        .child(div().text_size(12).text_color(o.muted).child(`${pct}%`)))
      .when(wide, (el) => el.child(div().flex_none().w(120).truncate().text_size(12).text_color(o.dim).child(p.lead ?? "")))
      .when(wide, (el) => el.child(div().flex_none().w(80).text_size(12).text_color(o.dim).child(p.targetDate ?? "")));
  }

  heading(o, label, color, n) {
    return h_flex().flex_none().h(HEADING_H).px(10).pt(8).gap(8).items_center().text_size(10.5)
      .child(div().font_semibold().text_color(color ?? o.dim).child(label.toUpperCase()))
      .child(div().font_medium().text_color(o.dim).child(String(n)))
      .child(div().flex_1().h(1).bg(o.line));
  }

  tabBar(t, o) {
    const w = this.workspace();
    let bar = h_flex().flex_none().gap(4).items_center();
    if (this.drilled) {
      bar = bar
        .child(h_flex().id("undrill").flex_none().h(28).px(8).gap(6).items_center().rounded(7).cursor_pointer().hover((s) => s.bg(t.hover))
          .on_click((_e, cx) => this.undrill(cx))
          .child(icon("back", o.muted, 13)).child(div().text_size(12.5).text_color(o.muted).child(this.drillFrom ? "Project" : "Projects")))
        .child(h_flex().flex_none().h(28).px(10).gap(7).items_center().rounded(7).bg(o.raised).shadow_sm()
          .child(icon(projectIcon(this.drilled.status.type), hue(o, this.drilled.status.color), 12))
          .child(div().text_size(12.5).font_semibold().text_color(o.fg).child(this.drilled.name))
          .child(countPill(o, this.lists.project?.loaded ? this.lists.project.items.length : null, true)));
    } else {
      bar = bar.children(TABS.map((tb) => {
        const on = tb.id === this.tab;
        const l = this.lists[tb.id];
        return h_flex().id(`tab-${tb.id}`).flex_none().h(28).px(10).gap(7).items_center().rounded(7).cursor_pointer().text_size(12.5)
          .when(on, (el) => el.bg(o.raised).shadow_sm())
          .when(!on, (el) => el.hover((s) => s.bg(t.hover)))
          .on_click((_e, cx) => {
            this.tab = tb.id;
            localStorage.setItem("tab", tb.id);
            this.at = 0;
            cx.notify();
          })
          // Its icon, the spinner in its place while it loads: the same
          // size, so the tabs never move.
          .child(l?.loading ? icon("work", o.accent, 12).spin(1100) : icon(tb.icon, on ? o.fg : o.dim, 12))
          .child(div().when(on, (el) => el.font_semibold()).when(!on, (el) => el.font_medium()).text_color(on ? o.fg : o.muted).child(this.width < 700 ? tb.short ?? tb.label : tb.label))
          .child(countPill(o, l?.loaded ? l.items.length : null, on));
      }));
    }
    bar = bar.child(div().flex_1());
    // The workspace: with several, a click (or ctrl+t) goes to the next.
    if (w) {
      const many = this.workspaces.length > 1 && !this.demo;
      bar = bar.child(h_flex().id("ws").flex_none().h(26).px(8).gap(6).items_center().rounded(6).border_1().border_color(o.line)
        .when(many, (el) => el.cursor_pointer().hover((s) => s.bg(t.hover)).on_click((_e, cx) => this.switchWorkspace(cx)))
        .child(div().text_size(12).text_color(o.muted).child(w.name))
        .when(many, (el) => el.child(div().font_family(MONO).text_size(11).text_color(o.dim).child(`${this.workspaces.findIndex((x) => x.id === w.id) + 1}/${this.workspaces.length}`))));
    }
    return bar;
  }

  note(o, text, color) {
    return div().px(14).py(10).text_size(12.8).text_color(color ?? o.dim).child(text);
  }

  // The list: notes about it (an error, empty, loading), and its entries,
  // headings and rows, each of a fixed height, for the virtual list that
  // keeps the chosen row in sight.
  listBody(o) {
    const notes = [];
    const entries = [];
    this.entryOf = [];
    const l = this.lists[this.drilled ? "project" : this.tab];
    const rows = this.shown;
    if (l?.error) notes.push(this.note(o, l.error, o.fix));
    else if (!rows.length) {
      const empty = this.err ? "" : !l?.loaded || l.loading ? "Loading…"
        : this.finding && this.finding === this.filter.trim() ? `Searching Linear for ${this.finding}…`
        : this.filter ? "Nothing matches."
        : this.tab === "recent" ? "Nothing you filed lately." : this.tab === "projects" && !this.drilled ? "No projects." : "No open issues.";
      if (empty) notes.push(this.note(o, empty));
    }
    let last;
    rows.forEach((r, i) => {
      if (r.group && r.group !== last) {
        entries.push({ key: `h-${r.group}`, h: HEADING_H, el: () => this.heading(o, r.group, r.color ? hue(o, r.color) : null, rows.filter((x) => x.group === r.group).length) });
      }
      last = r.group;
      this.entryOf[i] = entries.length;
      const x = r.issue ?? r.project;
      entries.push({ key: `r-${x.id}`, row: i, h: ROW_H, el: () => div().h(ROW_H).pb(2).child(r.issue ? this.issueCard(o, r.issue, i === this.at, i) : this.projectCard(o, r.project, i === this.at, i)) });
    });
    const after = l?.warning ? div().px(14).py(6).text_size(11.5).text_color(o.dim).child(l.warning) : null;
    return { notes, entries, after };
  }

  listView(o) {
    const { notes, entries, after } = this.listBody(o);
    return v_flex().flex_1().min_h(0).gap(2)
      .children(notes)
      .when(entries.length > 0, (el) => el.child(
        div().relative().h(Math.min(LIST_H, entries.reduce((h, e) => h + e.h, 0))).min_h(0).flex_shrink(1)
          .child(v_virtual_list("ln-rows", entries.length, entries.map((e) => e.h), (i) => entries[i].key,
            (range) => entries.slice(range.start, range.end).map((e) => e.el())).track_scroll(this.scroll).size_full()
            // A click chooses a row; on the chosen one, opens it.
            .on_item_click((key, cx) => {
              const e = entries.find((x) => x.key === key);
              if (e?.row == null) return;
              if (e.row === this.at) return this.enter(cx);
              this.at = e.row;
              cx.notify();
            }))
          .child(Scrollbar.vertical("ln-rows").absolute().inset_0())))
      .when(Boolean(after), (el) => el.child(after));
  }

  // Keeps the chosen row in sight: the first, with its heading.
  follow() {
    const e = this.entryOf?.[this.at];
    if (e == null) return;
    this.scroll.scroll_to_item(this.at === 0 ? 0 : e);
  }

  field(o, label, kids) {
    return h_flex().gap(10).items_center().min_h(24)
      .child(div().flex_none().w(72).text_size(12).text_color(o.dim).child(label))
      .child(h_flex().flex_1().min_w(0).gap(6).flex_wrap().items_center().children([kids].flat().filter(Boolean)));
  }

  backButton(o, label) {
    return h_flex().id("back").flex_none().h(24).px(8).gap(4).items_center().rounded(6).cursor_pointer().hover((s) => s.bg(o.raised))
      .on_click((_e, cx) => this.back(cx))
      .child(icon("back", o.muted, 13))
      .child(div().text_size(12).text_color(o.muted).child(label));
  }

  fromLabel() {
    if (this.direct && this.backTo?.label) return this.backTo.label;
    if (this.direct && this.from) return { recap: "Recap", github: "GitHub" }[this.from] ?? this.from;
    if (this.drilled) return this.drilled.name;
    const tb = TABS.find((x) => x.id === this.tab);
    return tb.short ?? tb.label;
  }

  // An issue's screen.
  issueView(o) {
    const is = this.cur;
    const d = this.detail;
    const color = hue(o, is.state?.color);
    const tree = this.treeOf(is.branch);
    const ag = this.agentsOf(is);
    const prio = priorityIcon(is.priority);
    const canStart = d && !d.startWhy && is.state?.type !== "started";
    return v_flex().flex_1().min_h(0).gap(10)
      .child(
        h_flex().flex_none().gap(10).items_center()
          .child(this.backButton(o, this.fromLabel()))
          .when(Boolean(is.state), (el) => el.child(chip(o, "state", [icon(stateIcon(is.state), color, 12), is.state.name], { color, click: (cx) => this.statusMenu(cx) })))
          .child(div().font_family(MONO).text_size(12.5).text_color(o.muted).child(is.identifier))
          .child(div().text_size(12.5).text_color(o.dim).child(this.workspace()?.name ?? ""))
          .child(div().flex_1()),
      )
      .child(div().flex_none().text_size(17).font_semibold().text_color(o.fg).line_height(1.35).child(is.title ?? ""))
      .when(Boolean(d), (el) => el
        .child(this.field(o, "Assignee", is.assignee ? [icon("person", o.dim, 13), div().text_color(o.fg).child(is.mine ? `${is.assignee} (you)` : is.assignee)] : div().text_color(o.dim).child("unassigned")))
        .child(this.field(o, "Priority", [prio ? icon(prio, is.priority === 1 ? o.fix : o.muted, 13) : null, div().text_color(is.priority ? o.fg : o.dim).child(d.priorityLabel || PRIORITIES[is.priority])]))
        .when(Boolean(is.project), (el) => el.child(this.field(o, "Project", chip(o, "proj", [icon("project", o.dim, 12), is.project?.name ?? ""]))))
        .when(Boolean(d.cycle), (el) => el.child(this.field(o, "Cycle", div().text_color(o.fg).child(d.cycle ?? ""))))
        .when(Boolean(d.dueDate), (el) => el.child(this.field(o, "Due", [icon("calendar", o.dim, 13), div().text_color(o.fg).child(d.dueDate ?? "")])))
        .when(d.labels.length > 0, (el) => el.child(this.field(o, "Labels", d.labels.map((l) => chip(o, `lb-${l.name}`, [div().w(7).h(7).rounded_full().bg(hue(o, l.color)), l.name])))))
        .when((d.prs ?? []).length > 0, (el) => el.child(this.field(o, "Pull requests", (d.prs ?? []).map((p, i) =>
          chip(o, `pr-${i}`, [icon("pr", o.accent, 12), `#${p.number}`, div().text_color(o.muted).child(p.title.length > 48 ? p.title.slice(0, 47) + "…" : p.title)], { color: o.accent, click: (cx) => this.openPR(p, cx) })))))
        .child(this.field(o, "Branch", [chip(o, "branch", [icon("branch", o.dim, 13), is.branch], { mono: true, click: (cx) => this.copy(is.branch, cx) }),
          tree ? chip(o, "tree", [icon("worktree", o.accent, 12), "worktree"], { color: o.accent }) : div().text_size(12).text_color(o.dim).child("no worktree yet")]))
        // Why it can't be started; or what starting it does, and the way to.
        .when(Boolean(d.startWhy), (el) => el.child(h_flex().flex_none().gap(10).items_center().px(12).py(8).rounded(8).bg(tint(o.work, 0.12))
          .child(icon("need", o.work, 15))
          .child(div().flex_1().min_w(0).text_color(o.fg).child((d.startWhy ?? "").replace(/ \(w opens.*\)$/, ": w opens its worktree without starting it")))))
        .when(Boolean(canStart), (el) => el.child(h_flex().flex_none().gap(10).items_center().px(12).py(8).rounded(8).bg(tint(o.accent, 0.1))
          .child(icon("play", o.accent, 15))
          .child(div().flex_1().min_w(0).text_color(o.fg).child(`Start: ${is.assignee ? "" : "yours, "}In Progress, and an agent in its worktree working on it`))
          .child(h_flex().id("start-btn").flex_none().h(26).px(10).items_center().rounded(6).bg(o.accent).cursor_pointer().hover((s) => s.opacity(0.85))
            .on_click((_e, cx) => this.worktree(cx, true))
            .child(div().text_size(12).font_semibold().text_color(o.surface).child("Start"))))))
      .when(ag.length > 0, (el) => el.child(
        v_flex().flex_none().gap(1)
          .child(div().px(2).pb(2).text_size(10.5).font_semibold().text_color(o.dim).child(`AGENTS ${ag.length}`))
          .children(ag.slice(0, 4).map((a, i) => h_flex().id(`ag-${i}`).h(28).px(8).gap(8).items_center().rounded(6).cursor_pointer().hover((s) => s.bg(o.raised))
            .on_click(() => {
              this.dismiss();
              run("agent.focus", { terminal: a.address });
            })
            .child(a.status === "working" ? icon("work", o.work, 14).spin(1100) : a.status === "blocked" ? icon("need", o.need, 14) : icon("idle", o.dim, 14))
            .child(div().min_w(0).truncate().text_size(12.5).font_medium().text_color(o.fg).child(a.name))
            .child(h_flex().flex_none().h(18).px(6).gap(4).items_center().rounded(5).border_1().border_color(o.line).bg(o.sunk).font_family(MONO).text_size(10.5).text_color(o.fg)
              .child(icon(a.machine ? "server" : "laptop", o.muted, 11)).child(div().child(a.machine ?? "this computer")))
            .child(div().flex_1())
            .child(div().flex_none().text_size(11.5).text_color(o.dim).child(a.status ?? ""))))))
      .child(div().flex_none().h(1).bg(o.line))
      .child(v_flex().id("is-body").h(360).min_h(0).flex_shrink(1).overflow_y_scrollbar().pr(6)
        .child(d
          ? TextView.markdown(`md-${is.id}`, d.description || "*No description.*").selectable(true).text_size(13).text_color(o.muted).on_link_click((url, cx) => this.open(url, cx))
          : div().text_size(12.8).text_color(this.err ? o.fix : o.dim).child(this.err || "Loading…")));
  }

  // A project's screen.
  projectView(o) {
    const p = this.curProject;
    const d = this.projDetail;
    const color = hue(o, p.status.color);
    const pct = Math.round((p.progress ?? 0) * 100);
    const tree = this.treeOf(p.branch);
    return v_flex().flex_1().min_h(0).gap(10)
      .child(h_flex().flex_none().gap(10).items_center()
        .child(this.backButton(o, "Projects"))
        .child(chip(o, "pstate", [icon(projectIcon(p.status.type), color, 12), p.status.name], { color }))
        .child(div().font_family(MONO).text_size(12.5).text_color(o.dim).child(p.teams.join(" "))))
      .child(div().flex_none().text_size(17).font_semibold().text_color(o.fg).child(p.name))
      .when(Boolean(d?.description), (el) => el.child(div().text_size(13).text_color(o.muted).child(d?.description ?? "")))
      .child(this.field(o, "Progress", [div().flex_none().w(140).h(6).rounded(3).bg(o.line).overflow_hidden().child(div().h_full().w(Math.round(1.4 * pct)).bg(color)), div().text_color(o.fg).child(`${pct}%`)]))
      .when(Boolean(p.lead), (el) => el.child(this.field(o, "Lead", [icon("person", o.dim, 13), div().text_color(o.fg).child(p.lead ?? "")])))
      .when(Boolean(d?.startDate || p.targetDate), (el) => el.child(this.field(o, "Dates", [icon("calendar", o.dim, 13), div().text_color(o.fg).child(`${d?.startDate || "?"} → ${p.targetDate || "?"}`)])))
      .child(this.field(o, "Branch", [chip(o, "pbranch", [icon("branch", o.dim, 13), p.branch], { mono: true }), tree ? chip(o, "ptree", [icon("worktree", o.accent, 12), "worktree"], { color: o.accent }) : null]))
      .child(h_flex().flex_none().gap(10).items_center().px(12).py(8).rounded(8).bg(tint(o.accent, 0.1))
        .child(icon("project", o.accent, 15)).child(div().flex_1().text_color(o.fg).child("Its open issues, as a list"))
        .child(h_flex().id("drill-btn").flex_none().h(26).px(10).items_center().rounded(6).bg(o.accent).cursor_pointer().hover((s) => s.opacity(0.85))
          .on_click((_e, cx) => this.drill(p, cx))
          .child(div().text_size(12).font_semibold().text_color(o.surface).child("Its issues"))))
      .child(div().flex_none().h(1).bg(o.line))
      .child(v_flex().id("pj-body").h(300).min_h(0).flex_shrink(1).overflow_y_scrollbar().pr(6)
        .child(d ? TextView.markdown(`pmd-${p.id}`, d.content || "*No overview.*").selectable(true).text_size(13).text_color(o.muted).on_link_click((url, cx) => this.open(url, cx))
          : div().text_size(12.8).text_color(this.err ? o.fix : o.dim).child(this.err || "Loading…")));
  }

  // The new issue's form.
  createView(o) {
    const f = this.form;
    const states = f.team ? f.states[f.team.id] ?? [] : [];
    const pick = (id, items, isOn, onPick) => h_flex().gap(6).flex_wrap().items_center().children(items.map((x, i) => {
      const on = isOn(x);
      return h_flex().id(`${id}-${i}`).flex_none().h(24).px(8).gap(5).items_center().rounded(6).cursor_pointer()
        .border_1().border_color(on ? o.accent : o.line).when(on, (el) => el.bg(tint(o.accent, 0.14))).when(!on, (el) => el.hover((s) => s.bg(o.raised)))
        .on_click((_e, cx) => {
          onPick(x, cx);
          cx.notify();
        })
        .children(x.parts.filter(Boolean).map((p) => (typeof p === "string" ? div().text_size(12).text_color(on ? o.fg : o.muted).child(p) : p)));
    }));
    const input = (state) => h_flex().h(36).flex_none().px(12).items_center().rounded(8).border_1().border_color(o.line).bg(o.sunk)
      .child(Input.new(state).flex_1().h_full().text_size(13).text_color(o.fg));
    return v_flex().flex_1().min_h(0).gap(12)
      .child(h_flex().flex_none().gap(10).items_center()
        .child(this.backButton(o, this.fromLabel()))
        .child(div().text_size(14).font_semibold().text_color(o.fg).child(`New issue in ${this.workspace()?.name ?? "Linear"}`)))
      .child(input(this.title))
      .child(input(this.desc))
      .child(this.field(o, "Team", f.teams
        ? pick("tm", f.teams.map((t) => ({ ...t, parts: [div().font_family(MONO).text_size(11.5).text_color(o.muted).child(t.key), t.name] })), (t) => t.id === f.team?.id, (t, cx) => this.pickTeam(t, cx))
        : div().text_color(o.dim).child("Loading…")))
      .child(this.field(o, "Status", states.length
        ? pick("st", states.filter((s) => s.type !== "completed" && s.type !== "canceled").map((s) => ({ ...s, parts: [icon(stateIcon(s), hue(o, s.color), 12), s.name] })), (s) => s.id === f.state, (s) => (f.state = s.id))
        : div().text_color(o.dim).child("…")))
      .child(this.field(o, "Priority", pick("pr", PRIORITIES.map((n, i) => ({ i, parts: [priorityIcon(i) ? icon(priorityIcon(i), i === 1 ? o.fix : o.muted, 12) : null, n] })), (x) => x.i === f.priority, (x) => (f.priority = x.i))))
      .child(this.field(o, "Project", this.projectField(o)))
      .child(this.field(o, "Assignee", pick("me", [{ me: true, parts: [icon("person", o.muted, 12), "Me"] }, { me: false, parts: ["Nobody"] }], (x) => x.me === f.assignToMe, (x) => (f.assignToMe = x.me))))
      .child(h_flex().flex_none().gap(10).items_center()
        .child(div().flex_1())
        .child(h_flex().id("create-btn").flex_none().h(30).px(14).items_center().rounded(7).bg(o.accent).cursor_pointer().hover((s) => s.opacity(0.85))
          .on_click((_e, cx) => this.submit(cx))
          .child(div().text_size(12.5).font_semibold().text_color(o.surface).child("Create issue"))));
  }

  menuView(o) {
    const m = this.menu;
    return v_flex().flex_none().gap(2).p(10).rounded(10).border_1().border_color(o.line).bg(o.raised).shadow_md()
      .child(div().px(6).pb(6).text_size(13).font_semibold().text_color(o.fg).child(m.title))
      .children(m.items.map((it, i) => {
        const on = i === m.at;
        return h_flex().id(`mi-${i}`).h(30).px(8).gap(10).items_center().rounded(6).cursor_pointer()
          .when(on, (el) => el.bg(tint(o.accent, 0.18)))
          .when(!on, (el) => el.hover((s) => s.bg(o.sunk)))
          .on_click((_e, cx) => this.choose(it, cx))
          .child(div().flex_none().w(14).font_family(MONO).text_size(11.5).text_color(o.dim).child(String(i + 1)))
          .when(Boolean(it.icon), (el) => el.child(icon(it.icon, hue(o, it.color), 13)))
          .child(div().text_size(13).when(on, (el) => el.font_medium()).text_color(on ? o.fg : o.muted).child(it.label));
      }));
  }

  footer(o) {
    const k = (keys, label, fn, id) => key(o, keys, label, fn, id);
    if (this.menu) {
      return [k(["↑", "↓"], "choose", () => {}, "f-ch"), k(["enter"], "do it", (cx) => this.choose(this.menu.items[this.menu.at], cx), "f-go"), k(["esc"], "cancel", (cx) => this.escape(cx), "f-esc")];
    }
    if (this.screen === "issue") {
      return [
        k(["w"], "worktree", (cx) => this.worktree(cx, false), "f-w"),
        k(["s"], "start", (cx) => this.worktree(cx, true), "f-s"),
        k(["c"], "status", (cx) => this.statusMenu(cx), "f-c"),
        k(["n"], "new issue", (cx) => this.create(cx), "f-n"),
        k(["o"], "Linear", (cx) => this.open(this.cur.url, cx), "f-o"),
        k(["y"], "copy key", (cx) => this.copy(this.cur.identifier, cx), "f-y"),
        k(["b"], "copy branch", (cx) => this.copy(this.cur.branch, cx), "f-b"),
        ...((this.detail?.prs ?? []).length ? [k(["g"], "pull request", (cx) => this.openPR(null, cx), "f-g")] : []),
        k(["esc"], "back", (cx) => this.escape(cx), "f-esc"),
      ];
    }
    if (this.screen === "project") {
      return [
        k(["enter"], "its issues", (cx) => this.drill(this.curProject, cx), "f-i"),
        k(["w"], "worktree", (cx) => this.worktree(cx, false, { project: this.curProject }), "f-w"),
        k(["s"], "start", (cx) => this.worktree(cx, true, { project: this.curProject }), "f-s"),
        k(["n"], "new issue", (cx) => this.create(cx), "f-n"),
        k(["o"], "Linear", (cx) => this.open(this.curProject.url, cx), "f-o"),
        k(["esc"], "back", (cx) => this.escape(cx), "f-esc"),
      ];
    }
    if (this.screen === "create") {
      return [k(["tab"], "next field", () => {}, "f-tab"), k(["enter"], "create", (cx) => this.submit(cx), "f-go"), k(["esc"], "cancel", (cx) => this.escape(cx), "f-esc")];
    }
    if (this.signedOut) return [k(["esc"], "close", (cx) => this.escape(cx), "f-esc")];
    const sel = this.shown?.[this.at];
    const out = [
      k(["↑", "↓"], "choose", () => {}, "f-ch"),
      k(["enter"], "open", (cx) => this.enter(cx), "f-go"),
      k(["^s"], "start", (cx) => sel && this.worktree(cx, true, sel), "f-s"),
      k(["^n"], "new issue", (cx) => this.create(cx), "f-n"),
      k(["^o"], "Linear", (cx) => sel && this.open((sel.issue ?? sel.project).url, cx), "f-o"),
    ];
    if (this.workspaces.length > 1 && !this.demo) out.push(k(["^t"], "workspace", (cx) => this.switchWorkspace(cx), "f-t"));
    out.push(k(["←", "→"], this.drilled ? "back" : "tabs", (cx) => (this.drilled ? this.escape(cx) : this.step(1, cx)), "f-tab"));
    out.push(k(["esc"], "close", (cx) => this.escape(cx), "f-esc"));
    return out;
  }

  signInView(o) {
    return v_flex().flex_none().gap(12).px(14).py(18).items_start()
      .child(div().text_size(15).font_semibold().text_color(o.fg).child("Sign in to Linear"))
      .child(div().text_size(13).text_color(o.muted).line_height(1.5).child("You're not signed in to Linear on any machine. Signing in opens a terminal here running herdr-linear's sign-in, which opens Linear in your browser; then open this again. The TUI plugin, if you use it, shares the sign-in."))
      .child(h_flex().id("signin-btn").flex_none().h(30).px(14).items_center().rounded(7).bg(o.accent).cursor_pointer().hover((s) => s.opacity(0.85))
        .on_click((_e, cx) => this.signIn(cx))
        .child(div().text_size(12.5).font_semibold().text_color(o.surface).child("Sign in")))
      .child(words(o, [[["or try the demo", o.accent, (cx) => {
        this.demo = true;
        this.signedOut = false;
        this.start(cx);
      }]]], "demo-link"));
  }

  render() {
    const t = theme();
    const o = tones(t);
    this.shown = this.shownRows();
    if (this.screen === "list") this.at = Math.max(0, Math.min(this.at, this.shown.length - 1));
    const note = this.busy || this.err || this.flash;
    const noteColor = this.busy ? o.accent : this.err ? o.fix : o.muted;
    const scr = this.screen;
    return v_flex()
      .id("linear")
      .w_full()
      .min_h(0)
      .p(16)
      .gap(10)
      .bg(t.popover)
      .text_size(13)
      // The keyboard: the filter's, in the list (as Recap's), so ↑ ↓ reach the
      // list through it; on a screen without one, the popup's own.
      .when((scr !== "list" && scr !== "create") || this.signedOut, (el) => el.track_focus(this.keys))
      .on_size_change((size, cx) => {
        if (Math.abs(size.width - this.width) > 20) {
          this.width = size.width;
          cx.notify();
        }
      })
      .on_key_down((e, cx) => {
        const k = e.keystroke;
        if (k === "escape") this.escape(cx);
        else if (scr === "create") return;
        else if (!(scr === "issue" ? this.issueKey(k, cx) : scr === "project" ? this.projectKey(k, cx) : this.menu ? this.menuKey(k, cx) : this.listKey(k, cx))) return;
        cx.stop_propagation();
        cx.notify();
      })
      .when(scr === "list" && !this.signedOut, (el) => el
        .child(h_flex().h(36).flex_none().px(12).gap(10).items_center().rounded(9).border_1().border_color(o.line).bg(o.sunk)
          .child(icon("search", o.dim, 15))
          .child(Input.new(this.input).flex_1().h_full().text_size(13).text_color(o.fg))
          .child(div().flex_none().text_size(12).text_color(o.muted).child(this.demo ? "Demo · made-up workspace" : "")))
        .child(this.tabBar(t, o))
        .child(this.listView(o)))
      .when(scr === "list" && Boolean(this.signedOut), (el) => el.child(this.signInView(o)))
      .when(scr === "issue", (el) => el.child(this.issueView(o)))
      .when(scr === "project", (el) => el.child(this.projectView(o)))
      .when(scr === "create", (el) => el.child(this.createView(o)))
      .when(Boolean(this.menu), (el) => el.child(this.menuView(o)))
      .when(Boolean(note), (el) => el.child(h_flex().flex_none().gap(8).items_center()
        .when(Boolean(this.busy), (el) => el.child(icon("work", o.accent, 12).spin(1100)))
        .child(div().flex_1().min_w(0).truncate().text_size(12).text_color(noteColor).child(note))))
      .child(h_flex().flex_none().gap(14).flex_wrap().items_center().pt(8).border_t_1().border_color(o.line).children(this.footer(o)));
  }
}
