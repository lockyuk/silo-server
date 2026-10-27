// The theme songs plugin's admin page script, mounted inside a shadow root
// on Admin → Library & Metadata. Only the API, the mount point, the daily
// switch and the plugin-specific phrases differ from the plugin.
export function mountThemeSongsPage(root, api) {
  const $ = (s) => root.querySelector(s);
  const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  const STATUS = {
    downloaded: ["Downloaded", "ok"], present: ["Already had one", "ok"], missing: ["No theme yet", "warn"],
    pending: ["Not checked yet", "info"], skipped: ["Skipped", ""], no_tvdb: ["No TVDB id", "warn"], error: ["Error", "bad"],
  };
  const ORDER = ["downloaded", "present", "missing", "pending", "skipped", "no_tvdb", "error"];
  let state = null, filter = "", poll = 0, gone = false;

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

    const total = state.shows.length;
    $("#tiles").innerHTML = [["", "All shows", total]].concat(ORDER.filter((k) => state.counts[k]).map((k) => [k, STATUS[k][0], state.counts[k]]))
      .map(([k, label, n]) => `<button class="tile" data-filter="${k}" aria-pressed="${filter === k}"><div class="muted">${esc(label)}</div><div class="n">${n}</div></button>`).join("");

    const q = $("#q").value.trim().toLowerCase();
    const rows = state.shows.filter((s) => (!filter || s.s === filter) && (!q || s.n.toLowerCase().includes(q)));
    $("#shown").textContent = rows.length === total ? "" : `${rows.length} of ${total}`;
    $("#rows").innerHTML = rows.map((s) => {
      const [label, cls] = STATUS[s.s] || [s.s, ""];
      const actions = [];
      if (s.s === "downloaded") actions.push(`<button class="small danger" data-act="skip" data-id="${esc(s.i)}" title="Delete this theme and never download it again">Remove and skip</button>`);
      if (["missing", "pending", "error", "no_tvdb", "skipped"].includes(s.s)) actions.push(`<button class="small" data-act="retry" data-id="${esc(s.i)}">Try again</button>`);
      if (s.s === "missing" || s.s === "pending") actions.push(`<button class="small" data-act="skip" data-id="${esc(s.i)}" title="Stop looking for this show">Skip</button>`);
      return `<tr>
        <td><b>${esc(s.n)}</b>${s.v ? `<div class="mono hint">TVDB ${esc(s.v)}</div>` : ""}</td>
        <td><span class="pill ${cls}">${esc(label)}</span>${s.d ? `<div class="hint">${esc(s.d)}</div>` : ""}</td>
        <td>${esc(fmt(s.c))}</td>
        <td><div class="row" style="justify-content:flex-end">${actions.join("")}</div></td></tr>`;
    }).join("") || `<tr><td colspan="4" class="muted">${total ? "No shows match." : "No TV shows yet."}</td></tr>`;

    const r = state.report;
    $("#last").textContent = r ? `Last check ${fmt(r.finished)}: ${r.downloaded} downloaded, ${r.checked} checked of ${r.shows}` + (r.errors?.length ? `. ${r.errors.length} error(s): ${r.errors[0]}` : "") : "";
    if (root.activeElement !== $("#recheck-days")) $("#recheck-days").value = state.settings.recheck_days;
    $("#enabled").checked = !!state.settings.enabled;
  }

  $("#tiles").addEventListener("click", (e) => {
    const t = e.target.closest("[data-filter]");
    if (!t) return;
    filter = t.dataset.filter;
    render();
  });
  $("#q").addEventListener("input", render);

  async function sync(force) {
    try {
      state = await api("sync", { method: "POST", body: { force } });
      toast(force ? "Rechecking shows without a theme." : "Looking for missing themes.");
      load();
    } catch (e) { toast(e.message, true); }
  }
  $("#sync").addEventListener("click", () => sync(false));
  $("#recheck").addEventListener("click", () => sync(true));

  $("#rows").addEventListener("click", async (e) => {
    const b = e.target.closest("button[data-act]");
    if (!b) return;
    b.disabled = true;
    try {
      const s = await api(`shows/${encodeURIComponent(b.dataset.id)}/${b.dataset.act}`, { method: "POST" });
      toast(`${s.n}: ${(STATUS[s.s] || [s.s])[0]}`);
      load();
    } catch (err) { toast(err.message, true); b.disabled = false; }
  });

  $("#save-settings").addEventListener("click", async () => {
    try {
      state = await api("settings", { method: "PUT", body: { recheck_days: Number($("#recheck-days").value), enabled: $("#enabled").checked } });
      render();
      toast("Saved.");
    } catch (e) { toast(e.message, true); }
  });

  load().catch((e) => { $("#problems").innerHTML = `<div class="banner">Couldn't load: ${esc(e.message)}</div>`; });

  return () => {
    gone = true;
    clearTimeout(poll);
  };
}
