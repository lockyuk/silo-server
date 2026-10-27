import { useMemo, useState } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { CheckCircle2, ExternalLink, Film, Plus, Tv } from "lucide-react";

import { getPersonFilmography, roleSummary, type FilmographyCredit } from "@/api/v2/filmography";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { personKeys } from "@/hooks/queries/keys";

type TypeFilter = "all" | "movie" | "series";
type LibraryFilter = "all" | "in" | "out";
type RoleFilter = "all" | "acting" | "crew";

const pill = (active: boolean) =>
  `rounded-md border px-3 py-1.5 text-xs font-medium transition-colors ${
    active
      ? "border-border/20 bg-foreground/10 text-foreground"
      : "border-border/10 text-muted-foreground hover:text-foreground"
  }`;

function isActing(credit: FilmographyCredit) {
  return credit.roles.some((r) => r.department === "Acting");
}

function onlySelf(credit: FilmographyCredit) {
  return credit.roles.every((r) => r.self);
}

/**
 * Everything a person worked on, Plex style: one row per title grouped by
 * year, with library titles linking to their page and the rest opening a
 * details panel that can send them to the request server.
 */
export function PersonFilmography({
  personId,
  fallback,
}: {
  personId: string;
  fallback: React.ReactNode;
}) {
  const { data, isLoading, isError } = useQuery({
    queryKey: personKeys.filmography(personId),
    queryFn: ({ signal }) => getPersonFilmography(personId, signal),
    staleTime: 5 * 60 * 1000,
  });
  const [type, setType] = useState<TypeFilter>("all");
  const [library, setLibrary] = useState<LibraryFilter>("all");
  const [role, setRole] = useState<RoleFilter>("all");
  const [showSelf, setShowSelf] = useState(false);
  const [selected, setSelected] = useState<FilmographyCredit | null>(null);

  const credits = useMemo(() => data?.credits ?? [], [data]);
  const hasSelf = credits.some(onlySelf);
  const hasCrew = credits.some((c) => !isActing(c));
  const visible = useMemo(
    () =>
      credits.filter(
        (c) =>
          (type === "all" || c.item_type === type) &&
          (library === "all" || (library === "in") === !!c.item_id) &&
          (role === "all" || (role === "acting") === isActing(c)) &&
          (showSelf || !onlySelf(c)),
      ),
    [credits, type, library, role, showSelf],
  );
  const groups = useMemo(() => {
    const out: { label: string; credits: FilmographyCredit[] }[] = [];
    for (const credit of visible) {
      const label = credit.year ? String(credit.year) : "Upcoming";
      const last = out.at(-1);
      if (last?.label === label) last.credits.push(credit);
      else out.push({ label, credits: [credit] });
    }
    return out;
  }, [visible]);

  if (isLoading) {
    return (
      <div className="space-y-2">
        {Array.from({ length: 6 }, (_, i) => (
          <Skeleton key={i} className="h-16 w-full" />
        ))}
      </div>
    );
  }
  // Without a provider that lists credits, show the library titles only.
  if (isError || !data?.available) {
    return <>{fallback}</>;
  }

  const owned = credits.filter((c) => c.item_id).length;

  return (
    <div className="space-y-4">
      <p className="text-muted-foreground text-sm">
        {credits.length} title{credits.length === 1 ? "" : "s"} · {owned} in your library
      </p>
      <div className="flex flex-wrap gap-x-4 gap-y-2">
        <div className="flex gap-1.5" role="group" aria-label="Type">
          {(["all", "movie", "series"] as const).map((t) => (
            <button key={t} type="button" className={pill(type === t)} onClick={() => setType(t)}>
              {t === "all" ? "All" : t === "movie" ? "Movies" : "TV"}
            </button>
          ))}
        </div>
        <div className="flex gap-1.5" role="group" aria-label="Library">
          {(
            [
              ["all", "Everything"],
              ["in", "In library"],
              ["out", "Not in library"],
            ] as const
          ).map(([value, label]) => (
            <button
              key={value}
              type="button"
              className={pill(library === value)}
              onClick={() => setLibrary(value)}
            >
              {label}
            </button>
          ))}
        </div>
        {hasCrew && (
          <div className="flex gap-1.5" role="group" aria-label="Role">
            {(
              [
                ["all", "All roles"],
                ["acting", "Acting"],
                ["crew", "Crew"],
              ] as const
            ).map(([value, label]) => (
              <button
                key={value}
                type="button"
                className={pill(role === value)}
                onClick={() => setRole(value)}
              >
                {label}
              </button>
            ))}
          </div>
        )}
        {hasSelf && (
          <button
            type="button"
            className={pill(showSelf)}
            aria-pressed={showSelf}
            onClick={() => setShowSelf((v) => !v)}
          >
            Appearances as themselves
          </button>
        )}
      </div>

      {groups.length === 0 ? (
        <p className="text-muted-foreground py-6 text-sm">Nothing matches these filters.</p>
      ) : (
        <div className="space-y-6">
          {groups.map((group) => (
            <section key={group.label} aria-label={group.label}>
              <h3 className="text-muted-foreground mb-1 text-xs font-semibold tracking-wide uppercase">
                {group.label}
              </h3>
              <ul className="divide-border/10 divide-y">
                {group.credits.map((credit) => (
                  <li key={credit.key}>
                    <CreditRow credit={credit} onOpen={() => setSelected(credit)} />
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </div>
      )}

      <CreditDialog credit={selected} onClose={() => setSelected(null)} />
    </div>
  );
}

function Poster({ credit, className }: { credit: FilmographyCredit; className: string }) {
  const Icon = credit.item_type === "series" ? Tv : Film;
  return (
    <div className={`bg-surface shrink-0 overflow-hidden rounded ${className}`}>
      {credit.poster_url ? (
        <img src={credit.poster_url} alt="" loading="lazy" className="h-full w-full object-cover" />
      ) : (
        <div className="text-muted-foreground flex h-full w-full items-center justify-center">
          <Icon className="h-5 w-5" />
        </div>
      )}
    </div>
  );
}

function CreditRow({ credit, onOpen }: { credit: FilmographyCredit; onOpen: () => void }) {
  const body = (
    <>
      <Poster credit={credit} className="aspect-[2/3] w-10" />
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium">
          {credit.title}
          <span className="text-muted-foreground ml-2 text-xs font-normal">
            {credit.item_type === "series" ? "TV" : "Movie"}
          </span>
        </p>
        <p className="text-muted-foreground truncate text-xs">{roleSummary(credit)}</p>
      </div>
      {credit.item_id ? (
        <span className="text-primary flex shrink-0 items-center gap-1 text-xs font-medium">
          <CheckCircle2 className="h-4 w-4" />
          In library
        </span>
      ) : (
        <span className="text-muted-foreground shrink-0 text-xs">Not in library</span>
      )}
    </>
  );
  const className =
    "hover:bg-foreground/5 focus-visible:bg-foreground/5 flex w-full items-center gap-3 rounded-md px-2 py-2 text-left outline-none";
  return credit.item_id ? (
    <Link to={`/item/${credit.item_id}`} className={className}>
      {body}
    </Link>
  ) : (
    <button type="button" onClick={onOpen} className={className}>
      {body}
    </button>
  );
}

function CreditDialog({
  credit,
  onClose,
}: {
  credit: FilmographyCredit | null;
  onClose: () => void;
}) {
  const tmdb = credit?.provider_ids.tmdb;
  const tmdbURL = tmdb
    ? `https://www.themoviedb.org/${credit?.item_type === "series" ? "tv" : "movie"}/${tmdb}`
    : null;
  return (
    <Dialog open={!!credit} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-lg">
        {credit && (
          <>
            <DialogHeader>
              <DialogTitle>{credit.title}</DialogTitle>
              <DialogDescription>
                {[credit.item_type === "series" ? "TV series" : "Movie", credit.year || "Upcoming"]
                  .filter(Boolean)
                  .join(" · ")}
                {credit.vote_average ? ` · ★ ${credit.vote_average.toFixed(1)}` : ""}
              </DialogDescription>
            </DialogHeader>
            <div className="flex gap-4">
              <Poster credit={credit} className="aspect-[2/3] w-28" />
              <div className="min-w-0 space-y-2 text-sm">
                <p className="font-medium">{roleSummary(credit)}</p>
                {credit.overview ? (
                  <p className="text-muted-foreground max-h-48 overflow-y-auto leading-relaxed">
                    {credit.overview}
                  </p>
                ) : (
                  <p className="text-muted-foreground">No synopsis yet.</p>
                )}
              </div>
            </div>
            <p className="text-muted-foreground text-xs">Not in your library.</p>
            <div className="flex flex-wrap gap-2">
              {credit.request_url && (
                <Button asChild>
                  <a href={credit.request_url} target="_blank" rel="noreferrer">
                    <Plus className="h-4 w-4" />
                    Request in Seerr
                  </a>
                </Button>
              )}
              {tmdbURL && (
                <Button variant="outline" asChild>
                  <a href={tmdbURL} target="_blank" rel="noreferrer">
                    <ExternalLink className="h-4 w-4" />
                    TMDB
                  </a>
                </Button>
              )}
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
