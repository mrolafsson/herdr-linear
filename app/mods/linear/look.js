// How the Linear mod draws an issue or a project: its colours, its icons and
// the pieces of its card, in the app's theme. The same look as Recap's and
// GitHub's: colour is spent only on state, surfaces stay neutral; a status is
// drawn in Linear's own colour for it.

import { div, svg } from "gpui-kit";
import { h_flex } from "gpui-base";

// The mod's own icons, from the app's home folder, tinted with the text colour.
const ICONS = "mods/linear/icons";

// The faces: the app's own for words, mono for what you'd type (branches,
// machines, keys). JetBrains Mono ships with Herdr.
export const MONO = "JetBrains Mono";

// A colour at an alpha: the theme's colours are #rrggbb.
export const tint = (color, alpha) => (/^#[0-9a-f]{6}$/i.test(color ?? "") ? color + Math.round(alpha * 255).toString(16).padStart(2, "0") : color);

// Colour roles, from the theme's own names for them, so every theme reads:
// ready (green), waiting (yellow), a draft, merged, something broken, and the
// accent for links, focus and what's asked of you.
export function tones(t) {
  return {
    review: t.ansi2,
    work: t.working ?? t.ansi3,
    draft: t.ansi8 ?? t.dim,
    merged: t.ansi5,
    fix: t.ansi1,
    need: t.blocked ?? t.ansi1,
    accent: t.accent ?? t.ansi4,
    fg: t.foreground,
    muted: t.muted,
    dim: t.dim,
    surface: t.popover,
    raised: t.raised ?? t.hover,
    sunk: t.input ?? t.background,
    line: t.border,
  };
}

// A status's icon, by its type: a ring that fills as it goes, as Linear's.
export function stateIcon(state) {
  switch (state?.type) {
    case "triage":
      return "lin-triage";
    case "backlog":
      return "lin-backlog";
    case "unstarted":
      return "lin-todo";
    case "started":
      return /review/i.test(state.name ?? "") ? "lin-review" : "lin-prog";
    case "completed":
      return "lin-done";
    case "canceled":
      return "lin-canceled";
  }
  return "lin-todo";
}

// A project's icon, by its status's type.
export function projectIcon(type) {
  return { backlog: "lin-backlog", planned: "lin-todo", started: "lin-prog", paused: "lin-backlog", completed: "lin-done", canceled: "lin-canceled" }[type] ?? "lin-todo";
}

// A priority's icon and name: urgent, then bars for high, medium and low.
export const PRIORITIES = ["No priority", "Urgent", "High", "Medium", "Low"];
export function priorityIcon(p) {
  return { 1: "prio-urgent", 2: "prio-3", 3: "prio-2", 4: "prio-1" }[p] ?? null;
}

// Linear's colour for a status, readable on the theme: its own when it has
// one, else the theme's dim.
export const hue = (o, c) => (/^#[0-9a-f]{6}$/i.test(c ?? "") ? c : o.dim);

// How long ago, in a word or two.
export function ago(ms, now = Date.now()) {
  if (!ms) return "";
  const m = Math.floor((now - ms) / 60000);
  if (m < 1) return "just now";
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return d < 30 ? `${d}d ago` : new Date(ms).toLocaleDateString();
}

// One icon, `size` px square, in `color`.
export function icon(name, color, size = 16) {
  return svg(`${ICONS}/${name}.svg`).flex_none().w(size).h(size).text_color(color);
}

export function duration(ms) {
  const m = Math.floor(ms / 60000);
  if (m < 1) return "<1m";
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  return h < 24 ? `${h}h` : `${Math.floor(h / 24)}d`;
}

// A chip: a small rounded label, neutral unless it carries a state's colour
// (then tinted with it). `parts` are elements or strings; `click` makes it a
// link.
export function chip(o, id, parts, { color = null, mono = false, click = null } = {}) {
  return h_flex()
    .id(id)
    .flex_none()
    .h(22)
    .px(8)
    .gap(5)
    .items_center()
    .rounded(6)
    .bg(color ? tint(color, 0.12) : o.sunk)
    .text_size(mono ? 11 : 11.5)
    .text_color(color ?? o.muted)
    .when(!mono, (el) => el.font_medium())
    .when(mono, (el) => el.font_family(MONO))
    .when(Boolean(click), (el) =>
      el.cursor_pointer()
        .hover((s) => s.bg(color ? tint(color, 0.22) : o.raised))
        .on_click((_e, cx) => {
          cx.stop_propagation();
          click(cx);
        }))
    .children(parts.filter((p) => p !== null && p !== undefined && p !== "").map((p) => (typeof p === "string" ? div().child(p) : p)));
}

// Words in a row, each in its colour: [text, color, onClick?], joined by
// dim dots between groups.
export function words(o, groups, idBase) {
  const out = [];
  groups.filter((g) => g.length).forEach((g, i) => {
    if (i > 0) out.push(div().text_color(o.dim).child("·"));
    g.forEach(([text, color, click, under], j) => {
      const w = div().text_color(color).child(text);
      if (!click) return out.push(w);
      out.push(
        div()
          .id(`${idBase}-${i}-${j}`)
          .text_color(color)
          .font_medium()
          .when(Boolean(under), (el) => el.underline())
          .cursor_pointer()
          .hover((s) => s.underline())
          .on_click((_e, cx) => {
            cx.stop_propagation();
            click(cx);
          })
          .child(text),
      );
    });
  });
  return h_flex().gap(8).items_center().flex_wrap().children(out);
}

// What's drawn on a keycap: the keys with a symbol of their own as icons
// (enter as an arrow down and left, ⌃, ⌘, tab, the arrows), letters
// capitalised as on the key; "^g" or "ctrl-g" is ⌃G, "cmd-g" ⌘G.
const KEY_ICONS = {
  enter: "key-enter", "⏎": "key-enter", "↵": "key-enter",
  tab: "key-tab", "⇥": "key-tab",
  "↑": "key-up", up: "key-up", "↓": "key-down", down: "key-down",
  "←": "key-left", left: "key-left", "→": "key-right", right: "key-right",
  ctrl: "key-ctrl", "^": "key-ctrl", "⌃": "key-ctrl",
  cmd: "key-cmd", "⌘": "key-cmd",
};
function keyParts(k) {
  const m = /^(\^|ctrl[-+]|cmd[-+])(.+)$/i.exec(k);
  if (m) return [m[1][0] === "^" ? "ctrl" : m[1].slice(0, -1).toLowerCase(), ...keyParts(m[2])];
  return [KEY_ICONS[k.toLowerCase()] ? k.toLowerCase() : k.length === 1 ? k.toUpperCase() : k];
}

// One keycap.
export function cap(o, k) {
  return h_flex()
    .flex_none()
    .min_w(20)
    .h(20)
    .px(5)
    .gap(1)
    .items_center()
    .justify_center()
    .rounded(5)
    .border_1()
    .border_b_2()
    .border_color(o.line)
    .bg(o.surface)
    .font_family(MONO)
    .text_size(11)
    .text_color(o.muted)
    .children(keyParts(k).map((p) => (KEY_ICONS[p] ? icon(KEY_ICONS[p], o.muted, 11) : div().child(p))));
}

// A key in the footer: its keycaps and what it does. Clickable.
export function key(o, keys, label, click, id) {
  return h_flex()
    .id(id)
    .flex_none()
    .gap(6)
    .items_center()
    .cursor_pointer()
    .text_size(12)
    .text_color(o.dim)
    .hover((s) => s.text_color(o.fg))
    .on_click((_e, cx) => click(cx))
    .children(keys.map((k) => cap(o, k)))
    .child(div().child(label));
}

// A tab's count, in a pill wide enough for it from the start: "–" until
// it's known, so the tabs don't move when it comes.
export function countPill(o, n, on) {
  return div()
    .flex_none()
    .min_w(24)
    .h(18)
    .px(6)
    .flex()
    .items_center()
    .justify_center()
    .rounded(9)
    .bg(on ? o.sunk : tint(o.line, 0.6))
    .font_family(MONO)
    .text_size(11)
    .font_medium()
    .text_color(on ? o.fg : o.dim)
    .child(n == null || n === "" ? "–" : String(n));
}
