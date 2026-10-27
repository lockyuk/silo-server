// The push notifications admin page, in the email alerts page's style and
// mounted the same way, inside a shadow root on Admin → Notifications.
export function mountPushStylePage(root, api) {
  const $ = (s) => root.querySelector(s);
  const $$ = (s) => [...root.querySelectorAll(s)];
  const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

  // Templates may use <b>, <i>, <u>, <s> and <font color>, as the phone
  // shows them; anything else is shown as text.
  function formatted(html) {
    const tpl = document.createElement("template");
    tpl.innerHTML = html || "";
    const out = document.createDocumentFragment();
    const walk = (from, into) => {
      for (const node of from.childNodes) {
        if (node.nodeType === Node.TEXT_NODE) { into.appendChild(document.createTextNode(node.textContent)); continue; }
        if (node.nodeType !== Node.ELEMENT_NODE) continue;
        const tag = node.tagName.toLowerCase();
        let el = null;
        if (["b", "strong", "i", "em", "u", "s", "strike"].includes(tag)) el = document.createElement(tag);
        if (tag === "font") {
          el = document.createElement("span");
          const color = node.getAttribute("color") || "";
          if (/^#[0-9a-f]{3,8}$|^[a-z]+$/i.test(color)) el.style.color = color;
        }
        if (tag === "br") { into.appendChild(document.createTextNode(" ")); continue; }
        walk(node, el ? into.appendChild(el) : into);
      }
    };
    walk(tpl.content, out);
    return out;
  }
  const show = (sel, html) => { $(sel).replaceChildren(formatted(html)); };

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
    $("#collapsed-image").checked = draft.collapsed_image;
    $("#expanded-image").checked = draft.expanded_image;
    $("#expanded").checked = draft.expanded;
    $("#expanded-image").disabled = !draft.expanded;
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
      show("#p-title", p.title);
      show("#p-title-e", p.title);
      show("#p-body", p.body);
      show("#p-body-e", p.body);
      $("#p-syn").textContent = p.synopsis || "";
      $("#p-syn").hidden = !p.synopsis;
      // A backdrop shows as a thumbnail when collapsed, as Android does.
      $("#p-thumb-c").hidden = draft.image === "none" || !draft.collapsed_image;
      $("#p-thumb-e").hidden = draft.image !== "poster" || !draft.expanded_image;
      $("#p-hero").hidden = draft.image !== "backdrop" || !draft.expanded_image;
      $("#note-expanded").hidden = $("#label-expanded").hidden = !draft.expanded;
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
  bind("#collapsed-image", "collapsed_image", (el) => el.checked);
  bind("#expanded-image", "expanded_image", (el) => el.checked);
  bind("#expanded", "expanded", (el) => el.checked);
  $("#expanded").addEventListener("input", () => { $("#expanded-image").disabled = !draft.expanded; });
  bind("#synopsis-max", "synopsis_max", (el) => Number(el.value) || 0);
  bind("#batch", "batch_minutes", (el) => Math.max(0, Number(el.value) || 0));
  bind("#quiet", "quiet_enabled", (el) => el.checked);
  bind("#quiet-start", "quiet_start", (el) => el.value);
  bind("#quiet-end", "quiet_end", (el) => el.value);
  bind("#tz", "timezone", (el) => el.value.trim());
  ["#synopsis", "#quiet", "#collapsed-image", "#expanded-image", "#expanded"].forEach((s) => $(s).addEventListener("change", () => $(s).dispatchEvent(new Event("input"))));

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

  $$("[data-wrap]").forEach((b) => b.addEventListener("click", () => {
    const el = lastField;
    const tag = b.dataset.wrap;
    const open = tag === "font" ? `<font color="${$("#fmt-color").value}">` : `<${tag}>`;
    const close = `</${tag}>`;
    const from = el.selectionStart ?? el.value.length;
    const to = el.selectionEnd ?? from;
    el.value = el.value.slice(0, from) + open + el.value.slice(from, to) + close + el.value.slice(to);
    el.focus();
    el.selectionStart = from + open.length;
    el.selectionEnd = to + open.length;
    el.dispatchEvent(new Event("input"));
  }));

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
