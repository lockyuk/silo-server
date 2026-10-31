import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";

import type { SectionItem } from "@/api/types";

vi.mock("@/hooks/useAmbientColor", () => ({ useAmbientColor: () => undefined }));
vi.mock("@/hooks/useEpisodeSpoilerPrefs", () => ({
  useEpisodeSpoilerPrefs: () => ({ hideOverviews: false, hideImages: false }),
}));
vi.mock("@/components/ViewTransitionLink", async () => {
  const { Link } = await import("react-router");
  return { default: Link };
});
vi.mock("@/components/HeroBanner", () => ({
  HeroBackdropSlide: () => <div data-kind="backdrop" />,
}));
vi.mock("./HeroOverview", () => ({
  default: ({ item }: { item?: SectionItem }) => (item ? <p>{item.overview}</p> : null),
}));

import HomeHero from "./HomeHero";

function item(over: Partial<SectionItem>): SectionItem {
  return {
    content_id: "c1",
    type: "episode",
    title: "The Rabbit Hole",
    series_title: "11.22.63",
    season_number: 1,
    episode_number: 1,
    year: 2016,
    runtime: 45,
    genres: [],
    status: "matched",
    rating_imdb: null,
    overview: "A teacher travels back in time.",
    poster_url: "",
    poster_thumbhash: "",
    backdrop_url: "/b.jpg",
    backdrop_thumbhash: "",
    logo_url: "/logo.png",
    position_seconds: 1200,
    duration_seconds: 2700,
    overlay_summary: { resolution: "1080p", audio: "Dolby Digital+", audio_channels: "5.1" },
    ...over,
  };
}

const slides = [
  item({}),
  item({
    content_id: "c2",
    title: "Pilot",
    series_title: "Animal Kingdom",
    season_number: 1,
    episode_number: 2,
  }),
];

function renderHero(items = slides) {
  return render(
    <MemoryRouter>
      <HomeHero items={items} />
    </MemoryRouter>,
  );
}

describe("HomeHero", () => {
  it("shows the TV hero's details for the current slide", () => {
    renderHero();
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("The Rabbit Hole");
    expect(screen.getByText("Season 1 · Episode 1")).toBeInTheDocument();
    expect(screen.getByText("45 min")).toBeInTheDocument();
    expect(screen.getByText("1080p")).toBeInTheDocument();
    expect(screen.getByText("Dolby Digital+ 5.1")).toBeInTheDocument();
    expect(screen.getByText(/left · ends/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Resume" })).toHaveAttribute(
      "href",
      expect.stringContaining("c1"),
    );
    expect(screen.getByAltText("11.22.63")).toBeInTheDocument();
  });

  it("says Play when nothing has been watched", () => {
    renderHero([item({ position_seconds: 0 })]);
    expect(screen.getByRole("link", { name: "Play" })).toBeInTheDocument();
    expect(screen.queryByText(/left · ends/)).not.toBeInTheDocument();
  });

  it("moves between slides with the arrows and the dots", async () => {
    renderHero();
    await userEvent.click(screen.getByRole("button", { name: "Next slide" }));
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Pilot");
    await userEvent.click(screen.getByRole("button", { name: "Previous slide" }));
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("The Rabbit Hole");
    const dots = within(screen.getByRole("tablist", { name: "Slides" })).getAllByRole("tab");
    expect(dots).toHaveLength(2);
    await userEvent.click(dots[1]!);
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Pilot");
  });

  it("shows no arrows or dots for a single slide", () => {
    renderHero([item({})]);
    expect(screen.queryByRole("button", { name: "Next slide" })).not.toBeInTheDocument();
    expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
  });
});
