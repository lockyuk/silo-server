import { useMemo, type ReactNode } from "react";
import { Link, useLocation } from "react-router";
import {
  Bell,
  Bookmark,
  Clock,
  Heart,
  LogOut,
  Search,
  Send,
  Settings,
  Shield,
  UserCircle,
} from "lucide-react";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { SiloBrand } from "@/components/SiloBrand";
import ViewTransitionLink from "@/components/ViewTransitionLink";
import { useAuth } from "@/hooks/useAuth";
import { useCurrentProfile } from "@/hooks/useCurrentProfile";
import { useIsActingAdmin } from "@/hooks/useIsActingAdmin";
import { useNotificationCapability } from "@/hooks/queries/notificationWebhooks";
import { useUnreadNotificationCount } from "@/hooks/queries/notifications";
import { useUserLibraries } from "@/hooks/queries/libraries";
import { useRequestFeatureStatus } from "@/hooks/queries/useRequests";
import { useUICustomization } from "@/hooks/useUICustomization";
import { useViewTransitionNavigate } from "@/hooks/useViewTransition";
import { menuItemKey } from "@/lib/uiCustomization";
import { cn } from "@/lib/utils";
import { useCriticalContentLoaded } from "@/lib/startupPriority";
import { prefetchCatalog } from "@/pages/catalogRoute";
import {
  buildLibraryCollectionCatalogHref,
  buildPersonalCatalogHref,
  buildQueryCatalogHref,
  buildSectionCatalogHref,
  buildUserCollectionCatalogHref,
} from "@/pages/catalogSearchParams";

interface TopMenuLink {
  key: string;
  href: string;
  label: string;
}

/**
 * The tabs in the bar: the viewer's own primary menu when they have one, else
 * Home, each library, For You and Calendar, which is what the sidebar showed.
 */
function useTopMenuLinks(): TopMenuLink[] {
  const { data: libraries } = useUserLibraries();
  const { primaryMenu } = useUICustomization();
  return useMemo<TopMenuLink[]>(() => {
    if (!primaryMenu) {
      return [
        { key: "home", href: "/", label: "Home" },
        ...(libraries ?? []).map((library) => ({
          key: `library:${library.id}`,
          href: `/library/${library.id}`,
          label: library.name,
        })),
        { key: "for_you", href: "/recommendations", label: "For You" },
        { key: "calendar", href: "/calendar", label: "Calendar" },
      ];
    }
    return primaryMenu.items.flatMap((item): TopMenuLink[] => {
      const key = menuItemKey(item);
      if (item.type === "library") {
        const library = libraries?.find((candidate) => candidate.id === item.library_id);
        return library
          ? [{ key, href: `/library/${library.id}`, label: item.label || library.name }]
          : [];
      }
      if (item.type === "section") {
        if (!libraries?.some((candidate) => candidate.id === item.library_id)) return [];
        return [
          {
            key,
            href: buildSectionCatalogHref({
              scope: "library",
              libraryId: item.library_id,
              sectionId: item.section_id,
              title: item.label,
            }),
            label: item.label,
          },
        ];
      }
      if (item.type === "collection") {
        if (
          item.library_id !== undefined &&
          !libraries?.some((candidate) => candidate.id === item.library_id)
        ) {
          return [];
        }
        return [
          {
            key,
            href:
              item.library_id === undefined
                ? buildUserCollectionCatalogHref(item.collection_id, item.label)
                : buildLibraryCollectionCatalogHref(
                    item.collection_id,
                    item.label,
                    item.library_id,
                  ),
            label: item.label,
          },
        ];
      }
      switch (item.destination) {
        case "home":
          return [{ key, href: "/", label: "Home" }];
        case "for_you":
          return [{ key, href: "/recommendations", label: "For You" }];
        case "calendar":
          return [{ key, href: "/calendar", label: "Calendar" }];
        default:
          return [];
      }
    });
  }, [libraries, primaryMenu]);
}

// A tab at rest, selected, and focused or hovered, as on the TV: selected is the
// theme's selected fill, and the focused tab takes the primary fill and its
// readable text colour.
const TAB_BASE =
  "relative rounded-full px-4 py-2 text-[0.9375rem] font-medium whitespace-nowrap transition-colors duration-150 outline-none " +
  "hover:bg-primary hover:text-primary-foreground focus-visible:bg-primary focus-visible:text-primary-foreground";
const TAB_REST = "text-foreground/85";
const TAB_SELECTED = "bg-accent text-foreground";

const ROW =
  "gap-3 rounded-lg px-3 py-2.5 text-[0.9375rem] font-semibold " +
  "focus:bg-primary focus:text-primary-foreground data-[highlighted]:bg-primary data-[highlighted]:text-primary-foreground";

interface TopMenuBarProps {
  /** Extra controls placed before the bell, such as the admin activity indicator. */
  trailing?: ReactNode;
}

/**
 * The desktop navigation, matching the TV app's top menu: the wordmark at the
 * left, a floating capsule of tabs with search at the centre, and the
 * notification bell and profile avatar at the right. The avatar opens the same
 * list as the TV's profile dropdown. Below the lg breakpoint the page keeps its
 * own header and drawer.
 */
export default function TopMenuBar({ trailing }: TopMenuBarProps) {
  const location = useLocation();
  const navigate = useViewTransitionNavigate();
  const { user, logout, clearProfile } = useAuth();
  const { profile } = useCurrentProfile();
  const showAdminNav = useIsActingAdmin();
  const links = useTopMenuLinks();
  const requestStatus = useRequestFeatureStatus();
  const showRequests = requestStatus.data?.requests_enabled === true;
  const notificationCapability = useNotificationCapability();
  const showNotifications = notificationCapability.isError
    ? false
    : (notificationCapability.data?.in_app.enabled ?? true);
  const criticalContentLoaded = useCriticalContentLoaded();
  const { data: unread } = useUnreadNotificationCount(
    Boolean(profile) && showNotifications && criticalContentLoaded,
  );

  const name = profile?.name ?? user?.username ?? "User";
  const initial = name.charAt(0).toUpperCase() || "?";
  const hasUnread = (unread ?? 0) > 0;

  // The longest matching tab wins, so a library under /library/2 does not also
  // light up Home, and nested pages keep their tab selected.
  const activeKey = useMemo(() => {
    const path = location.pathname;
    let best: { key: string; length: number } | null = null;
    for (const link of links) {
      const base = link.href.split("?")[0] ?? link.href;
      const matches = base === "/" ? path === "/" : path === base || path.startsWith(`${base}/`);
      if (matches && (!best || base.length > best.length))
        best = { key: link.key, length: base.length };
    }
    return best?.key ?? null;
  }, [links, location.pathname]);

  return (
    <header className="pointer-events-none fixed inset-x-0 top-0 z-40 hidden lg:block">
      <div className="mx-auto flex max-w-[120rem] items-center justify-between gap-6 px-10 pt-5">
        <ViewTransitionLink to="/" className="pointer-events-auto shrink-0" aria-label="Home">
          <SiloBrand className="h-11 w-[104px]" />
        </ViewTransitionLink>

        <nav
          aria-label="Primary"
          className="border-border bg-popover/75 pointer-events-auto flex min-w-0 items-center gap-1 rounded-full border p-1.5 shadow-[0_10px_30px_-12px_rgba(0,0,0,0.7)] backdrop-blur-xl"
        >
          <ViewTransitionLink
            to={buildQueryCatalogHref()}
            onPointerDown={prefetchCatalog}
            onFocus={prefetchCatalog}
            aria-label="Search"
            className={cn(
              TAB_BASE,
              "flex size-10 items-center justify-center p-0",
              location.pathname === "/catalog" ? TAB_SELECTED : TAB_REST,
            )}
          >
            <Search className="size-[1.125rem]" />
          </ViewTransitionLink>
          {links.map((link) => {
            const selected = link.key === activeKey;
            return (
              <ViewTransitionLink
                key={link.key}
                to={link.href}
                aria-current={selected ? "page" : undefined}
                className={cn(TAB_BASE, selected ? TAB_SELECTED : TAB_REST)}
              >
                {link.label}
              </ViewTransitionLink>
            );
          })}
        </nav>

        <div className="pointer-events-auto flex shrink-0 items-center gap-3">
          {trailing}
          {showNotifications && (
            <Link
              to="/notifications"
              aria-label={hasUnread ? "Notifications, unread" : "Notifications"}
              className={cn(
                "border-border bg-popover/75 text-foreground/85 relative flex size-11 items-center justify-center rounded-full border backdrop-blur-xl transition-colors outline-none",
                "hover:bg-primary hover:text-primary-foreground focus-visible:bg-primary focus-visible:text-primary-foreground",
              )}
            >
              <Bell className="size-[1.125rem]" />
              {hasUnread && (
                <span
                  aria-hidden="true"
                  className="bg-destructive ring-background absolute top-2.5 right-2.5 size-2 rounded-full ring-2"
                />
              )}
            </Link>
          )}
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                aria-label={`${name} menu`}
                className="border-border focus-visible:ring-ring data-[state=open]:ring-ring rounded-full border transition-shadow outline-none focus-visible:ring-2 data-[state=open]:ring-2"
              >
                <Avatar className="size-11">
                  {profile?.avatar_url ? <AvatarImage src={profile.avatar_url} alt={name} /> : null}
                  <AvatarFallback className="bg-secondary text-foreground text-base font-bold">
                    {initial}
                  </AvatarFallback>
                </Avatar>
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent
              align="end"
              sideOffset={10}
              className="border-border bg-popover w-[min(21rem,calc(100vw-1.5rem))] rounded-2xl border p-2 shadow-[0_18px_40px_-14px_rgba(0,0,0,0.75)]"
            >
              <div className="flex items-center gap-3 px-3 py-3">
                <Avatar className="size-11">
                  {profile?.avatar_url ? <AvatarImage src={profile.avatar_url} alt={name} /> : null}
                  <AvatarFallback className="bg-secondary text-foreground text-base font-bold">
                    {initial}
                  </AvatarFallback>
                </Avatar>
                <div className="flex min-w-0 flex-col">
                  <span className="truncate text-base font-semibold">{name}</span>
                  {user?.username && user.username !== name && (
                    <span className="text-muted-foreground truncate text-xs font-medium tracking-wide uppercase">
                      {user.username}
                    </span>
                  )}
                </div>
              </div>
              <DropdownMenuSeparator />
              {profile && (
                <DropdownMenuItem
                  className={ROW}
                  onClick={() => {
                    clearProfile();
                    navigate("/profiles");
                  }}
                >
                  <UserCircle className="size-5" />
                  Switch Profile
                </DropdownMenuItem>
              )}
              <DropdownMenuItem
                className={ROW}
                onClick={() => navigate(buildPersonalCatalogHref("watchlist"))}
              >
                <Bookmark className="size-5" />
                Watchlist
              </DropdownMenuItem>
              <DropdownMenuItem
                className={ROW}
                onClick={() => navigate(buildPersonalCatalogHref("favorites"))}
              >
                <Heart className="size-5" />
                Favorites
              </DropdownMenuItem>
              <DropdownMenuItem
                className={ROW}
                onClick={() => navigate(buildPersonalCatalogHref("history"))}
              >
                <Clock className="size-5" />
                History
              </DropdownMenuItem>
              {showRequests && (
                <DropdownMenuItem className={ROW} onClick={() => navigate("/requests")}>
                  <Send className="size-5" />
                  Requests
                </DropdownMenuItem>
              )}
              <DropdownMenuSeparator />
              <DropdownMenuItem className={ROW} onClick={() => navigate("/settings")}>
                <Settings className="size-5" />
                Settings
              </DropdownMenuItem>
              {showAdminNav && (
                <DropdownMenuItem className={ROW} onClick={() => navigate("/admin")}>
                  <Shield className="size-5" />
                  Admin
                </DropdownMenuItem>
              )}
              <DropdownMenuItem className={ROW} onClick={logout}>
                <LogOut className="size-5" />
                Sign Out
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>
    </header>
  );
}
