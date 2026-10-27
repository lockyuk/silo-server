import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { setAccessToken, setProfileId, setProfileToken } from "@/api/client";

import { ThemeSongsSettings } from "./ThemeSongsSettings";

const runTask = vi.fn();
vi.mock("@/components/realtimeEventsContext", () => ({ useEventChannel: () => {} }));
vi.mock("@/hooks/queries/admin/tasks", () => ({
  useTasks: () => ({ data: [{ key: "download_theme_songs", state: "idle" }] }),
  useRunTask: () => ({ mutate: runTask, isPending: false }),
}));

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  setAccessToken("synthetic");
  setProfileId("profile-a");
  setProfileToken("pin-a");
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  runTask.mockReset();
});

function setup() {
  let overview = {
    config: { enabled: false, recheck_days: 14 },
    counts: { folder: 1, downloaded: 1, not_available: 1, failed: 0, pending: 0 },
    series: [
      { id: "s-folder", title: "Folder Show", tvdb_id: "1", state: "folder" },
      { id: "s-down", title: "Downloaded Show", tvdb_id: "2", state: "downloaded" },
      { id: "s-miss", title: "Missing Show", tvdb_id: "3", state: "not_available" },
    ],
  };
  const calls: { method: string; path: string; body?: unknown }[] = [];
  const reply = (body: unknown) =>
    new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
  vi.stubGlobal(
    "fetch",
    vi.fn((url: unknown, init: RequestInit) => {
      const path = new URL(String(url), "http://silo.test").pathname;
      const method = init.method ?? "GET";
      const body = init.body ? (JSON.parse(String(init.body)) as unknown) : undefined;
      calls.push({ method, path, body });
      if (method === "PUT") {
        overview = { ...overview, config: body as typeof overview.config };
        return Promise.resolve(reply(overview));
      }
      if (path.endsWith("/retry")) {
        return Promise.resolve(
          reply({ id: "s-miss", title: "Missing Show", tvdb_id: "3", state: "downloaded" }),
        );
      }
      return Promise.resolve(reply(overview));
    }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <ThemeSongsSettings />
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { calls };
}

it("shows the counts and saves the switch", async () => {
  const { calls } = setup();
  expect(await screen.findByText(/1 in folders · 1 downloaded · 1 not available/)).toBeTruthy();
  // Run now waits for downloads to be switched on.
  expect(screen.getByRole("button", { name: /Run now/ }).hasAttribute("disabled")).toBe(true);
  fireEvent.click(screen.getByLabelText("Download TV theme songs"));
  await waitFor(() =>
    expect(calls).toContainEqual({
      method: "PUT",
      path: "/api/v2/admin/theme-songs/settings",
      body: { enabled: true, recheck_days: 14 },
    }),
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: /Run now/ }).hasAttribute("disabled")).toBe(false),
  );
  fireEvent.click(screen.getByRole("button", { name: /Run now/ }));
  expect(runTask).toHaveBeenCalledWith("download_theme_songs", expect.anything());
});

it("filters series and retries one", async () => {
  const { calls } = setup();
  fireEvent.click(await screen.findByRole("button", { name: "Show series" }));
  fireEvent.click(screen.getByRole("button", { name: "Not available" }));
  expect(screen.queryByText("Downloaded Show")).toBeNull();
  // A folder theme has no actions; the missing one can be fetched now.
  fireEvent.click(screen.getByRole("button", { name: "Download the theme for Missing Show now" }));
  await waitFor(() =>
    expect(calls).toContainEqual({
      method: "POST",
      path: "/api/v2/admin/theme-songs/series/s-miss/retry",
      body: undefined,
    }),
  );
});
