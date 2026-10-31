import { lazy, Suspense, useCallback, useEffect, useMemo, useState, type MouseEvent } from "react";
import { Link } from "react-router";
import { ChevronLeft, ChevronRight, Play } from "lucide-react";

import type { SectionItem } from "@/api/types";
import { primaryCardRating } from "@/components/ratings/ratings";
import { useAmbientColor } from "@/hooks/useAmbientColor";
import { useEpisodeSpoilerPrefs } from "@/hooks/useEpisodeSpoilerPrefs";
import { isEpisodeStill, isEpisodeUnwatched } from "@/lib/episodeSpoilers";
import { buildItemHref, buildMediaPlayHref } from "@/lib/mediaNavigation";
import { cn } from "@/lib/utils";
import { markPlaybackIntent } from "@/player/first-frame";
import { parseWatchHref } from "@/pages/watchRouteHelpers";
import ViewTransitionLink from "@/components/ViewTransitionLink";
import { HeroBackdropSlide } from "./HeroBanner";
import { HeroOverviewText } from "./HeroOverviewText";

const HeroOverview = lazy(() => import("./HeroOverview"));

/** How long each slide stays, and the dot's fill timer runs, in milliseconds. */
const DWELL_MS = 8000;

interface HomeHeroProps {
  items: SectionItem[];
  maxSlides?: number;
  libraryId?: number;
  onRefreshItems?: () => void;
}

function formatRuntime(minutes: number | undefined): string | null {
  if (!minutes || minutes <= 0) return null;
  if (minutes < 60) return `${Math.round(minutes)} min`;
  const h = Math.floor(minutes / 60);
  const m = Math.round(minutes % 60);
  return m === 0 ? `${h}h` : `${h}h ${m}m`;
}

/** "28m left · ends 20:16" for something part-watched, else null. */
function timeLeft(item: SectionItem, now: Date): { fraction: number; label: string } | null {
  const position = item.position_seconds ?? 0;
  const duration = item.duration_seconds ?? 0;
  if (position <= 0 || duration <= 0 || position >= duration) return null;
  const leftSeconds = duration - position;
  const minutes = Math.max(1, Math.round(leftSeconds / 60));
  const left = minutes >= 60 ? `${Math.floor(minutes / 60)}h ${minutes % 60}m` : `${minutes}m`;
  const ends = new Date(now.getTime() + leftSeconds * 1000);
  const clock = ends.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
  return { fraction: Math.min(1, position / duration), label: `${left} left · ends ${clock}` };
}

/** The eyebrow over the title: season and episode, or year and genres. */
function eyebrow(item: SectionItem): string {
  if (
    item.type === "episode" &&
    Number.isInteger(item.season_number) &&
    Number.isInteger(item.episode_number)
  ) {
    return `Season ${item.season_number} · Episode ${item.episode_number}`;
  }
  const genres = [...new Set((item.genres ?? []).map((g) => g.trim()).filter(Boolean))].slice(0, 2);
  return [item.year > 0 ? String(item.year) : null, ...genres].filter(Boolean).join(" · ");
}

/** Resolution, HDR and audio as the outlined chips the TV shows after the runtime. */
function qualityBadges(item: SectionItem): string[] {
  const o = item.overlay_summary;
  if (!o) return [];
  const audio = [o.audio, o.audio_channels].filter(Boolean).join(" ");
  return [o.resolution, o.hdr, audio].filter((v): v is string => Boolean(v && v.trim()));
}

/**
 * Home's featured hero, laid out like the TV app's Continue Watching hero: the
 * art runs full-bleed and crossfades (no zoom), the series logo sits top-left,
 * and the details are anchored at the bottom: an eyebrow with the accent tick,
 * the title, runtime, rating and quality chips, the synopsis on a thin rule, a
 * progress bar with the time left, then back and next arrows around a Resume
 * pill, with a dot per slide whose current dot fills as the timer runs.
 */
export default function HomeHero({
  items,
  maxSlides = 10,
  libraryId,
  onRefreshItems,
}: HomeHeroProps) {
  const spoilerPrefs = useEpisodeSpoilerPrefs();
  const slides = useMemo(() => items.slice(0, maxSlides), [items, maxSlides]);
  const pendingSlideIds = useMemo(
    () => new Set(slides.filter((s) => s.pending_translation_language).map((s) => s.content_id)),
    [slides],
  );
  const [{ active, outgoing }, setState] = useState({ active: 0, outgoing: null as number | null });
  const [paused, setPaused] = useState(false);
  const [cycle, setCycle] = useState(0);
  const [now] = useState(() => new Date());

  const go = useCallback(
    (delta: number) => {
      setState(({ active: current }) => ({
        active: (current + delta + slides.length) % slides.length,
        outgoing: current,
      }));
      setCycle((c) => c + 1);
    },
    [slides.length],
  );
  const goTo = useCallback((index: number) => {
    setState(({ active: current }) =>
      index === current
        ? { active: current, outgoing: null }
        : { active: index, outgoing: current },
    );
    setCycle((c) => c + 1);
  }, []);

  useEffect(() => {
    if (outgoing === null) return;
    const timer = window.setTimeout(
      () => setState((s) => (s.outgoing === outgoing ? { ...s, outgoing: null } : s)),
      1600,
    );
    return () => window.clearTimeout(timer);
  }, [outgoing]);

  useEffect(() => {
    if (slides.length <= 1 || paused) return;
    if (window.matchMedia?.("(prefers-reduced-motion: reduce)")?.matches) return;
    const timer = window.setTimeout(() => go(1), DWELL_MS);
    return () => window.clearTimeout(timer);
  }, [slides.length, paused, active, cycle, go]);

  const current = slides[active] ?? slides[0];
  useAmbientColor(current?.backdrop_thumbhash);
  if (!current) return null;

  const hideOverview =
    spoilerPrefs.hideOverviews &&
    current.type === "episode" &&
    isEpisodeUnwatched({
      played: current.user_state?.played,
      position_seconds: current.position_seconds,
    });
  const overviewShown = Boolean(current.overview) && !hideOverview;
  const left = timeLeft(current, now);
  const playHref = buildMediaPlayHref({
    contentId: current.content_id,
    type: current.type,
    libraryId,
  });
  const resumes = (current.position_seconds ?? 0) > 0;
  const rating = current.type === "movie" ? primaryCardRating(current) : null;
  const facts = [formatRuntime(current.runtime)].filter(Boolean) as string[];
  const badges = qualityBadges(current);
  const logoSrc = current.logo_url || null;

  const onPlay = (event: MouseEvent<HTMLAnchorElement>) => {
    const opensHere =
      event.button === 0 && !event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey;
    const request = opensHere ? parseWatchHref(playHref) : null;
    if (request) markPlaybackIntent(request.requestKey);
  };

  return (
    <section
      aria-roledescription="carousel"
      aria-label="Continue watching"
      className="home-hero group relative h-[calc(100dvh-7rem)] min-h-[34rem] w-full overflow-hidden"
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
      onFocus={() => setPaused(true)}
      onBlur={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget)) setPaused(false);
      }}
    >
      {slides.map((slide, i) => (
        <HeroBackdropSlide
          key={slide.content_id ?? i}
          slide={slide}
          index={i}
          isActive={i === active}
          keepsMotion={false}
          hideImage={
            spoilerPrefs.hideImages &&
            slide.type === "episode" &&
            isEpisodeStill(slide.backdrop_is_episode_still) &&
            isEpisodeUnwatched({
              played: slide.user_state?.played,
              position_seconds: slide.position_seconds,
            })
          }
          shouldLoad={
            i === active ||
            i === outgoing ||
            i === (active + 1) % slides.length ||
            i === (active - 1 + slides.length) % slides.length
          }
        />
      ))}

      {/* Scrims: dark under the menu bar, to the left for the copy, and up from the bottom. */}
      <div className="pointer-events-none absolute inset-x-0 top-0 h-44 bg-gradient-to-b from-black/70 to-transparent" />
      <div className="pointer-events-none absolute inset-0 bg-gradient-to-r from-black/70 via-black/25 to-transparent" />
      <div className="pointer-events-none absolute inset-x-0 bottom-0 h-3/5 bg-gradient-to-t from-black/85 via-black/40 to-transparent" />

      {/* Logo, top-left under the bar. */}
      <div
        key={`logo-${current.content_id}`}
        className="home-hero-enter absolute top-28 left-10 z-10 xl:left-14"
      >
        {logoSrc ? (
          <img
            src={logoSrc}
            alt={current.series_title || current.title}
            className="max-h-28 w-auto max-w-[26rem] object-contain object-left drop-shadow-[0_4px_18px_rgba(0,0,0,0.6)]"
          />
        ) : (
          <p className="font-display max-w-xl text-4xl font-extrabold tracking-tight drop-shadow-[0_4px_18px_rgba(0,0,0,0.6)]">
            {current.series_title || current.title}
          </p>
        )}
      </div>

      <div className="absolute inset-x-0 bottom-0 z-10 px-10 pb-8 xl:px-14">
        <div key={`copy-${current.content_id}`} className="home-hero-enter max-w-3xl">
          <p className="mb-3 flex items-center gap-3 text-sm font-semibold tracking-[0.22em] text-white/85 uppercase">
            <span
              aria-hidden="true"
              className="h-5 w-1 rounded-full bg-[var(--accent-theme-accent,#e50914)]"
            />
            {eyebrow(current)}
          </p>
          <h1
            className="font-display mb-3 max-w-4xl text-4xl font-extrabold tracking-[-0.03em] text-balance xl:text-5xl"
            style={{ textShadow: "var(--hero-text-shadow, 0 2px 20px rgba(0,0,0,0.5))" }}
          >
            <ViewTransitionLink
              to={buildItemHref({ contentId: current.content_id, libraryId })}
              className="hover:underline focus-visible:underline focus-visible:outline-none"
            >
              {current.title}
            </ViewTransitionLink>
          </h1>
          <div className="mb-4 flex flex-wrap items-center gap-x-3 gap-y-2 text-[0.9375rem] font-medium text-white/85">
            {facts.map((fact) => (
              <span key={fact}>{fact}</span>
            ))}
            {rating && (
              <span className="inline-flex items-center gap-1">
                <span aria-hidden="true">★</span>
                {rating.display}
              </span>
            )}
            {badges.map((badge) => (
              <span
                key={badge}
                className="rounded border border-white/45 px-2 py-0.5 text-xs font-semibold tracking-wide"
              >
                {badge}
              </span>
            ))}
          </div>
          <Suspense
            fallback={
              overviewShown ? (
                <div className="mb-5 max-w-2xl border-l-2 border-white/30 pl-4">
                  <HeroOverviewText overview={current.overview} />
                </div>
              ) : null
            }
          >
            <div
              className={cn(
                "mb-5 max-w-2xl border-l-2 border-white/30 pl-4 [&>div]:mb-0",
                !overviewShown && "hidden",
              )}
            >
              <HeroOverview
                item={overviewShown ? current : undefined}
                pendingContentIds={pendingSlideIds}
                onRefreshItems={onRefreshItems}
              />
            </div>
          </Suspense>
          {left && (
            <div className="mb-5 flex max-w-xl items-center gap-4">
              <div
                className="h-[3px] flex-1 overflow-hidden rounded-full bg-white/25"
                role="progressbar"
                aria-valuenow={Math.round(left.fraction * 100)}
                aria-valuemin={0}
                aria-valuemax={100}
                aria-label="Watched"
              >
                <div
                  className="h-full rounded-full bg-[var(--accent-theme-accent,#e50914)]"
                  style={{ width: `${left.fraction * 100}%` }}
                />
              </div>
              <span className="text-sm font-medium whitespace-nowrap text-white/85">
                {left.label}
              </span>
            </div>
          )}
        </div>

        <div className="flex items-center gap-4">
          {slides.length > 1 && (
            <button
              type="button"
              onClick={() => go(-1)}
              aria-label="Previous slide"
              className="hover:bg-primary hover:text-primary-foreground focus-visible:bg-primary focus-visible:text-primary-foreground flex size-11 items-center justify-center rounded-full bg-[var(--accent-theme-rest,rgb(255_255_255/0.18))] text-white backdrop-blur transition-colors focus-visible:outline-none"
            >
              <ChevronLeft className="size-5" />
            </button>
          )}
          <Link
            to={playHref}
            onClick={onPlay}
            className="bg-primary text-primary-foreground flex h-11 min-w-36 items-center justify-center gap-2 rounded-full px-7 text-base font-semibold transition-[filter] hover:brightness-110 focus-visible:ring-2 focus-visible:ring-white focus-visible:outline-none"
          >
            <Play className="size-4 fill-current" />
            {resumes ? "Resume" : "Play"}
          </Link>
          {slides.length > 1 && (
            <>
              <button
                type="button"
                onClick={() => go(1)}
                aria-label="Next slide"
                className="hover:bg-primary hover:text-primary-foreground focus-visible:bg-primary focus-visible:text-primary-foreground flex size-11 items-center justify-center rounded-full bg-[var(--accent-theme-rest,rgb(255_255_255/0.18))] text-white backdrop-blur transition-colors focus-visible:outline-none"
              >
                <ChevronRight className="size-5" />
              </button>
              <div className="ml-2 flex items-center gap-2" role="tablist" aria-label="Slides">
                {slides.map((slide, i) => (
                  <button
                    key={`${slide.content_id}-${i === active ? cycle : "rest"}`}
                    type="button"
                    role="tab"
                    aria-selected={i === active}
                    aria-label={`Slide ${i + 1} of ${slides.length}`}
                    data-active={i === active ? "true" : "false"}
                    data-paused={paused ? "true" : "false"}
                    onClick={() => goTo(i)}
                    className="home-hero-dot"
                  />
                ))}
              </div>
            </>
          )}
        </div>
      </div>

      <div aria-live="polite" className="sr-only">
        {current.title}
      </div>
    </section>
  );
}
