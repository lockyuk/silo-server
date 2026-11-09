import { useCallback } from "react";
import type { ItemDetail } from "@/api/types";
import { useToggleFavorite } from "@/hooks/queries/favorites";
import { useDeleteRating, useSetRating } from "@/hooks/queries/ratings";
import { useToggleWatchlist } from "@/hooks/queries/watchlist";
import { useNotificationCapability } from "@/hooks/queries/notificationWebhooks";
import {
  useFollowedSeriesIds,
  useSetNotificationSeriesFollowed,
} from "@/hooks/queries/notifications";
import { canMarkNotInterested, useToggleNotInterested } from "@/hooks/queries/notInterested";
import ActionBar, { type ActionBarProps } from "./ActionBar";
import { useDetailWatchedActions } from "./useDetailWatchedActions";

type UserActionProps =
  | "isWatched"
  | "watchedLabel"
  | "onToggleWatched"
  | "isUpdatingWatched"
  | "onMarkUnwatched"
  | "onToggleFavorite"
  | "isFavorite"
  | "onToggleWatchlist"
  | "inWatchlist"
  | "onToggleFollow"
  | "isFollowing"
  | "onToggleNotInterested"
  | "notInterested"
  | "rating"
  | "onRatingChange";

interface MediaUserActionBarProps extends Omit<ActionBarProps, UserActionProps> {
  item: ItemDetail;
}

/**
 * Owns the frequently changing mutation state for the detail-page user actions.
 * Keeping these hooks below the page content boundary prevents a mutation's
 * pending/settled notifications from re-rendering the hero and below-fold page.
 */
export default function MediaUserActionBar({ item, ...props }: MediaUserActionBarProps) {
  const isFavorite = item.user_state?.is_favorite ?? false;
  const inWatchlist = item.user_state?.in_watchlist ?? false;
  const notInterested = item.user_state?.not_interested ?? false;
  const watchedActions = useDetailWatchedActions(item);
  const { mutate: toggleFavorite } = useToggleFavorite(item.content_id);
  const { mutate: toggleWatchlist } = useToggleWatchlist(item.content_id);
  const { mutate: toggleNotInterested } = useToggleNotInterested(item.content_id);
  const { mutate: setRating } = useSetRating(item.content_id);
  const { mutate: deleteRating } = useDeleteRating(item.content_id);
  // Following a series asks for a notification per new episode, so the choice
  // only appears where the server delivers notifications.
  const notificationCapability = useNotificationCapability();
  const canFollow = item.type === "series" && (notificationCapability.data?.in_app?.enabled ?? false);
  const followed = useFollowedSeriesIds(canFollow);
  const { mutate: setFollowed } = useSetNotificationSeriesFollowed();
  const isFollowing = followed.ids.has(item.content_id);
  const handleToggleFollow = useCallback(
    () => setFollowed({ seriesId: item.content_id, title: item.title, followed: !isFollowing }),
    [isFollowing, item.content_id, item.title, setFollowed],
  );
  const handleToggleFavorite = useCallback(
    () => toggleFavorite(isFavorite),
    [isFavorite, toggleFavorite],
  );
  const handleToggleWatchlist = useCallback(
    () => toggleWatchlist(inWatchlist),
    [inWatchlist, toggleWatchlist],
  );
  const handleToggleNotInterested = useCallback(
    () => toggleNotInterested(notInterested),
    [notInterested, toggleNotInterested],
  );
  const handleRatingChange = useCallback(
    (rating: number | null) => {
      if (rating === null) {
        deleteRating();
      } else {
        setRating(rating);
      }
    },
    [deleteRating, setRating],
  );

  return (
    <ActionBar
      {...props}
      {...watchedActions}
      itemTitle={item.title}
      onToggleFavorite={handleToggleFavorite}
      isFavorite={isFavorite}
      onToggleWatchlist={handleToggleWatchlist}
      inWatchlist={inWatchlist}
      onToggleFollow={canFollow && !followed.isLoading ? handleToggleFollow : undefined}
      isFollowing={isFollowing}
      onToggleNotInterested={
        canMarkNotInterested(item.type) && item.user_state ? handleToggleNotInterested : undefined
      }
      notInterested={notInterested}
      rating={item.user_rating ?? null}
      onRatingChange={handleRatingChange}
    />
  );
}
