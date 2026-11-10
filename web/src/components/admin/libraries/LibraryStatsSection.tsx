import type { Library } from "@/api/types";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";

import EpisodeStillSizes from "./EpisodeStillSizes";
import LibraryMediaStats from "./LibraryMediaStats";

/**
 * What each library holds: its video files by resolution and size on disk, and
 * for TV libraries how large and from where its episode thumbnails are.
 */
export default function LibraryStatsSection({ libraries }: { libraries: Library[] }) {
  if (libraries.length === 0) return null;
  return (
    <section className="space-y-3" aria-label="Library statistics">
      <h2 className="text-lg font-semibold">Library statistics</h2>
      <div className="grid gap-4 lg:grid-cols-2">
        {libraries.map((library) => (
          <Card key={library.id}>
            <CardHeader className="pb-3">
              <CardTitle className="text-base">{library.name}</CardTitle>
            </CardHeader>
            <CardContent className="space-y-5">
              <LibraryMediaStats libraryId={library.id} />
              {(library.type === "series" || library.type === "mixed") && (
                <EpisodeStillSizes libraryId={library.id} />
              )}
            </CardContent>
          </Card>
        ))}
      </div>
    </section>
  );
}
