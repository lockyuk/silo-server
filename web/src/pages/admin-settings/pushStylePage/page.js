// The push notifications admin page, in the email alerts page's style and
// mounted the same way, inside a shadow root on Admin → Notifications.
export function mountPushStylePage(root, api) {
  const $ = (s) => root.querySelector(s);
  const $$ = (s) => [...root.querySelectorAll(s)];
  const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

  const VARS = {
    episode: [["series", "Show name"], ["code", "S01E02"], ["season", "Season number"], ["episode", "Episode number"], ["episode_title", "Episode title"], ["year", "Show's first year"], ["overview", "Synopsis"]],
    movie: [["title", "Movie title"], ["year", "Release year"], ["overview", "Synopsis"]],
    group: [["series", "Show name"], ["count", "How many episodes"], ["codes", "S01E03–E05"], ["year", "Show's first year"], ["#episodes", "Each episode: {{#episodes}}{{code}} {{/episodes}}"]],
  };

  let saved = null, draft = null, defaults = null, kind = "episode", gone = false, previewTimer = 0, previewSeq = 0;

  function toast(msg, bad = false) {
    const t = document.createElement("div");
    t.className = "toast" + (bad ? " bad" : "");
    t.setAttribute("role", "status");
    t.textContent = msg;
    $(".wrap").appendChild(t);
    setTimeout(() => t.remove(), bad ? 7000 : 3500);
  }

  const dirty = () => JSON.stringify(saved) !== JSON.stringify(draft);
  function markDirty() {
    $("#dirty").hidden = !dirty();
    $("#save").disabled = !dirty();
  }

  function renderVars() {
    $("#vars").innerHTML = `<details open><summary>Variables</summary><p>Click to insert at the cursor. Sections like {{#episode_title}}…{{/episode_title}} show only when the value is there.</p>` +
      VARS[kind].map(([v, d]) => `<code data-var="${esc(v)}" title="${esc(d)}">{{${esc(v)}}}</code>`).join("") + `</details>`;
  }

  function fill() {
    $("#t-title").value = draft[kind + "_title"];
    $("#t-body").value = draft[kind + "_body"];
    $$("[data-kind]").forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.kind === kind)));
    $$("[data-image]").forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.image === draft.image)));
    $("#synopsis").checked = draft.synopsis;
    $("#synopsis-max").value = draft.synopsis_max;
    $("#batch").value = draft.batch_minutes;
    $("#quiet").checked = draft.quiet_enabled;
    $("#quiet-start").value = draft.quiet_start;
    $("#quiet-end").value = draft.quiet_end;
    $("#tz").value = draft.timezone;
    renderVars();
    markDirty();
    schedulePreview();
  }

  function schedulePreview() {
    clearTimeout(previewTimer);
    previewTimer = setTimeout(preview, 250);
  }

  async function preview() {
    const seq = ++previewSeq;
    try {
      const p = await api("preview", { settings: draft, kind });
      if (gone || seq !== previewSeq) return;
      $("#p-error").hidden = !p.error;
      $("#p-error").textContent = p.error || "";
      $("#p-title").textContent = $("#p-title-e").textContent = p.title || "";
      $("#p-body").textContent = $("#p-body-e").textContent = p.body || "";
      $("#p-syn").textContent = p.synopsis || "";
      $("#p-syn").hidden = !p.synopsis;
      $("#p-thumb-c").hidden = $("#p-thumb-e").hidden = draft.image !== "poster";
      $("#p-hero").hidden = draft.image !== "backdrop";
    } catch (e) {
      if (!gone) { $("#p-error").hidden = false; $("#p-error").textContent = e.message; }
    }
  }

  $$("[role=tab]").forEach((tab) => tab.addEventListener("click", () => {
    $$("[role=tab]").forEach((t) => t.setAttribute("aria-selected", String(t === tab)));
    $$("[data-panel]").forEach((p) => { p.hidden = p.dataset.panel !== tab.dataset.tab; });
  }));
  $$("[data-kind]").forEach((b) => b.addEventListener("click", () => { kind = b.dataset.kind; fill(); }));
  $$("[data-image]").forEach((b) => b.addEventListener("click", () => { draft.image = b.dataset.image; fill(); }));

  const bind = (sel, key, read) => $(sel).addEventListener("input", () => { draft[typeof key === "function" ? key() : key] = read($(sel)); markDirty(); schedulePreview(); });
  bind("#t-title", () => kind + "_title", (el) => el.value);
  bind("#t-body", () => kind + "_body", (el) => el.value);
  bind("#synopsis", "synopsis", (el) => el.checked);
  bind("#synopsis-max", "synopsis_max", (el) => Number(el.value) || 0);
  bind("#batch", "batch_minutes", (el) => Math.max(0, Number(el.value) || 0));
  bind("#quiet", "quiet_enabled", (el) => el.checked);
  bind("#quiet-start", "quiet_start", (el) => el.value);
  bind("#quiet-end", "quiet_end", (el) => el.value);
  bind("#tz", "timezone", (el) => el.value.trim());
  ["#synopsis", "#quiet"].forEach((s) => $(s).addEventListener("change", () => $(s).dispatchEvent(new Event("input"))));

  let lastField = $("#t-body");
  ["#t-title", "#t-body"].forEach((s) => $(s).addEventListener("focus", () => { lastField = $(s); }));
  $("#vars").addEventListener("click", (e) => {
    const code = e.target.closest("[data-var]");
    if (!code) return;
    const v = code.dataset.var;
    const text = v.startsWith("#") ? `{{${v}}}{{code}} {{/${v.slice(1)}}}` : `{{${v}}}`;
    const el = lastField;
    const at = el.selectionStart ?? el.value.length;
    el.value = el.value.slice(0, at) + text + el.value.slice(el.selectionEnd ?? at);
    el.focus();
    el.selectionStart = el.selectionEnd = at + text.length;
    el.dispatchEvent(new Event("input"));
  });

  $("#reset-kind").addEventListener("click", () => {
    draft[kind + "_title"] = defaults[kind + "_title"];
    draft[kind + "_body"] = defaults[kind + "_body"];
    fill();
  });

  $("#save").addEventListener("click", async () => {
    $("#save").disabled = true;
    try {
      const out = await api("save", draft);
      saved = out.settings;
      draft = structuredClone(saved);
      $("#problems").innerHTML = "";
      fill();
      toast("Saved.");
    } catch (e) {
      $("#problems").innerHTML = `<div class="banner bad">${esc(e.message)}</div>`;
      markDirty();
    }
  });

  $("#test").addEventListener("click", async () => {
    $("#test").disabled = true;
    $("#test-result").textContent = dirty() ? "Sending with the saved settings; save first to test your edits…" : "Sending…";
    try {
      const r = await api("test");
      $("#test-result").textContent = r.devices === 0
        ? "Your profile has no Android phone registered for push."
        : `Sent to ${r.sent} of ${r.devices} phone${r.devices === 1 ? "" : "s"}.`;
    } catch (e) {
      $("#test-result").textContent = "";
      toast(e.message, true);
    } finally {
      $("#test").disabled = false;
    }
  });

  api("load").then((out) => {
    if (gone) return;
    saved = out.settings;
    defaults = out.defaults;
    draft = structuredClone(saved);
    fill();
  }).catch((e) => { $("#problems").innerHTML = `<div class="banner bad">Couldn't load: ${esc(e.message)}</div>`; });

  return () => {
    gone = true;
    clearTimeout(previewTimer);
  };
}
