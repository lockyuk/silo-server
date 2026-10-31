// The theme songs plugin's admin page script, mounted inside a shadow root
// on Admin → Library & Metadata. Only the API, the mount point, the daily
// switch and the plugin-specific phrases differ from the plugin.
export function mountThemeSongsPage(root, api) {
  const $ = (s) => root.querySelector(s);
  const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  const STATUS = {
    downloaded: ["Downloaded", "ok"], present: ["Already had one", "ok"], missing: ["No theme yet", "warn"],
    pending: ["Not checked yet", "info"], skipped: ["Skipped", ""], no_tvdb: ["No TVDB id", "warn"], error: ["Error", "bad"],
    review: ["To review", "info"],
  };
  const ORDER = ["review", "downloaded", "present", "missing", "pending", "skipped", "no_tvdb", "error"];
  const SOURCE = { plex: "Plex", themerrdb: "ThemerrDB", youtube: "YouTube search" };
  let state = null, filter = "", type = "", poll = 0, gone = false, page = 0;
  let perPage = 50;
  try { perPage = Number(localStorage.getItem("silo.themeSongs.perPage") ?? 50); } catch { /* storage blocked */ }
  const dur = (n) => n ? `${Math.floor(n / 60)}:${String(n % 60).padStart(2, "0")}` : "";

  function toast(msg, bad = false) {
    const t = document.createElement("div");
    t.className = "toast" + (bad ? " bad" : "");
    t.setAttribute("role", "status");
    t.textContent = msg;
    $(".ts-root").appendChild(t);
    setTimeout(() => t.remove(), bad ? 7000 : 3500);
  }
  const fmt = (t) => !t || t.startsWith("0001") ? "–" : new Date(t).toLocaleString(undefined, { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });

  async function load() {
    state = await api("state");
    if (gone) return;
    render();
    clearTimeout(poll);
    if (state.running) poll = setTimeout(() => load().catch(() => {}), 2000);
  }

  function render() {
    const p = state.problems || [];
    $("#problems").innerHTML = p.length ? `<div class="banner">${p.map(esc).join("<br>")}</div>` : "";
    $("#progress").hidden = !state.running;
    $("#progress").textContent = state.running ? "Working… " + (state.progress || "") : "";
    $("#sync").disabled = $("#recheck").disabled = state.running;

    const typed = state.shows.filter((s) => !type || s.t === type);
    const total = typed.length;
    const counts = {};
    for (const s of typed) counts[s.s] = (counts[s.s] ?? 0) + 1;
    $("#tiles").innerHTML = [["", type === "movie" ? "All films" : type === "series" ? "All shows" : "All titles", total]].concat(ORDER.filter((k) => counts[k]).map((k) => [k, STATUS[k][0], counts[k]]))
      .map(([k, label, n]) => `<button class="tile" data-filter="${k}" aria-pressed="${filter === k}"><div class="muted">${esc(label)}</div><div class="n">${n}</div></button>`).join("");

    const q = $("#q").value.trim().toLowerCase();
    const rows = typed.filter((s) => (!filter || s.s === filter) && (!q || s.n.toLowerCase().includes(q)));
    $("#shown").textContent = rows.length === total ? "" : `${rows.length} of ${total}`;
    $("#review-hint").hidden = filter !== "review";
    const pages = perPage ? Math.max(1, Math.ceil(rows.length / perPage)) : 1;
    page = Math.min(page, pages - 1);
    const shown = perPage ? rows.slice(page * perPage, (page + 1) * perPage) : rows;
    $("#page-label").textContent = `Page ${page + 1} of ${pages}`;
    $("#prev").disabled = page === 0;
    $("#next").disabled = page >= pages - 1;
    $("#per-page").value = String(perPage);
    $("#rows").innerHTML = shown.map((s) => {
      const [label, cls] = STATUS[s.s] || [s.s, ""];
      const actions = [];
      if (s.s === "downloaded") actions.push(`<button class="small danger" data-act="skip" data-id="${esc(s.i)}" title="Delete this theme and never download it again">Remove and skip</button>`);
      if (["missing", "pending", "error", "no_tvdb", "skipped"].includes(s.s)) actions.push(`<button class="small" data-act="retry" data-id="${esc(s.i)}">Try again</button>`);
      if (s.s === "review") actions.push(`<button class="small primary" data-act="approve" data-id="${esc(s.i)}">Keep</button>`, `<button class="small" data-act="reject" data-id="${esc(s.i)}">Reject</button>`);
      if (s.s === "missing" || s.s === "pending") actions.push(`<button class="small" data-act="skip" data-id="${esc(s.i)}" title="Stop looking for this show">Skip</button>`);
      return `<tr>
        <td><span class="tag">${s.t === "movie" ? "FILM" : "TV"}</span><b>${esc(s.n)}</b>${s.y ? ` <span class="muted">(${esc(s.y)})</span>` : ""}${s.v ? `<div class="mono hint">TVDB ${esc(s.v)}</div>` : ""}</td>
        <td><span class="pill ${cls}">${esc(label)}</span>${s.src && s.s === "downloaded" ? `<div class="hint">from ${esc(SOURCE[s.src] || s.src)}</div>` : ""}${s.s === "review" && s.r ? `<div class="hint"><a href="${esc(s.r.url)}" target="_blank" rel="noopener noreferrer">${esc(s.r.title)}</a></div><div class="hint">${esc([s.r.channel, dur(s.r.duration_seconds)].filter(Boolean).join(" · "))}</div>` : ""}${s.d ? `<div class="hint">${esc(s.d)}</div>` : ""}</td>
        <td>${esc(fmt(s.c))}</td>
        <td><div class="row" style="justify-content:flex-end">${actions.join("")}</div></td></tr>`;
    }).join("") || `<tr><td colspan="4" class="muted">${total ? "Nothing matches." : "No titles yet."}</td></tr>`;

    const r = state.report;
    $("#last").textContent = r ? `Last check ${fmt(r.finished)}: ${r.downloaded} downloaded, ${r.checked} checked of ${r.shows}` + (r.errors?.length ? `. ${r.errors.length} error(s): ${r.errors[0]}` : "") : "";
    if (root.activeElement !== $("#recheck-days")) $("#recheck-days").value = state.settings.recheck_days;
    $("#enabled").checked = !!state.settings.enabled;
    $("#themerrdb").checked = !!state.settings.themerrdb;
    $("#youtube-search").checked = !!state.settings.youtube_search;
    for (const b of root.querySelectorAll("#types button")) b.setAttribute("aria-pressed", String(b.dataset.type === type));
  }

  $("#tiles").addEventListener("click", (e) => {
    const t = e.target.closest("[data-filter]");
    if (!t) return;
    filter = t.dataset.filter;
    page = 0;
    render();
    $("#list").scrollIntoView({ behavior: "smooth", block: "start" });
  });
  $("#q").addEventListener("input", () => { page = 0; render(); });
  $("#per-page").addEventListener("change", (e) => {
    perPage = Number(e.target.value);
    try { localStorage.setItem("silo.themeSongs.perPage", String(perPage)); } catch { /* storage blocked */ }
    page = 0;
    render();
  });
  $("#prev").addEventListener("click", () => { page = Math.max(0, page - 1); render(); $("#list").scrollIntoView({ block: "start" }); });
  $("#next").addEventListener("click", () => { page += 1; render(); $("#list").scrollIntoView({ block: "start" }); });
  $("#types").addEventListener("click", (e) => {
    const b = e.target.closest("button[data-type]");
    if (!b) return;
    type = b.dataset.type;
    filter = "";
    page = 0;
    render();
  });

  async function sync(force) {
    try {
      state = await api("sync", { method: "POST", body: { force } });
      toast(force ? "Rechecking titles without a theme." : "Looking for missing themes.");
      load();
    } catch (e) { toast(e.message, true); }
  }
  $("#sync").addEventListener("click", () => sync(false));
  $("#recheck").addEventListener("click", () => sync(true));

  async function act(e) {
    const b = e.target.closest("button[data-act]");
    if (!b) return;
    b.disabled = true;
    if (b.dataset.act === "approve") b.textContent = "Downloading…";
    try {
      const s = await api(`shows/${encodeURIComponent(b.dataset.id)}/${b.dataset.act}`, { method: "POST" });
      // Keep on a video YouTube no longer serves turns it down and searches
      // again, so the title comes back still waiting, with the next result.
      if (b.dataset.act === "approve" && s.s !== "downloaded") {
        toast(s.s === "review"
          ? `${s.n}: that video isn't on YouTube any more, so here's the next result to review.`
          : `${s.n}: that video isn't on YouTube any more, and no other result was found.`, true);
      } else {
        toast(`${s.n}: ${(STATUS[s.s] || [s.s])[0]}`);
      }
      load();
    } catch (err) { toast(err.message, true); b.disabled = false; }
  }
  $("#rows").addEventListener("click", act);

  $("#save-settings").addEventListener("click", async () => {
    try {
      state = await api("settings", { method: "PUT", body: { recheck_days: Number($("#recheck-days").value), enabled: $("#enabled").checked, themerrdb: $("#themerrdb").checked, youtube_search: $("#youtube-search").checked } });
      render();
      toast("Saved.");
    } catch (e) { toast(e.message, true); }
  });

  load().catch((e) => { if (!gone) $("#problems").innerHTML = `<div class="banner">Couldn't load: ${esc(e.message)}</div>`; });

  return () => {
    gone = true;
    clearTimeout(poll);
  };
}
