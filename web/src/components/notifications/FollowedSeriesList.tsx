import { useState } from "react";
import { Bell, BellOff, Search, X } from "lucide-react";
import type { NotifiedSeries } from "@/api/v2/notifications";
import ViewTransitionLink from "@/components/ViewTransitionLink";
import { buildItemHref } from "@/lib/mediaNavigation";
import { LoadMoreOnScroll } from "@/components/LoadMoreOnScroll";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  useNotificationSeries,
  useSetNotificationSeriesFollowed,
  useSetNotificationSeriesMuted,
} from "@/hooks/queries/notifications";
import { useDebounce } from "@/hooks/useDebounce";
import { NotificationPoster } from "./NotificationPoster";

function FollowedSeriesRow({
  series,
  busy,
  onUnfollow,
  onToggleMute,
}: {
  series: NotifiedSeries;
  busy: boolean;
  onUnfollow: () => void;
  onToggleMute: () => void;
}) {
  const { item } = series;
  return (
    <li className="hover:bg-muted/60 flex items-center gap-1 rounded-xl pr-2 transition-colors">
      <ViewTransitionLink
        to={buildItemHref({ contentId: item.content_id })}
        className="flex min-w-0 flex-1 items-center gap-3 rounded-xl px-3 py-2"
      >
        <div className={series.muted ? "shrink-0 opacity-50" : "shrink-0"}>
          <NotificationPoster url={item.poster_url} thumbhash={item.poster_thumbhash} />
        </div>
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium">{item.title}</div>
          <div className="text-muted-foreground truncate text-xs">
            {series.muted
              ? "Muted: no notifications"
              : series.followed
                ? "Following: new episodes notify you"
                : "Not following"}
          </div>
        </div>
      </ViewTransitionLink>
      <Button
        variant="ghost"
        size="sm"
        className="text-muted-foreground shrink-0"
        disabled={busy}
        aria-label={`${series.muted ? "Unmute" : "Mute"} ${item.title}`}
        title={series.muted ? "Notify me about new episodes again" : "Stop all notifications"}
        onClick={onToggleMute}
      >
        {series.muted ? <Bell /> : <BellOff />}
        <span className="hidden sm:inline">{series.muted ? "Unmute" : "Mute"}</span>
      </Button>
      {series.followed && (
        <Button
          variant="ghost"
          size="sm"
          className="text-muted-foreground shrink-0"
          disabled={busy}
          aria-label={`Unfollow ${item.title}`}
          title="Stop following"
          onClick={onUnfollow}
        >
          <X />
          <span className="hidden sm:inline">Unfollow</span>
        </Button>
      )}
    </li>
  );
}

/**
 * The shows this profile follows (and the ones it muted), with a search box.
 * Follow a show from its page; here a follow can be removed or muted.
 */
export function FollowedSeriesList() {
  const [text, setText] = useState("");
  const q = useDebounce(text, 300).trim();
  const query = useNotificationSeries("all", { q, sort: "title" });
  const setFollowed = useSetNotificationSeriesFollowed();
  const setMuted = useSetNotificationSeriesMuted();
  const busyId = setFollowed.isPending
    ? setFollowed.variables?.seriesId
    : setMuted.isPending
      ? setMuted.variables?.seriesId
      : undefined;
  const series = query.data?.pages.flatMap((page) => page.series) ?? [];

  return (
    <div className="space-y-3">
      <div className="relative">
        <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 h-4 w-4 -translate-y-1/2" />
        <Input
          type="search"
          value={text}
          onChange={(event) => setText(event.target.value)}
          placeholder="Search followed shows"
          aria-label="Search followed shows"
          className="h-9 pl-8"
        />
      </div>
      {query.isLoading ? (
        <div className="space-y-2">
          <Skeleton className="h-14 w-full" />
          <Skeleton className="h-14 w-full" />
        </div>
      ) : series.length === 0 ? (
        <p className="text-muted-foreground px-1 text-sm">
          {q
            ? "No followed shows match."
            : "You're not following any shows yet. Open a show and choose Follow to get a notification for each new episode."}
        </p>
      ) : (
        <ul className="space-y-1">
          {series.map((entry) => (
            <FollowedSeriesRow
              key={entry.item.content_id}
              series={entry}
              busy={busyId === entry.item.content_id}
              onUnfollow={() =>
                setFollowed.mutate({
                  seriesId: entry.item.content_id,
                  title: entry.item.title,
                  followed: false,
                })
              }
              onToggleMute={() =>
                setMuted.mutate({
                  seriesId: entry.item.content_id,
                  title: entry.item.title,
                  muted: !entry.muted,
                })
              }
            />
          ))}
        </ul>
      )}
      <LoadMoreOnScroll
        hasNextPage={Boolean(query.hasNextPage)}
        isFetchingNextPage={query.isFetchingNextPage}
        isError={query.isFetchNextPageError}
        onLoadMore={() => void query.fetchNextPage()}
        errorMessage="Failed to load more shows."
      />
    </div>
  );
}
