export type PushStylePageAPI = (path: string, body?: unknown) => Promise<any>;

/** Mounts the push notifications page into root; returns the unmount cleanup. */
export function mountPushStylePage(root: ShadowRoot, api: PushStylePageAPI): () => void;
