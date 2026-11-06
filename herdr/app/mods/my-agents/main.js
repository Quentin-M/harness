// Agents in grouped or priority views; parents take their most urgent sub-agent's place.
// A viewed done agent stays in Done until left.

import { View, div, image, svg } from "gpui-kit";
import { v_flex, h_flex } from "gpui-base";
import { agents, terminals, focused, spaces, run, theme, shared, setShared, setAgentOrder, openOverlay, closeOverlay, props, window, layout } from "herdr";

function find(node, id) {
  if (node?.id === id) return node;
  for (const child of node?.children ?? []) {
    const found = find(child, id);
    if (found) return found;
  }
  return null;
}

// The sidebar's rail goes by its width, not a flag: dragging its edge narrows it too.
const narrow = () => (find(layout(), "sidebar")?.size?.px ?? Infinity) < 160;

// To the rail (on macOS wide enough for the traffic lights), or back to the width it had.
function toggleSidebar() {
  const width = find(layout(), "sidebar")?.size?.px;
  if (!narrow()) setShared("sidebar.width", width);
  // Mods edited before the rail went by width still read the flag.
  setShared("sidebar.collapsed", !narrow());
  run("layout.resize", { id: "sidebar", px: narrow() ? (shared("sidebar.width") ?? 268) : window()?.platform === "macos" ? 76 : 44 });
}

// The title minus the leading spinner/status glyphs agents put in front of it.
const clean = (s) => (s ?? "").replace(/^[^\p{L}\p{N}]+/u, "").trim();

const RANK = { blocked: 4, done: 3, working: 2, idle: 1 };
// A sub-agent without a pane isn't in agents(): its raw word, error counting as
// blocked. It's never done: it reports to its parent, and the parent's done is the news.
const kidStatus = (k, listed) => {
  const s = listed.get(k.address) ?? k.status;
  return s === "error" ? "blocked" : s === "done" ? "idle" : s;
};
const SORTS = [
  { id: "tree", label: "Space › tab › pane", icon: "layers" },
  { id: "priority", label: "Priority", icon: "clock" },
];
const SORT_KEY = "my-agents.sort";
const sortOf = () => SORTS.find((s) => s.id === shared(SORT_KEY)) ?? SORTS[0];
const INDENT = 14;

// Every agent gets its own pet: a pixel sprite (pets/*.svg, tinted) in a Catppuccin Mocha accent.
const PETS = ["cat", "fox", "panda", "duck", "owl", "bunny", "turtle", "dragon", "bear", "frog", "ghost", "octopus", "invader"];
// Ordered so neighbours contrast: red, green, blue, yellow, mauve, teal, peach, lavender, pink, sky, maroon, sapphire.
const PET_COLORS = ["#f38ba8", "#a6e3a1", "#89b4fa", "#f9e2af", "#cba6f7", "#94e2d5", "#fab387", "#b4befe", "#f5c2e7", "#89dceb", "#eba0ac", "#74c7ec"];
const machineOf = (addr) => (addr.includes("/") ? addr.split("/")[0] : "");

// Gone agents: only judged on machines the list has heard from, since right after a start,
// or while a machine connects, its agents aren't listed yet.
function goneIn(all) {
  const heard = new Set(all.map((k) => machineOf(k.address)));
  const live = new Set(all.filter((k) => k.state === "live").map((k) => k.address));
  return (addr) => heard.has(machineOf(addr)) && !live.has(addr);
}
const SECTION = { 4: "Needs you", 3: "Done", 2: "Working", 1: "Idle" };
const SECTION_STATUS = { 4: "blocked", 3: "done", 2: "working" };

function duration(ms) {
  const m = Math.floor(ms / 60000);
  if (m < 1) return "<1m";
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  return h < 24 ? `${h}h` : `${Math.floor(h / 24)}d`;
}

// The agent's icon from the app's program table: a one-color glyph or a picture.
function icon(p, size, color) {
  if (!p?.icon) return null;
  return p.tint ? svg(p.icon).size(size).flex_none().text_color(color) : image(p.icon).w(size).h(size).flex_none();
}

export default class Agents extends View {
  init(_props, cx) {
    if (props()?.sortMenu) return;
    this.folded = new Set();
    // Each agent's last state and when it changed (ms), kept across restarts.
    this.changed = JSON.parse(localStorage.getItem("changed") ?? "{}");
    this.pins = JSON.parse(localStorage.getItem("pins") ?? "[]");
    this.pets = JSON.parse(localStorage.getItem("pets") ?? "{}");
    cx.timer.every(30000, (cx) => cx.notify());
  }

  // Stamps agents whose state changed. Done and idle count as one: looking at a
  // finished agent turns done into idle, and looking isn't the agent doing anything.
  track(list, now) {
    let dirty = false;
    for (const a of list) {
      const s = a.status === "done" ? "idle" : a.status;
      if (this.changed[a.address]?.s === s) continue;
      this.changed[a.address] = { s, at: now };
      dirty = true;
    }
    if (!dirty) return;
    // Gone and old: the list alone is empty right after a start.
    const listed = new Set(list.map((a) => a.address));
    for (const [k, c] of Object.entries(this.changed)) if (!listed.has(k) && now - c.at > 30 * 86400000) delete this.changed[k];
    localStorage.setItem("changed", JSON.stringify(this.changed));
  }

  setPins(pins) {
    this.pins = pins;
    localStorage.setItem("pins", JSON.stringify(pins));
  }

  // Drops pins of agents that ended. Only for machines the list has heard from:
  // right after a start, or while a machine connects, its agents aren't listed yet.
  prunePins(gone) {
    const kept = this.pins.filter((p) => !gone(p));
    if (kept.length !== this.pins.length) this.setPins(kept);
  }

  // Keeps each agent's [sprite, color] for its whole life, across restarts. A new agent takes
  // the first least-used sprite and color, so pets on screen differ in both when they can.
  adoptPets(list, gone) {
    let dirty = false;
    for (const [addr, [p, c]] of Object.entries(this.pets)) {
      if (!gone(addr) && PETS.includes(p) && PET_COLORS.includes(c)) continue;
      delete this.pets[addr];
      dirty = true;
    }
    const least = (options, at) => {
      const uses = (o) => Object.values(this.pets).filter((pet) => pet[at] === o).length;
      return options.reduce((best, o) => (uses(o) < uses(best) ? o : best));
    };
    for (const a of list) {
      if (this.pets[a.address]) continue;
      this.pets[a.address] = [least(PETS, 0), least(PET_COLORS, 1)];
      dirty = true;
    }
    if (dirty) localStorage.setItem("pets", JSON.stringify(this.pets));
  }

  pet(a, size) {
    const [p, c] = this.pets[a.address] ?? [PETS[0], PET_COLORS[0]];
    return svg(`mods/my-agents/pets/${p}.svg`).size(size).flex_none().text_color(c);
  }

  // Faint tree guides, one INDENT column per level: "│" passes through, "├" and "└" branch off
  // (└ ends here), "" is blank. x puts each line under its parent's chevron; reach carries the
  // branch-off tick across the row's gap to what it points at.
  guides(t, lines, h, reach) {
    const line = (el) => el.absolute().bg(t.border);
    return h_flex().flex_none().h(h).children(lines.map((l, i) => {
      const x = i ? 13 : 7;
      return div().relative().w(INDENT).h(h).flex_none()
        .when(l === "│" || l === "├", (el) => el.child(line(div()).left(x).top(0).w(1).h(h)))
        .when(l === "└", (el) => el.child(line(div()).left(x).top(0).w(1).h(h / 2)))
        .when(l === "├" || l === "└", (el) => el.child(line(div()).left(x).top(h / 2).w(INDENT - x + reach).h(1)));
    }));
  }

  since(a) {
    return this.changed[a.address]?.at ?? 0;
  }

  // Looking at a done agent makes it idle; it stays with Done until you leave
  // it rather than drop away under you. Any other change moves it at once.
  hold(list) {
    const cur = list.find((a) => a.focused);
    const same = cur && cur.address === this.held?.address;
    // Arriving, or any change but done turning idle, sets what it was.
    if (!same || cur.status !== "idle") this.held = cur ? { address: cur.address, rank: RANK[cur.status] ?? 0 } : null;
  }

  rank(a) {
    const sawDone = a.address === this.held?.address && this.held.rank === RANK.done && a.status === "idle";
    return sawDone ? RANK.done : RANK[a.status] ?? 0;
  }

  sortMenu(t) {
    const on = sortOf().id;
    return v_flex()
      .w_full()
      .p(5)
      .bg(t.popover)
      .text_size(12.8)
      .child(div().px(8).pt(4).pb(6).text_size(11).text_color(t.dim).child("Sort agents"))
      .children(SORTS.map((s) => h_flex()
        .id(`sort-${s.id}`)
        .h(31)
        .px(8)
        .gap(9)
        .rounded(7)
        .items_center()
        .text_color(t.foreground)
        .hover((el) => el.bg(t.hover))
        .on_click(() => {
          setShared(SORT_KEY, s.id);
          closeOverlay(props().overlay);
        })
        .child(svg(`mods/workspace-chrome/${s.icon}.svg`).size(14).flex_none().text_color(t.muted))
        .child(div().flex_1().child(s.label))
        .when(s.id === on, (el) => el.child(svg("mods/workspace-chrome/check.svg").size(14).flex_none().text_color(t.foreground)))));
  }

  peek(addr) {
    run("terminal.peek", { terminal: addr });
  }

  // Live sub-agents of `addr`, depth first, each with the tree lines in front of it.
  tree(all, addr, lead = []) {
    const kids = all.filter((k) => k.parent === addr && k.state === "live");
    return kids.flatMap((k, i) => {
      const last = i === kids.length - 1;
      return [
        { t: k, lines: [...lead, last ? "└" : "├"] },
        ...this.tree(all, k.address, [...lead, last ? "" : "│"]),
      ];
    });
  }

  subRow(t, s, shown, cont) {
    const k = s.t;
    const st = kidStatus(k, this.listed);
    const pic = icon(k.program, 14, t.foreground);
    const on = k.address === shown;
    return h_flex()
      .id(`sub-${k.address}`)
      .pl(8)
      .pr(8)
      .rounded(8)
      .h(26)
      .flex_none()
      .gap(0)
      .items_center()
      .text_size(12.5)
      .when(on, (el) => el.bg(t.raised))
      .hover((s) => s.bg(t.hover))
      .on_click(() => this.peek(k.address))
      .when(cont.length > 0, (el) => el.child(this.guides(t, cont, 26, 0)))
      // Under its agent's pet: past the guide gap, then 31px in.
      .child(div().w((cont.length ? 9 : 0) + 31).flex_none())
      .children(s.lines.map(l => div().self_start().mt(-4).w(9).h(17).mr(4).flex_none()
        .when(Boolean(l), el => el.border_l(1).border_color(t.border))
        .when(l === "└" || l === "├", el => el.border_b(1).rounded_bl(6))))
      .when(Boolean(pic), el => el.child(div().mr(8).child(pic)))
      .child(div().flex_1().truncate().text_color(on ? t.foreground : t.muted).child(k.name || k.template || k.program?.name || k.address))
      .child(this.status(t, st));
  }

  // One 14px slot, so every row's status sits in the same column.
  status(t, st) {
    const slot = h_flex().size(14).flex_none().items_center().justify_center();
    if (st === "working") return slot.child(svg("mods/workspace-chrome/spinner.svg").size(12).text_color(t.working).spin(900));
    if (st === "blocked") return slot.rounded(4).bg(t.blocked + "26").text_size(10.5).font_bold().text_color(t.blocked).child("!");
    if (st === "done") return slot.child(svg("mods/workspace-chrome/check.svg").size(13).text_color(t.ansi2 ?? t.done));
    if (st === "idle") return slot.child(div().size(7).rounded(4).border(1.5).border_color(t.idle ?? t.dim));
    return slot;
  }

  // The right edge of every row: a fixed-width timer (or count, or pin) column, then the status.
  right(t, left, st) {
    return h_flex()
      .flex_none()
      .gap(6)
      .items_center()
      .child(h_flex().w(30).flex_none().justify_end().text_size(11).text_color(t.dim).child(left ?? ""))
      .child(this.status(t, st));
  }

  render(cx) {
    const t = theme();
    if (props()?.sortMenu) return this.sortMenu(t);
    const collapsed = narrow();
    const sort = sortOf();
    let list = agents();
    this.listed = new Map(list.map((a) => [a.address, a.status]));
    const all = terminals();
    const shown = focused()?.agent;
    const now = Date.now();
    this.track(list, now);
    this.hold(list);
    const byPriority = sort.id === "priority";
    const urgency = new Map(list.map((a) => [a.address, Math.max(this.rank(a), ...this.tree(all, a.address).map((s) => RANK[kidStatus(s.t, this.listed)] ?? 0))]));
    const rankOf = (a) => urgency.get(a.address);
    if (byPriority) list = [...list].sort((x, y) => rankOf(y) - rankOf(x) || this.since(y) - this.since(x) || y.seq - x.seq);
    const gone = goneIn(all);
    this.prunePins(gone);
    this.adoptPets(list, gone);
    const pinned = this.pins.map((p) => list.find((a) => a.address === p)).filter(Boolean);
    const rest = list.filter((a) => !this.pins.includes(a.address));
    list = [...pinned, ...rest];
    setAgentOrder(list.map((a) => a.address));

    const withKids = (a, i, lines = [], suffix = whereOf(a)) => {
      const subs = collapsed ? [] : this.tree(all, a.address);
      const r = row(a, i, subs, lines, suffix);
      if (this.folded.has(a.address)) return [r];
      const cont = lines.map((l) => (l === "├" || l === "│" ? "│" : ""));
      return [r, ...subs.map((s) => this.subRow(t, s, shown, cont))];
    };

    // The fold chevron; folded, the count too, colored by the most urgent one.
    const fold = (a, subs) => {
      const sts = subs.map((s) => kidStatus(s.t, this.listed));
      const top = sts.reduce((m, s) => ((RANK[s] ?? 0) > (RANK[m] ?? 0) ? s : m), null);
      const open = !this.folded.has(a.address);
      return div()
        .id(`fold-${a.address}`)
        .h(18)
        .px(3)
        .gap(3)
        .flex()
        .items_center()
        .rounded(4)
        .text_size(11)
        .text_color(open ? t.dim : (t[top] ?? t.dim))
        .cursor_pointer()
        .hover((s) => s.bg(t.hover).text_color(t.foreground))
        .on_click((_e, cx) => {
          cx.stop_propagation();
          open ? this.folded.add(a.address) : this.folded.delete(a.address);
          cx.notify();
        })
        .child(svg(`mods/workspace-chrome/chevron-${open ? "down" : "right"}.svg`).size(11).flex_none())
        .when(!open, (el) => el.child(String(subs.length)));
    };

    const tabs = new Map(spaces().flatMap((s) => s.tabs).map((tab) => [tab.id, tab]));

    const named = (a) => Boolean(tabs.get(a.tab)?.named) && a.tab_name !== a.space_name;
    const tabLabel = (a) => (tabs.get(a.tab)?.named ? a.tab_name : `Tab ${a.tab_index}`);
    // Where an agent is, as far as nothing above its row says it: no "q/1", no "q/q".
    const whereOf = (a) => (named(a) ? `${a.space_name}/${a.tab_name}` : a.space_name);

    // A row reads "session \u00b7 suffix": the session is the agent's terminal title. The tree
    // passes no suffix; priority and pinned rows name the place, unless it repeats the session.
    const row = (a, i, subs = [], lines = [], suffix = null) => {
      const pic = this.pet(a, 16);
      const session = clean(a.title) || a.program?.name || a.address;
      const more = suffix && suffix.toLowerCase() !== session.toLowerCase() ? suffix : null;
      // An agent on another machine (`vps/t7`) says which.
      const machine = a.address.includes("/") ? a.address.split("/")[0] : null;
      if (collapsed) {
        return h_flex()
          .id(`ag-${a.address}`)
          .h(26)
          .px(8)
          .flex_none()
          .items_center()
          .gap(4)
          .hover((s) => s.bg(t.hover))
          .on_click(() => run("pane.focus", { pane: a.pane }))
          .child(h_flex().flex_1().min_w(0).justify_end().child(pic))
          .child(this.status(t, a.status));
      }
      const when = this.since(a);
      const isPinned = this.pins.includes(a.address);
      const pin = div()
        .id(`pin-${a.address}`)
        .flex_none()
        .p(2)
        .rounded(4)
        .text_color(t.dim)
        .cursor_pointer()
        .hover((s) => s.bg(t.hover).text_color(t.foreground))
        .tooltip(isPinned ? "Unpin" : "Pin to the top")
        .on_click((_e, cx) => {
          cx.stop_propagation();
          this.setPins(isPinned ? this.pins.filter((p) => p !== a.address) : [...this.pins, a.address]);
          // The row moves away from under the pointer, and no hover-out comes.
          this.hovered = null;
          cx.notify();
        })
        .child(svg(`mods/workspace-chrome/${isPinned ? "pin-off" : "pin"}.svg`).size(13));
      return h_flex()
        .id(`ag-${a.address}`)
        .pl(8)
        .pr(8)
        .h(30)
        .flex_none()
        .rounded(8)
        .items_center()
        .gap(9)
        .when(a.focused, (el) => el.bg(t.selected))
        .cursor_pointer()
        .hover((s) => s.bg(t.hover))
        .on_hover((on, cx) => {
          if (on) this.hovered = a.address;
          else if (this.hovered === a.address) this.hovered = null;
          cx.notify();
        })
        .on_click(() => run("pane.focus", { pane: a.pane }))
        .when(lines.length > 0, (el) => el.child(this.guides(t, lines, 30, 7)))
        .when(Boolean(pic), el => el.child(div().id(`agent-fold-${a.address}`)
          .when(subs.length > 0, el => el.on_click((_e, cx) => {
            cx.stop_propagation();
            this.folded.has(a.address) ? this.folded.delete(a.address) : this.folded.add(a.address);
            cx.notify();
          })).child(pic)))
        .child(h_flex().flex_1().min_w(0).gap(5).items_center()
          .child(div().min_w(0).max_w(more ? 150 : 400).truncate().text_color(t.foreground).child(session))
          .when(Boolean(more), el => el.child(div().flex_1().min_w(0).truncate().text_color(t.dim).child(`\u00b7 ${more}`))))
        .when(Boolean(machine), el => el.child(div().text_size(10).text_color(t.dim).child(machine)))
        .when(subs.length > 0, el => el.child(fold(a, subs)))
        .child(this.right(t, this.hovered === a.address ? pin : when ? duration(now - when) : "", a.status));
    };

    // A space or tab line in the tree: the chevron folds it, the rest selects it.
    // Folded, the timer column holds how many agents it hides and the status their most urgent one.
    const branch = (id, label, members, lines, select) => {
      const depth = lines.length;
      const open = !this.folded.has(id);
      const top = members.reduce((m, a) => ((RANK[a.status] ?? 0) > (RANK[m] ?? 0) ? a.status : m), null);
      return h_flex()
        .id(id)
        .pl(8)
        .pr(8)
        .h(28)
        .flex_none()
        .rounded(8)
        .gap(6)
        .items_center()
        .cursor_pointer()
        .hover((s) => s.bg(t.hover))
        .on_click(select)
        .when(depth > 0, (el) => el.child(this.guides(t, lines, 28, 4)))
        .child(div()
          .id(`${id}-fold`)
          .p(2)
          .rounded(4)
          .text_color(t.dim)
          .hover((s) => s.bg(t.hover).text_color(t.foreground))
          .on_click((_e, cx) => {
            cx.stop_propagation();
            open ? this.folded.add(id) : this.folded.delete(id);
            cx.notify();
          })
          .child(svg(`mods/workspace-chrome/chevron-${open ? "down" : "right"}.svg`).size(11).flex_none()))
        .child(div().flex_1().min_w(0).truncate().font_medium().text_size(depth ? 12.5 : 13).text_color(depth ? t.muted : t.foreground).child(label))
        .child(this.right(t, open ? "" : String(members.length), open ? null : top));
    };

    // Space \u203a tab \u203a agents, in agents() order, which is the window's space, tab, pane order.
    const treeRows = () => {
      const bySpace = [];
      for (const a of rest) {
        if (bySpace.at(-1)?.id !== a.space) bySpace.push({ id: a.space, name: a.space_name, members: [], tabs: [] });
        const sp = bySpace.at(-1);
        sp.members.push(a);
        if (sp.tabs.at(-1)?.id !== a.tab) sp.tabs.push({ id: a.tab, members: [] });
        sp.tabs.at(-1).members.push(a);
      }
      // A level with one child folds into it: a lone agent stands in for its space, and a lone
      // tab or a lone pane gets no line of its own.
      const elbow = (i, n) => (i === n - 1 ? "└" : "├");
      return bySpace.flatMap((sp) => {
        if (sp.members.length === 1) return withKids(sp.members[0], 0, [], null);
        const head = branch(`sp-${sp.id}`, sp.name, sp.members, [], () => run("space.select", { id: sp.id }));
        if (this.folded.has(`sp-${sp.id}`)) return [head];
        if (sp.tabs.length === 1) return [head, ...sp.members.flatMap((a, i) => withKids(a, i, [elbow(i, sp.members.length)], null))];
        return [head, ...sp.tabs.flatMap((tb, j) => {
          const first = tb.members[0];
          const own = elbow(j, sp.tabs.length);
          if (tb.members.length === 1) return withKids(first, 0, [own], null);
          const pass = own === "└" ? "" : "│";
          return [
            branch(`tb-${tb.id}`, tabLabel(first), tb.members, [own], () => run("tab.select", { id: tb.id })),
            ...(this.folded.has(`tb-${tb.id}`) ? [] : tb.members.flatMap((a, i) => withKids(a, i, [pass, elbow(i, tb.members.length)], null))),
          ];
        })];
      });
    };

    // Priority sections in list order (the tree builds its own groups).
    const groups = pinned.length ? [{ id: "pinned", label: "Pinned", status: null, members: pinned }] : [];
    for (const a of byPriority ? rest : []) {
      const r = rankOf(a) || RANK.idle;
      const id = `sec-${r}`;
      if (groups.at(-1)?.id === id) groups.at(-1).members.push(a);
      else groups.push({ id, label: SECTION[r], status: SECTION_STATUS[r], members: [a] });
    }

    // A group's header; it folds the group. Folded, it says how many and the most urgent state.
    const header = (g) => {
      const open = !this.folded.has(g.id);
      const top = g.members.reduce((m, a) => ((RANK[a.status] ?? 0) > (RANK[m] ?? 0) ? a.status : m), null);
      return h_flex()
        .id(g.id)
        .px(8)
        .pt(8)
        .h(30)
        .flex_none()
        .gap(6)
        .items_center()
        .text_size(11)
        .font_medium()
        .cursor_pointer()
        .hover((s) => s.text_color(t.foreground))
        .on_click((_e, cx) => {
          open ? this.folded.add(g.id) : this.folded.delete(g.id);
          cx.notify();
        })
        .child(div().truncate().text_color(t[g.status] ?? t.dim).child(g.label))
        .when(byPriority || !open, (el) => el.child(div().text_color(t.dim).child(String(g.members.length))))
        .child(div().flex_1())
        .when(!open, (el) => el
          .when(!byPriority && top && top !== "idle", (el) => el.child(this.status(t, top)))
          .child(svg("mods/workspace-chrome/chevron-right.svg").size(11).flex_none().text_color(t.dim)));
    };
    const flat = (members) => members.flatMap((a, i) => withKids(a, i));
    const rows = collapsed
      ? flat(list)
      : [
          ...groups.flatMap((g) => [header(g), ...(this.folded.has(g.id) ? [] : flat(g.members))]),
          ...(byPriority ? [] : treeRows()),
        ];

    return v_flex()
      .size_full()
      .min_h(0)
      .bg(t.chrome)
      .border_t(1)
      .border_color(t.border)
      .text_size(13)
      .when(!collapsed, (el) =>
        el.child(
          h_flex()
            .px(16)
            .h(41)
            .pt(14)
            .pb(5)
            .flex_none()
            .items_center()
            .justify_between()
            .child(div().text_size(11.5).font_medium().text_color(t.dim).child("Agents"))
            .child(
              div()
                .id("ag-sort")
                .text_size(11)
                .text_color(t.dim)
                .hover((s) => s.text_color(t.foreground))
                .on_click(() => openOverlay({ mod: "my-agents", kind: "popup", width: 200, height: 140, props: { sortMenu: true } }))
                .tooltip(`Sort agents: ${sort.label}`)
                .child(svg(`mods/workspace-chrome/${sort.icon}.svg`).size(14)),
            ),
        ),
      )
      .when(list.length === 0 && !collapsed, (el) =>
        el.child(div().px(12).text_size(12).text_color(t.dim).child("No agent status yet. Agents show up here when they report their status. Older versions and agents inside tmux may not.")),
      )
      .child(v_flex().id("ag-list").flex_1().min_h(0).px(collapsed ? 0 : 8).overflow_y_scrollbar().mode("always").children(rows))
      .child(
        h_flex()
          .h(28)
          .px(12)
          .items_center()
          .justify_end()
          .when(collapsed, (el) => el.justify_center().px(0))
          .child(
            div()
              .id("collapse")
              .text_size(13)
              .text_color(t.dim)
              .hover((s) => s.text_color(t.foreground))
              .on_click(toggleSidebar)
              .child(collapsed ? "»" : ""),
          ),
      );
  }
}
