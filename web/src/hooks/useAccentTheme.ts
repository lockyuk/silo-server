import { useCallback, useSyncExternalStore } from "react";

import { ACCENT_THEMES, type AccentThemeId } from "@/lib/accentThemes";

/**
 * The accent theme (the TV and phone apps' colour themes) is a per-device
 * choice kept in this browser. The static boot script (themeBoot.js) applies
 * it before first paint; this hook reads, changes and applies it afterwards.
 * "default" removes the attribute so the base theme shows unchanged.
 */
export const ACCENT_THEME_STORAGE_KEY = "spoel-accent-theme";

const listeners = new Set<() => void>();

function isAccent(value: unknown): value is AccentThemeId {
  return ACCENT_THEMES.some((theme) => theme.id === value);
}

export function readAccentTheme(): AccentThemeId {
  try {
    const saved = localStorage.getItem(ACCENT_THEME_STORAGE_KEY);
    return isAccent(saved) ? saved : "default";
  } catch {
    return "default";
  }
}

export function applyAccentTheme(id: AccentThemeId): void {
  const root = document.documentElement;
  if (id === "default") root.removeAttribute("data-accent");
  else root.setAttribute("data-accent", id);
}

export function setAccentTheme(id: AccentThemeId): void {
  try {
    if (id === "default") localStorage.removeItem(ACCENT_THEME_STORAGE_KEY);
    else localStorage.setItem(ACCENT_THEME_STORAGE_KEY, id);
  } catch {
    // Blocked storage: the choice still applies for this page view.
  }
  applyAccentTheme(id);
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  window.addEventListener("storage", listener);
  return () => {
    listeners.delete(listener);
    window.removeEventListener("storage", listener);
  };
}

export function useAccentTheme(): readonly [AccentThemeId, (id: AccentThemeId) => void] {
  const current = useSyncExternalStore(subscribe, readAccentTheme, () => "default" as const);
  const set = useCallback((id: AccentThemeId) => setAccentTheme(id), []);
  return [current, set] as const;
}
