import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  navigate: vi.fn(),
  logout: vi.fn(),
  clearProfile: vi.fn(),
  isAdmin: false,
  requestsEnabled: false,
}));

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({
    user: { username: "pete" },
    logout: mocks.logout,
    clearProfile: mocks.clearProfile,
  }),
}));
vi.mock("@/hooks/useCurrentProfile", () => ({
  useCurrentProfile: () => ({ profile: { id: "p1", name: "Pete", avatar_url: null } }),
}));
vi.mock("@/hooks/useIsActingAdmin", () => ({ useIsActingAdmin: () => mocks.isAdmin }));
vi.mock("@/hooks/queries/libraries", () => ({
  useUserLibraries: () => ({
    data: [
      { id: 1, name: "Movies", type: "movie" },
      { id: 2, name: "TV Shows", type: "series" },
    ],
  }),
}));
vi.mock("@/hooks/useUICustomization", () => ({
  useUICustomization: () => ({ primaryMenu: null }),
}));
vi.mock("@/hooks/queries/useRequests", () => ({
  useRequestFeatureStatus: () => ({ data: { requests_enabled: mocks.requestsEnabled } }),
}));
vi.mock("@/hooks/queries/notificationWebhooks", () => ({
  useNotificationCapability: () => ({ isError: false, data: { in_app: { enabled: true } } }),
}));
vi.mock("@/hooks/queries/notifications", () => ({
  useUnreadNotificationCount: () => ({ data: 3 }),
}));
vi.mock("@/lib/startupPriority", () => ({ useCriticalContentLoaded: () => true }));
vi.mock("@/hooks/useViewTransition", () => ({ useViewTransitionNavigate: () => mocks.navigate }));
vi.mock("@/components/ViewTransitionLink", async () => {
  const { Link } = await import("react-router");
  return { default: Link };
});
vi.mock("@/components/SiloBrand", () => ({ SiloBrand: () => <span>brand</span> }));
vi.mock("@/pages/catalogRoute", () => ({ prefetchCatalog: () => undefined }));

import TopMenuBar from "./TopMenuBar";

function renderBar(path = "/") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <TopMenuBar />
    </MemoryRouter>,
  );
}

describe("TopMenuBar", () => {
  it("shows the tabs and marks the current page", () => {
    renderBar("/library/2");
    const nav = screen.getByRole("navigation", { name: "Primary" });
    for (const label of ["Home", "Movies", "TV Shows", "For You", "Calendar"]) {
      expect(nav).toHaveTextContent(label);
    }
    expect(within(nav).getByRole("link", { name: "TV Shows" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(within(nav).getByRole("link", { name: "Home" })).not.toHaveAttribute("aria-current");
  });

  it("marks Home only on the home page", () => {
    renderBar("/");
    const nav = screen.getByRole("navigation", { name: "Primary" });
    expect(within(nav).getByRole("link", { name: "Home" })).toHaveAttribute("aria-current", "page");
  });

  it("flags unread notifications", () => {
    renderBar();
    expect(screen.getByRole("link", { name: "Notifications, unread" })).toBeInTheDocument();
  });

  it("opens the profile dropdown with the TV's entries", async () => {
    renderBar();
    await userEvent.click(screen.getByRole("button", { name: "Pete menu" }));
    for (const label of [
      "Switch Profile",
      "Watchlist",
      "Favorites",
      "History",
      "Settings",
      "Sign Out",
    ]) {
      expect(await screen.findByRole("menuitem", { name: label })).toBeInTheDocument();
    }
    expect(screen.queryByRole("menuitem", { name: "Admin" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Requests" })).not.toBeInTheDocument();
  });

  it("signs out from the dropdown", async () => {
    renderBar();
    await userEvent.click(screen.getByRole("button", { name: "Pete menu" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Sign Out" }));
    expect(mocks.logout).toHaveBeenCalled();
  });
});
