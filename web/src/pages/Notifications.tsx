import { useAuth } from "@/hooks/useAuth";
import { notificationScope } from "@/api/v2/notifications";
import { useState, useRef } from "react";
import { Bell, BellOff, Check, CheckCheck, Mail, Smartphone } from "lucide-react";
import type { AppNotification, NotificationChannel } from "@/api/types";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { TablePagination } from "@/components/ui/pagination";
import {
  formatEpisodeCode,
  useMarkAllNotificationsRead,
  useMarkNotificationRead,
  useNotifications,
  useUnreadNotificationCount,
} from "@/hooks/queries/notifications";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { preferredDateLocale } from "@/lib/datetime";
import { requestDetailHref } from "@/lib/mediaRequests";
import ViewTransitionLink from "@/components/ViewTransitionLink";
import { NotificationPoster } from "@/components/notifications/NotificationPoster";

const PAGE_SIZES = [10, 20, 50];
const PAGE_SIZE_KEY = "silo.notifications.pageSize";

function storedPageSize(): number {
  try {
    const value = Number(localStorage.getItem(PAGE_SIZE_KEY));
    if (PAGE_SIZES.includes(value)) return value;
  } catch {
    /* Storage is unavailable; use the default. */
  }
  return 20;
}

function storePageSize(size: number) {
  try {
    localStorage.setItem(PAGE_SIZE_KEY, String(size));
  } catch {
    /* Storage is unavailable; the choice lasts for this visit. */
  }
}

const CHANNELS: Record<NotificationChannel, { label: string; icon: typeof Mail }> = {
  email_instant: { label: "Instant email", icon: Mail },
  email_daily: { label: "Daily digest", icon: Mail },
  email_weekly: { label: "Weekly digest", icon: Mail },
  push: { label: "Push", icon: Smartphone },
};

function formatNotificationTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  const diffMs = Date.now() - date.getTime();
  const diffMinutes = Math.round(diffMs / 60_000);
  if (diffMinutes < 1) {
    return "Just now";
  }
  if (diffMinutes < 60) {
    return `${diffMinutes}m ago`;
  }
  const diffHours = Math.round(diffMinutes / 60);
  if (diffHours < 24) {
    return `${diffHours}h ago`;
  }
  const diffDays = Math.round(diffHours / 24);
  if (diffDays < 7) {
    return `${diffDays}d ago`;
  }
  return date.toLocaleDateString(preferredDateLocale(), { month: "short", day: "numeric" });
}

function notificationTitle(notification: AppNotification): string {
  if (notification.type === "episode.available") {
    return notification.series_title || "New episode available";
  }
  if (notification.type === "movie.available") {
    return notification.series_title || "New movie available";
  }
  if (notification.type === "request.fulfilled") {
    return notification.series_title || "Request available";
  }
  if (notification.type === "request.approved" || notification.type === "request.declined") {
    return notification.reason_flags?.title || "Media request";
  }
  // Unknown types render with a generic fallback by design — the type
  // registry is extensible.
  return "Notification";
}

function notificationDescription(notification: AppNotification): string {
  if (notification.type === "episode.available") {
    const code = formatEpisodeCode(notification);
    return (
      [code, notification.episode_title].filter(Boolean).join(" — ") || "New episode available"
    );
  }
  if (notification.type === "movie.available") {
    return "New movie in your library";
  }
  if (notification.type === "request.fulfilled") {
    const mediaType = notification.reason_flags?.media_type;
    if (notification.reason_flags?.follower) {
      return mediaType === "movie"
        ? "A movie you followed is now available"
        : mediaType === "series"
          ? "A series you followed is now available"
          : "A title you followed is now available";
    }
    return mediaType === "movie"
      ? "Your requested movie is now available"
      : mediaType === "series"
        ? "Your requested series is now available"
        : "Your request is now available";
  }
  if (notification.type === "request.approved") {
    return "Your request was approved";
  }
  if (notification.type === "request.declined") {
    const reason = notification.reason_flags?.reason;
    return reason ? `Your request was declined — ${reason}` : "Your request was declined";
  }
  return notification.type;
}

/**
 * Where a row leads. Episode and fulfilled-request rows open the catalog item;
 * approved and declined requests have no catalog item yet, so they open the
 * title's page from the TMDB id their payload carries.
 */
function notificationHref(notification: AppNotification): string | null {
  if (notification.episode_id) return `/item/${notification.episode_id}`;
  if (notification.series_id) return `/item/${notification.series_id}`;
  if (notification.type === "request.approved" || notification.type === "request.declined") {
    const { media_type: mediaType, tmdb_id: tmdbID } = notification.reason_flags ?? {};
    if ((mediaType === "movie" || mediaType === "series") && tmdbID && tmdbID > 0) {
      return requestDetailHref(mediaType, tmdbID);
    }
  }
  return null;
}

function NotificationRow({
  notification,
  onMarkRead,
}: {
  notification: AppNotification;
  onMarkRead: (id: string) => void;
}) {
  const unread = !notification.read_at;
  const detailHref = notificationHref(notification);

  const body = (
    <>
      <NotificationPoster url={notification.poster_url} thumbhash={notification.poster_thumbhash} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          {unread && (
            <span
              className="h-2 w-2 shrink-0 rounded-full"
              style={{ background: "var(--primary)" }}
              aria-label="Unread"
            />
          )}
          <span className={`truncate text-sm ${unread ? "font-semibold" : "font-medium"}`}>
            {notificationTitle(notification)}
          </span>
          <span className="text-muted-foreground ml-auto shrink-0 text-xs">
            {formatNotificationTime(notification.created_at)}
          </span>
        </div>
        <div className="text-muted-foreground mt-0.5 truncate text-sm">
          {notificationDescription(notification)}
        </div>
        {notification.sent_via && notification.sent_via.length > 0 && (
          <div className="mt-1.5 flex flex-wrap gap-1" aria-label="Sent as">
            {notification.sent_via.map((channel) => {
              const info = CHANNELS[channel];
              if (!info) return null;
              const Icon = info.icon;
              return (
                <span
                  key={channel}
                  className="bg-muted text-muted-foreground inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-medium"
                >
                  <Icon className="h-3 w-3" />
                  {info.label}
                </span>
              );
            })}
          </div>
        )}
      </div>
    </>
  );

  return (
    <li className="group relative">
      {detailHref ? (
        <ViewTransitionLink
          to={detailHref}
          onClick={() => unread && onMarkRead(notification.id)}
          className={`hover:bg-muted/60 flex items-start gap-3 rounded-xl px-3 py-3 transition-colors ${
            unread ? "bg-muted/30" : ""
          }`}
        >
          {body}
        </ViewTransitionLink>
      ) : (
        <div
          className={`flex items-start gap-3 rounded-xl px-3 py-3 ${unread ? "bg-muted/30" : ""}`}
        >
          {body}
        </div>
      )}
      {unread && (
        <Button
          variant="ghost"
          size="icon"
          className="absolute right-2 bottom-2 h-7 w-7 opacity-0 transition-opacity group-hover:opacity-100"
          title="Mark as read"
          onClick={(event) => {
            event.preventDefault();
            onMarkRead(notification.id);
          }}
        >
          <Check className="h-4 w-4" />
        </Button>
      )}
    </li>
  );
}

export default function Notifications() {
  useAuth();
  return <NotificationsInbox key={notificationScope()} />;
}
function NotificationsInbox() {
  useDocumentTitle("Notifications");
  const [statusFilter, setStatusFilter] = useState<"all" | "unread">("all");
  const [pageSize, setPageSize] = useState(storedPageSize);
  const [page, setPage] = useState(0);
  const [turning, setTurning] = useState(false);
  const list = useNotifications(statusFilter, pageSize);
  const { data: unreadCount } = useUnreadNotificationCount();
  const markRead = useMarkNotificationRead();
  const markAllRead = useMarkAllNotificationsRead();
  const markAllBusy = useRef(false);
  const pages = list.data?.pages ?? [];
  const cutoff = pages[0]?.read_cutoff;
  async function markDisplayedRead() {
    if (markAllBusy.current || !cutoff) return;
    markAllBusy.current = true;
    try {
      await markAllRead.mutateAsync(cutoff);
    } catch {
      /* The mutation exposes the error and refreshes authoritative state. */
    } finally {
      markAllBusy.current = false;
    }
  }

  // Pages come one cursor at a time, so a jump ahead reads the pages between.
  async function goToPage(target: number) {
    if (turning) return;
    setTurning(true);
    try {
      let loaded = pages.length;
      let more = list.hasNextPage;
      while (loaded <= target && more) {
        const result = await list.fetchNextPage();
        if (result.isError) break;
        loaded = result.data?.pages.length ?? loaded;
        more = result.hasNextPage;
      }
      setPage(Math.min(target, Math.max(0, loaded - 1)));
    } finally {
      setTurning(false);
    }
  }

  const shownPage = Math.min(page, Math.max(0, pages.length - 1));
  const notifications = pages[shownPage]?.notifications ?? [];
  const total = pages[0]?.total ?? notifications.length;

  return (
    <div className="mx-auto w-full max-w-3xl px-4 py-8">
      <div className="mb-6 flex flex-wrap items-center gap-3">
        <h1 className="flex items-center gap-2.5 text-2xl font-semibold">
          <Bell className="h-6 w-6" />
          Notifications
        </h1>
        <div className="ml-auto flex items-center gap-2">
          {(unreadCount ?? 0) > 0 && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => void markDisplayedRead()}
              disabled={markAllRead.isPending || markAllRead.isError || !cutoff}
            >
              <CheckCheck className="mr-1.5 h-4 w-4" />
              Mark all read
            </Button>
          )}
        </div>
      </div>

      <div className="mb-4 flex gap-1.5">
        {(["all", "unread"] as const).map((status) => (
          <Button
            key={status}
            variant={statusFilter === status ? "secondary" : "ghost"}
            size="sm"
            onClick={() => {
              setStatusFilter(status);
              setPage(0);
            }}
          >
            {status === "all" ? "All" : `Unread${unreadCount ? ` (${unreadCount})` : ""}`}
          </Button>
        ))}
      </div>

      {markAllRead.isError && (
        <p role="alert">
          Could not mark the displayed notifications read.{" "}
          <Button
            onClick={() => {
              void list.restart().then(() => markAllRead.reset());
            }}
          >
            Reload inbox
          </Button>{" "}
          before trying again.
        </p>
      )}
      {list.isError && (
        <div role="alert">
          Could not load notifications.{" "}
          <Button
            onClick={() => {
              void list.restart().then(() => markAllRead.reset());
            }}
          >
            Reload notifications
          </Button>
        </div>
      )}
      {list.isLoading ? (
        <div className="space-y-2">
          {Array.from({ length: 5 }).map((_, index) => (
            <Skeleton key={index} className="h-20 w-full rounded-xl" />
          ))}
        </div>
      ) : notifications.length === 0 && !list.isError ? (
        <div className="text-muted-foreground flex flex-col items-center gap-3 py-20 text-center">
          <BellOff className="h-10 w-10 opacity-50" />
          <div className="text-sm">
            {statusFilter === "unread" ? "No unread notifications" : "No notifications yet"}
          </div>
          <div className="max-w-sm text-xs">
            New episodes and movies show up here as they arrive.
          </div>
        </div>
      ) : (
        <>
          <ul className="list-none space-y-1">
            {notifications.map((notification) => (
              <NotificationRow
                key={notification.id}
                notification={notification}
                onMarkRead={(id) => markRead.mutate(id)}
              />
            ))}
          </ul>
          <TablePagination
            className="mt-6"
            page={shownPage}
            pageSize={pageSize}
            total={total}
            onPageChange={(next) => void goToPage(next)}
            onPageSizeChange={(size) => {
              storePageSize(size);
              setPageSize(size);
              setPage(0);
            }}
            pageSizeOptions={PAGE_SIZES}
            itemNoun="notification"
            isFetching={turning || list.isFetchingNextPage}
          />
        </>
      )}
    </div>
  );
}
