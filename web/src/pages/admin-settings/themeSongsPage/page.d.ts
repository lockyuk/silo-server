export type ThemeSongsPageAPI = (
  path: string,
  opts?: { method?: string; body?: unknown },
) => Promise<unknown>;

/** Mounts the theme songs page into root; returns the unmount cleanup. */
export function mountThemeSongsPage(root: ShadowRoot, api: ThemeSongsPageAPI): () => void;
