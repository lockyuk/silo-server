import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import Notifications from "./Notifications";
import { stubRadixDom } from "./adminPage.test-support";
const state = vi.hoisted(() => ({
  list: {
    data: undefined as unknown,
    isLoading: false,
    isError: false,
    hasNextPage: false,
    isFetchingNextPage: false,
    fetchNextPage: vi.fn(),
    restart: vi.fn(),
  },
  listArgs: [] as unknown[][],
  all: { mutateAsync: vi.fn(), isPending: false, isError: false, reset: vi.fn() },
}));
vi.mock("@/hooks/useAuth", () => ({ useAuth: () => ({}) }));
vi.mock("@/api/v2/notifications", () => ({ notificationScope: () => "owner" }));
vi.mock("@/hooks/useDocumentTitle", () => ({ useDocumentTitle: () => {} }));
vi.mock("@/hooks/queries/notifications", async (importOriginal) => ({
  formatEpisodeCode: (await importOriginal<typeof import("@/hooks/queries/notifications")>())
    .formatEpisodeCode,
  useNotifications: (...args: unknown[]) => {
    state.listArgs.push(args);
    return state.list;
  },
  useUnreadNotificationCount: () => ({ data: 2 }),
  useMarkAllNotificationsRead: () => state.all,
  useMarkNotificationRead: () => ({ mutate: vi.fn() }),
}));
vi.mock("@/hooks/queries/libraries", () => ({
  useAvailableUserLibraries: () => ({
    data: [
      { id: 3, name: "Films", type: "movies" },
      { id: 4, name: "TV Shows", type: "series" },
      { id: 5, name: "Books", type: "audiobooks" },
    ],
  }),
}));
const row = {
  id: "one",
  type: "episode.available",
  series_title: "Displayed episode",
  created_at: "2026-01-01T00:00:00Z",
  reason_flags: {},
  read_at: null,
};
function renderAt(path = "/") {
  render(
    <MemoryRouter initialEntries={[path]}>
      <Notifications />
    </MemoryRouter>,
  );
}
/** Lists one notification in an inbox whose read cutoff is "c". */
function showOnly(notification: Record<string, unknown>) {
  state.list.data = { pages: [{ notifications: [notification], read_cutoff: "c" }] };
}
beforeEach(() => {
  state.list.data = { pages: [{ notifications: [row], read_cutoff: "frozen-cutoff" }] };
  state.list.isError = false;
  state.all.isError = false;
  state.all.isPending = false;
  state.all.mutateAsync.mockReset();
  state.all.reset.mockReset();
  state.list.restart.mockReset().mockResolvedValue(undefined);
  state.list.hasNextPage = false;
  state.list.fetchNextPage.mockReset();
});
afterEach(cleanup);
it("submits the displayed cutoff once while the mutation remains pending", async () => {
  let finish!: () => void;
  state.all.mutateAsync.mockReturnValue(
    new Promise<void>((resolve) => {
      finish = resolve;
    }),
  );
  renderAt();
  const button = screen.getByRole("button", { name: "Mark all read" });
  fireEvent.click(button);
  fireEvent.click(button);
  expect(state.all.mutateAsync).toHaveBeenCalledExactlyOnceWith("frozen-cutoff");
  expect(screen.getByText("Displayed episode")).toBeInTheDocument();
  finish();
});
it("retains partial rows and requires explicit reload after a mark-all failure", async () => {
  state.list.isError = true;
  state.all.isError = true;
  renderAt();
  expect(screen.getByText("Displayed episode")).toBeInTheDocument();
  expect(screen.queryByText("No notifications yet")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Mark all read" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Reload inbox" }));
  await waitFor(() => expect(state.all.reset).toHaveBeenCalledOnce());
  expect(state.list.restart).toHaveBeenCalledOnce();
  expect(state.all.mutateAsync).not.toHaveBeenCalled();
});
it.each([
  [
    "links a request.approved row to the title's page",
    {
      type: "request.approved",
      reason_flags: { media_type: "movie", tmdb_id: 603, title: "The Matrix" },
    },
    "/title/movie/603",
  ],
  [
    "keeps a fulfilled request linked to its catalog item",
    {
      type: "request.fulfilled",
      series_id: "movie-603",
      series_title: "The Matrix",
      reason_flags: { media_type: "movie", tmdb_id: 603 },
    },
    "/item/movie-603",
  ],
])("%s", (_title, notification, href) => {
  showOnly({ ...row, ...notification });
  renderAt();
  expect(screen.getByRole("link", { name: /The Matrix/ })).toHaveAttribute("href", href);
});
it("leaves a request row unlinked when its payload lacks the TMDB id", () => {
  showOnly({
    ...row,
    type: "request.approved",
    reason_flags: { title: "No Id", media_type: "movie" },
  });
  renderAt();
  expect(screen.getByText("No Id")).toBeInTheDocument();
  expect(screen.queryByRole("link", { name: /No Id/ })).not.toBeInTheDocument();
});
it("does not describe failed initial loading as an empty inbox or permit an unbounded read-all", () => {
  state.list.data = undefined;
  state.list.isError = true;
  renderAt();
  expect(screen.queryByText("No notifications yet")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Mark all read" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Reload notifications" })).toBeInTheDocument();
});

it("shows how each notification was sent", () => {
  showOnly({ ...row, sent_via: ["email_instant", "email_daily", "push"] });
  renderAt();
  const sent = screen.getByLabelText("Sent as");
  expect(sent).toHaveTextContent("Instant email");
  expect(sent).toHaveTextContent("Daily digest");
  expect(sent).toHaveTextContent("Push");
  expect(sent).not.toHaveTextContent("Weekly digest");
});
it("has no Series view or Preferences", () => {
  renderAt("/notifications?view=series");
  expect(screen.queryByRole("button", { name: "Series" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Preferences/ })).not.toBeInTheDocument();
  expect(screen.getByText("Displayed episode")).toBeInTheDocument();
});
it("shows one page at a time and reads the next page on demand", async () => {
  const second = { ...row, id: "two", series_title: "Second page" };
  state.list.hasNextPage = true;
  state.list.data = { pages: [{ notifications: [row], read_cutoff: "c", total: 25 }] };
  state.list.fetchNextPage.mockImplementation(async () => {
    state.list.data = {
      pages: [
        { notifications: [row], read_cutoff: "c", total: 25 },
        { notifications: [second], read_cutoff: "c", total: 25 },
      ],
    };
    return { isError: false, data: state.list.data, hasNextPage: false };
  });
  renderAt();
  expect(screen.getByRole("button", { name: "Page 2" })).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: /next page/i }));
  await waitFor(() => expect(screen.getByText("Second page")).toBeInTheDocument());
  expect(screen.queryByText("Displayed episode")).not.toBeInTheDocument();
  expect(state.list.fetchNextPage).toHaveBeenCalledOnce();
});

it("filters by library and age, and says when nothing matches", async () => {
  stubRadixDom();
  const user = userEvent.setup();
  state.listArgs = [];
  renderAt();
  expect(state.listArgs.at(-1)).toEqual(["all", 20, { libraryId: undefined, maxAgeDays: 0 }]);

  await user.click(screen.getByRole("combobox", { name: "Library" }));
  expect(screen.queryByRole("option", { name: "Books" })).toBeNull();
  await user.click(screen.getByRole("option", { name: "Films" }));
  await user.click(screen.getByRole("combobox", { name: "Age" }));
  await user.click(screen.getByRole("option", { name: "Last 7 days" }));
  expect(state.listArgs.at(-1)).toEqual(["all", 20, { libraryId: "3", maxAgeDays: 7 }]);

  state.list.data = { pages: [{ notifications: [], read_cutoff: "c", total: 0 }] };
  await user.click(screen.getByRole("button", { name: /^Unread/ }));
  expect(screen.getByText("No notifications match these filters")).toBeInTheDocument();
  expect(state.listArgs.at(-1)).toEqual(["unread", 20, { libraryId: "3", maxAgeDays: 7 }]);
});
