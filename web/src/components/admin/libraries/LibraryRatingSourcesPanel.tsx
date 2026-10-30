import { toast } from "sonner";

import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  useAdminRatingSources,
  useLibraryRatingSources,
  useSetLibraryRatingSources,
} from "@/hooks/queries/admin/ratingSources";

/**
 * Which external ratings (IMDb, TMDB, Rotten Tomatoes, MDBList...) this
 * library's titles show. Off, the library follows Settings → Library &
 * Metadata → Ratings. Saved as soon as it changes, like the translate panel.
 */
export function LibraryRatingSourcesPanel({ libraryId }: { libraryId: number }) {
  const sourcesQuery = useAdminRatingSources();
  const choiceQuery = useLibraryRatingSources(libraryId);
  const save = useSetLibraryRatingSources(libraryId);
  const sources = sourcesQuery.data?.items ?? [];
  const choice = choiceQuery.data;
  if (!choice || sources.length === 0) return null;

  const custom = !choice.use_server_default;
  const chosen = new Set(choice.sources);
  const put = (body: { use_server_default: boolean; sources: string[] }) =>
    save.mutate(body, {
      onError: (e) => toast.error(e instanceof Error ? e.message : "Couldn't save ratings."),
    });

  return (
    <div className="space-y-2">
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-0.5">
          <Label htmlFor={`rating-sources-${libraryId}`}>Choose ratings for this library</Label>
          <p className="text-muted-foreground text-xs">
            Pick which scores this library&apos;s titles show. Off, it shows what Settings → Library
            &amp; Metadata → Ratings shows. Scores come from your metadata providers, such as
            MDBList for IMDb and Rotten Tomatoes.
          </p>
        </div>
        <Switch
          id={`rating-sources-${libraryId}`}
          checked={custom}
          disabled={save.isPending}
          onCheckedChange={(on) =>
            put(
              on
                ? {
                    use_server_default: false,
                    sources: sources.filter((s) => s.always_shown).map((s) => s.source),
                  }
                : { use_server_default: true, sources: [] },
            )
          }
        />
      </div>
      {custom && (
        <div className="grid gap-2 pl-1 sm:grid-cols-2">
          {sources.map((s) => {
            const id = `rating-source-${libraryId}-${s.source}`;
            return (
              <label key={s.source} htmlFor={id} className="flex items-center gap-2 text-sm">
                <Checkbox
                  id={id}
                  checked={chosen.has(s.source)}
                  disabled={save.isPending}
                  onCheckedChange={(on) => {
                    const next = new Set(chosen);
                    if (on) next.add(s.source);
                    else next.delete(s.source);
                    put({ use_server_default: false, sources: [...next] });
                  }}
                />
                <span>{s.label}</span>
                {s.provider && (
                  <span className="text-muted-foreground text-xs">via {s.provider}</span>
                )}
              </label>
            );
          })}
        </div>
      )}
    </div>
  );
}
