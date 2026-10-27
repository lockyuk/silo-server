import { v2 } from "@/api/v2/request";

export type Filmography = Awaited<ReturnType<typeof getPersonFilmography>>;
export type FilmographyCredit = Filmography["credits"][number];

export function getPersonFilmography(id: string, signal?: AbortSignal) {
  return v2("GET /api/v2/catalog/people/{id}/filmography", { path: { id }, signal });
}

/** How the person worked on a title, e.g. "as Tyler Durden · Director". */
export function roleSummary(credit: FilmographyCredit) {
  return credit.roles
    .map((r) => {
      if (r.department === "Acting") {
        const name = r.character ? `as ${r.character}` : "Actor";
        return r.episode_count
          ? `${name} (${r.episode_count} episode${r.episode_count === 1 ? "" : "s"})`
          : name;
      }
      return r.job || r.department;
    })
    .join(" · ");
}
