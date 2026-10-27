import { useEffect, useRef } from "react";

import { v2 } from "@/api/v2/request";
import { useCurrentProfile } from "@/hooks/useCurrentProfile";

import emailCSS from "./emailAlertsPage/page.css?raw";
import pageCSS from "./pushStylePage/page.css?raw";
import pageHTML from "./pushStylePage/page.html?raw";
import { mountPushStylePage, type PushStylePageAPI } from "./pushStylePage/page.js";

type Settings = Parameters<typeof saveSettings>[0];

function saveSettings(body: Awaited<ReturnType<typeof loadSettings>>["settings"]) {
  return v2("PUT /api/v2/admin/notifications/push-style", { body });
}

function loadSettings() {
  return v2("GET /api/v2/admin/notifications/push-style");
}

/**
 * The push notifications admin page: templates with a live preview, image
 * and synopsis, batching and quiet hours. Styled like the email alerts page
 * and mounted in a shadow root the same way.
 */
export default function PushStylePanel() {
  const hostRef = useRef<HTMLDivElement>(null);
  const { profile } = useCurrentProfile();
  const profileRef = useRef("");
  useEffect(() => {
    profileRef.current = profile?.id ?? "";
  }, [profile?.id]);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    // Silo has one dark theme.
    host.dataset.theme = "dark";
    const root = host.shadowRoot ?? host.attachShadow({ mode: "open" });
    root.innerHTML = `<style>${emailCSS}\n${pageCSS}</style>${pageHTML}`;

    const api: PushStylePageAPI = async (path, body) => {
      switch (path) {
        case "load":
          return loadSettings();
        case "save":
          return saveSettings(body as Settings);
        case "preview": {
          const b = body as { settings: Settings; kind: "episode" | "movie" | "group" };
          return v2("POST /api/v2/admin/notifications/push-style/preview", { body: b });
        }
        case "test":
          return v2("POST /api/v2/admin/notifications/push-style/test", {
            body: { profile_id: profileRef.current },
            retryAuthentication: false,
          });
      }
      throw new Error(`Unknown request ${path}`);
    };

    const unmount = mountPushStylePage(root, api);
    return () => {
      unmount();
      root.innerHTML = "";
    };
  }, []);

  return <div ref={hostRef} className="overflow-hidden rounded-xl" />;
}
