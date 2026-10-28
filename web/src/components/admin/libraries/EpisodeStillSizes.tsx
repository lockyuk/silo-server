import { Loader2 } from "lucide-react";

import { Label } from "@/components/ui/label";
import { useLibraryEpisodeStillSizes } from "@/hooks/queries/admin/libraries";
import { cn } from "@/lib/utils";

const SOURCES = [
  { key: "tmdb", label: "TMDB" },
  { key: "tvdb", label: "TheTVDB" },
  { key: "frame", label: "Video frame" },
  { key: "local", label: "Local file" },
  { key: "other", label: "Other" },
] as const;

const SIZES = [
  { key: "4k", label: "4K" },
  { key: "1080p", label: "1080p" },
  { key: "720p", label: "720p" },
  { key: "below_720p", label: "Below 720p" },
  { key: "unmeasured", label: "Not measured" },
] as const;

const FULL_HD = new Set(["4k", "1080p"]);

/**
 * The library's episode thumbnails counted by where they came from and how
 * big they are. Sizes are measured by the "Episode Thumbnails From Video"
 * task; a still still downloading counts as not measured.
 */
export default function EpisodeStillSizes({ libraryId }: { libraryId: number }) {
  const { data, isLoading, isError } = useLibraryEpisodeStillSizes(libraryId);

  const count = (source: string, size: string) =>
    data?.counts
      .filter(
        (c) =>
          c.source === source &&
          (c.size === size || (size === "unmeasured" && c.size === "unreadable")),
      )
      .reduce((sum, c) => sum + c.count, 0) ?? 0;
  const sizeTotal = (size: string) => SOURCES.reduce((sum, s) => sum + count(s.key, size), 0);
  const sourceTotal = (source: string) =>
    data?.counts.filter((c) => c.source === source).reduce((sum, c) => sum + c.count, 0) ?? 0;
  const none = sourceTotal("none");
  const fullHD =
    data?.counts.filter((c) => FULL_HD.has(c.size)).reduce((sum, c) => sum + c.count, 0) ?? 0;
  const visibleSources = SOURCES.filter((s) => sourceTotal(s.key) > 0);

  return (
    <div className="space-y-1.5">
      <Label>Episode thumbnail sizes</Label>
      {isLoading && (
        <p className="text-muted-foreground flex items-center gap-2 text-xs">
          <Loader2 className="size-3 animate-spin" /> Counting thumbnails…
        </p>
      )}
      {isError && <p className="text-destructive text-xs">Could not load thumbnail sizes.</p>}
      {data && (
        <>
          <p className="text-muted-foreground text-xs">
            {fullHD.toLocaleString()} of {data.total.toLocaleString()} episodes have a Full HD or
            larger thumbnail
            {none > 0 && <>; {none.toLocaleString()} have none</>}.
          </p>
          <div className="overflow-x-auto rounded-md border">
            <table className="w-full text-xs tabular-nums">
              <thead className="bg-muted/50 text-muted-foreground">
                <tr>
                  <th className="px-2.5 py-1.5 text-left font-medium">Source</th>
                  {SIZES.map((size) => (
                    <th
                      key={size.key}
                      className="px-2.5 py-1.5 text-right font-medium whitespace-nowrap"
                    >
                      {size.label}
                    </th>
                  ))}
                  <th className="px-2.5 py-1.5 text-right font-medium">Total</th>
                </tr>
              </thead>
              <tbody>
                {visibleSources.map((source) => (
                  <tr key={source.key} className="border-t">
                    <td className="px-2.5 py-1.5 whitespace-nowrap">{source.label}</td>
                    {SIZES.map((size) => {
                      const n = count(source.key, size.key);
                      return (
                        <td
                          key={size.key}
                          className={cn(
                            "px-2.5 py-1.5 text-right",
                            n === 0 && "text-muted-foreground/50",
                            n > 0 && FULL_HD.has(size.key) && "text-foreground font-medium",
                          )}
                        >
                          {n.toLocaleString()}
                        </td>
                      );
                    })}
                    <td className="px-2.5 py-1.5 text-right">
                      {sourceTotal(source.key).toLocaleString()}
                    </td>
                  </tr>
                ))}
                {none > 0 && (
                  <tr className="border-t">
                    <td className="text-muted-foreground px-2.5 py-1.5">No thumbnail</td>
                    <td colSpan={SIZES.length} />
                    <td className="px-2.5 py-1.5 text-right">{none.toLocaleString()}</td>
                  </tr>
                )}
                <tr className="bg-muted/30 border-t font-medium">
                  <td className="px-2.5 py-1.5">All</td>
                  {SIZES.map((size) => (
                    <td key={size.key} className="px-2.5 py-1.5 text-right">
                      {sizeTotal(size.key).toLocaleString()}
                    </td>
                  ))}
                  <td className="px-2.5 py-1.5 text-right">{data.total.toLocaleString()}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <p className="text-muted-foreground text-xs">
            4K is 3840×2160 or larger, 1080p 1920×1080, 720p 1280×720. New thumbnails are measured
            every 15 minutes.
          </p>
        </>
      )}
    </div>
  );
}
