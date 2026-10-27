import { useEffect, useRef } from "react";

import { v2 } from "@/api/v2/request";

import pageCSS from "./themeSongsPage/page.css?raw";
import pageHTML from "./themeSongsPage/page.html?raw";
import { mountThemeSongsPage, type ThemeSongsPageAPI } from "./themeSongsPage/page.js";

type Overview = Awaited<ReturnType<typeof readOverview>>;
type Series = Overview["series"][number];

function readOverview() {
  return v2("GET /api/v2/admin/theme-songs");
}

// The page was written against the plugin's admin API; these map the
// server's responses onto the shapes it reads.
function pluginStatus(s: Series) {
  switch (s.state) {
    case "folder":
      return "present";
    case "downloaded":
      return "downloaded";
    case "skipped":
      return "skipped";
    case "failed":
      return "error";
    default:
      if (!s.tvdb_id) return "no_tvdb";
      return s.state === "pending" ? "pending" : "missing";
  }
}

function pluginShow(s: Series) {
  return {
    i: s.id,
    n: s.title,
    v: s.tvdb_id,
    s: pluginStatus(s),
    c: s.downloaded_at ?? s.checked_at ?? "",
    d: s.detail ?? "",
  };
}

function pluginState(o: Overview) {
  const shows = o.series.map(pluginShow);
  const counts: Record<string, number> = {};
  for (const s of shows) counts[s.s] = (counts[s.s] ?? 0) + 1;
  const r = o.report;
  return {
    shows,
    counts,
    settings: o.config,
    running: o.running,
    progress: o.progress ?? "",
    report: r
      ? {
          finished: r.finished,
          downloaded: r.downloaded,
          checked: r.checked,
          shows: r.series,
          errors: [...(r.error ? [r.error] : []), ...(r.failures ?? [])],
        }
      : null,
    problems: o.config.enabled
      ? []
      : ["Daily downloads are off. Turn them on under Settings below, or download now."],
  };
}

/**
 * The theme songs plugin's admin page, unchanged in look and behaviour,
 * mounted in a shadow root so its styles neither leak nor inherit.
 */
export default function ThemeSongsPanel() {
  const hostRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    // Silo has one dark theme.
    host.dataset.theme = "dark";
    const root = host.shadowRoot ?? host.attachShadow({ mode: "open" });
    root.innerHTML = `<style>${pageCSS}</style>${pageHTML}`;

    const api: ThemeSongsPageAPI = async (path, opts = {}) => {
      const body = opts.body as Record<string, unknown> | undefined;
      if (path === "state") return pluginState(await readOverview());
      if (path === "sync") {
        return pluginState(
          await v2("POST /api/v2/admin/theme-songs/run", { body: { force: !!body?.force } }),
        );
      }
      if (path === "settings") {
        return pluginState(
          await v2("PUT /api/v2/admin/theme-songs/settings", {
            body: { enabled: !!body?.enabled, recheck_days: Number(body?.recheck_days) },
          }),
        );
      }
      const show = /^shows\/([^/]+)\/(retry|skip)$/.exec(path);
      if (show?.[1]) {
        const id = decodeURIComponent(show[1]);
        const updated =
          show[2] === "retry"
            ? await v2("POST /api/v2/admin/theme-songs/series/{id}/retry", { path: { id } })
            : await v2("POST /api/v2/admin/theme-songs/series/{id}/skip", { path: { id } });
        return pluginShow(updated);
      }
      throw new Error(`Unknown request ${opts.method ?? "GET"} ${path}`);
    };

    const unmount = mountThemeSongsPage(root, api);
    return () => {
      unmount();
      root.innerHTML = "";
    };
  }, []);

  return <div id="theme-songs" ref={hostRef} className="overflow-hidden rounded-xl" />;
}
