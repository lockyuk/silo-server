import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { setAccessToken, setProfileId, setProfileToken } from "@/api/client";

import { PersonFilmography } from "./PersonFilmography";

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
});

const acting = (character: string, extra: object = {}) => ({
  department: "Acting",
  character,
  ...extra,
});

function setup(body: unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } }),
      ),
    ),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <PersonFilmography personId="7" fallback={<p>library grid</p>} />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

const filmography = {
  person_id: "7",
  available: true,
  credits: [
    {
      key: "movie:tmdb:9",
      item_type: "movie",
      title: "Next Thing",
      roles: [acting("Lead")],
      provider_ids: { tmdb: "9" },
    },
    {
      key: "movie:tmdb:550",
      item_type: "movie",
      title: "Fight Club",
      year: 1999,
      release_date: "1999-10-15",
      roles: [acting("Tyler Durden"), { department: "Production", job: "Producer" }],
      provider_ids: { tmdb: "550" },
      item_id: "movie-550",
    },
    {
      key: "series:tmdb:1399",
      item_type: "series",
      title: "Friends",
      year: 1994,
      release_date: "1994-09-22",
      overview: "Six friends.",
      roles: [acting("Will", { episode_count: 1 })],
      provider_ids: { tmdb: "1399" },
      request_url: "http://seerr.test/tv/1399",
    },
    {
      key: "series:tmdb:2",
      item_type: "series",
      title: "Late Show",
      year: 1994,
      release_date: "1994-01-01",
      roles: [acting("Self", { self: true })],
      provider_ids: { tmdb: "2" },
    },
  ],
};

it("lists every title by year and links library titles", async () => {
  setup(filmography);
  expect(await screen.findByText("4 titles · 1 in your library")).toBeTruthy();
  // Upcoming leads, then years newest first; talk-show appearances start hidden.
  const headings = screen.getAllByRole("heading", { level: 3 }).map((h) => h.textContent);
  expect(headings).toEqual(["Upcoming", "1999", "1994"]);
  expect(screen.queryByText("Late Show")).toBeNull();
  const owned = screen.getByRole("link", { name: /Fight Club/ });
  expect(owned.getAttribute("href")).toBe("/item/movie-550");
  expect(within(owned).getByText("as Tyler Durden · Producer")).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: "Appearances as themselves" }));
  expect(screen.getByText("Late Show")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "In library" }));
  expect(screen.queryByText("Friends")).toBeNull();
  expect(screen.getByText("Fight Club")).toBeTruthy();
});

it("opens a title outside the library with a request link", async () => {
  setup(filmography);
  fireEvent.click(await screen.findByRole("button", { name: /Friends/ }));
  const dialog = await screen.findByRole("dialog");
  expect(within(dialog).getByText("Six friends.")).toBeTruthy();
  expect(within(dialog).getByText("as Will (1 episode)")).toBeTruthy();
  expect(
    within(dialog)
      .getByRole("link", { name: /Request in Seerr/ })
      .getAttribute("href"),
  ).toBe("http://seerr.test/tv/1399");
  expect(within(dialog).getByRole("link", { name: /TMDB/ }).getAttribute("href")).toBe(
    "https://www.themoviedb.org/tv/1399",
  );
});

it("falls back to the library grid without provider credits", async () => {
  setup({ person_id: "7", available: false, credits: [] });
  expect(await screen.findByText("library grid")).toBeTruthy();
});
