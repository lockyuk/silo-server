import { useId, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { Loader2, Play, RotateCcw, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { v2 } from "@/api/v2/request";
import { TaskStatusBadge } from "@/components/admin/TaskStatusBadge";
import { useEventChannel } from "@/components/realtimeEventsContext";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { useRunTask, useTasks } from "@/hooks/queries/admin/tasks";
import { formatDateTime } from "@/lib/datetime";

import { FieldGroup } from "./FieldGroup";
import { SettingFieldRow } from "./SettingField";

const TASK_KEY = "download_theme_songs";
const QUERY_KEY = ["admin", "theme-songs"] as const;

type Overview = Awaited<ReturnType<typeof readOverview>>;
type Series = Overview["series"][number];
type State = Series["state"];

function readOverview() {
  return v2("GET /api/v2/admin/theme-songs");
}

const STATE_LABELS: Record<State, string> = {
  folder: "In folder",
  downloaded: "Downloaded",
  not_available: "Not available",
  failed: "Failed",
  pending: "Waiting",
};

const FILTERS: { value: State | "all"; label: string }[] = [
  { value: "all", label: "All" },
  { value: "downloaded", label: "Downloaded" },
  { value: "folder", label: "In folder" },
  { value: "not_available", label: "Not available" },
  { value: "failed", label: "Failed" },
  { value: "pending", label: "Waiting" },
];

/** Downloads TV theme songs into Silo's storage, never into media folders. */
export function ThemeSongsSettings() {
  useEventChannel("tasks");
  const client = useQueryClient();
  const enabledId = useId();
  const recheckId = useId();
  const { data } = useQuery({ queryKey: QUERY_KEY, queryFn: readOverview });
  const { data: tasks } = useTasks();
  const runTask = useRunTask();
  const task = tasks?.find((t) => t.key === TASK_KEY);
  const running = task?.state === "running" || task?.state === "cancelling";
  const [filter, setFilter] = useState<State | "all">("all");
  const [showList, setShowList] = useState(false);
  const [recheck, setRecheck] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: (body: Overview["config"]) =>
      v2("PUT /api/v2/admin/theme-songs/settings", { body }),
    onSuccess: (next) => client.setQueryData(QUERY_KEY, next),
    onError: () => toast.error("Theme song settings could not be saved"),
  });

  const seriesAction = useMutation({
    mutationFn: ({ id, action }: { id: string; action: "retry" | "delete" }) =>
      action === "retry"
        ? v2("POST /api/v2/admin/theme-songs/series/{id}/retry", { path: { id } })
        : v2("DELETE /api/v2/admin/theme-songs/series/{id}", { path: { id } }),
    onSuccess: (updated, { action }) => {
      void client.invalidateQueries({ queryKey: QUERY_KEY });
      if (action === "retry") {
        toast.success(
          updated.state === "downloaded"
            ? `Downloaded the theme for ${updated.title}`
            : `${updated.title}: ${STATE_LABELS[updated.state].toLowerCase()}`,
        );
      } else {
        toast.success(`Deleted the downloaded theme for ${updated.title}`);
      }
    },
    onError: () => toast.error("The theme could not be changed"),
  });

  const visible = useMemo(
    () => (data?.series ?? []).filter((s) => filter === "all" || s.state === filter),
    [data, filter],
  );

  const config = data?.config;
  const counts = data?.counts;

  return (
    <FieldGroup
      label="Theme songs"
      description="Downloads theme songs for TV series that have no theme file in their folder. They are kept in Silo's storage; media folders are never written to."
    >
      <SettingFieldRow
        label="Download TV theme songs"
        htmlFor={enabledId}
        description="Looks series up by their TheTVDB id in Plex's public theme library, once a day."
      >
        <Switch
          id={enabledId}
          checked={config?.enabled ?? false}
          disabled={!config || save.isPending}
          onCheckedChange={(enabled) => config && save.mutate({ ...config, enabled })}
        />
      </SettingFieldRow>
      <SettingFieldRow
        label="Ask again after (days)"
        htmlFor={recheckId}
        description="How long to wait before asking again for a theme the library didn't have."
      >
        <Input
          id={recheckId}
          type="number"
          min={1}
          max={365}
          className="w-24"
          value={recheck ?? String(config?.recheck_days ?? "")}
          disabled={!config}
          onChange={(e) => setRecheck(e.target.value)}
          onBlur={() => {
            const days = Number(recheck);
            if (
              config &&
              recheck != null &&
              days >= 1 &&
              days <= 365 &&
              days !== config.recheck_days
            ) {
              save.mutate({ ...config, recheck_days: days });
            }
            setRecheck(null);
          }}
        />
      </SettingFieldRow>

      <div className="border-border flex flex-col gap-3 border-b py-4 sm:flex-row sm:items-center">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-sm font-semibold">Series</h3>
            {task?.last_execution && <TaskStatusBadge result={task.last_execution} />}
          </div>
          {counts && (
            <p className="text-muted-foreground mt-1 text-xs">
              {counts.folder} in folders · {counts.downloaded} downloaded · {counts.not_available}{" "}
              not available
              {counts.failed > 0 ? ` · ${counts.failed} failed` : ""}
              {counts.pending > 0 ? ` · ${counts.pending} waiting` : ""}
            </p>
          )}
          <p className="text-muted-foreground mt-1 text-xs">
            {task?.last_execution
              ? `Last run ${formatDateTime(task.last_execution.completed_at)}`
              : "Never run"}
          </p>
        </div>
        <div className="flex flex-col gap-2 sm:flex-row">
          <Button variant="outline" size="sm" onClick={() => setShowList((v) => !v)}>
            {showList ? "Hide series" : "Show series"}
          </Button>
          <Button variant="outline" size="sm" asChild>
            <Link to={`/admin/tasks/${TASK_KEY}`}>History</Link>
          </Button>
          <Button
            size="sm"
            disabled={!task || running || runTask.isPending || !config?.enabled}
            onClick={() =>
              runTask.mutate(TASK_KEY, {
                onSettled: () => void client.invalidateQueries({ queryKey: QUERY_KEY }),
              })
            }
          >
            {runTask.isPending ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Play className="h-3.5 w-3.5" />
            )}
            {running ? "Running" : "Run now"}
          </Button>
        </div>
      </div>

      {showList && (
        <div className="py-4">
          <div className="mb-3 flex flex-wrap gap-1.5">
            {FILTERS.map((f) => (
              <Button
                key={f.value}
                size="sm"
                variant={filter === f.value ? "default" : "outline"}
                onClick={() => setFilter(f.value)}
              >
                {f.label}
              </Button>
            ))}
          </div>
          {visible.length === 0 ? (
            <p className="text-muted-foreground text-sm">No series here.</p>
          ) : (
            <ul className="divide-border max-h-[28rem] divide-y overflow-y-auto">
              {visible.map((s) => (
                <li key={s.id} className="flex items-center gap-3 py-2">
                  <div className="min-w-0 flex-1">
                    <Link to={`/item/${s.id}`} className="block truncate text-sm hover:underline">
                      {s.title}
                    </Link>
                    <p className="text-muted-foreground truncate text-xs">
                      {STATE_LABELS[s.state]}
                      {s.tvdb_id ? "" : " · no TheTVDB id"}
                      {s.detail ? ` · ${s.detail}` : ""}
                      {s.downloaded_at
                        ? ` · ${formatDateTime(s.downloaded_at)}`
                        : s.checked_at
                          ? ` · checked ${formatDateTime(s.checked_at)}`
                          : ""}
                    </p>
                  </div>
                  {s.state !== "folder" && (
                    <Button
                      variant="ghost"
                      size="icon"
                      title="Download now"
                      aria-label={`Download the theme for ${s.title} now`}
                      disabled={seriesAction.isPending}
                      onClick={() => seriesAction.mutate({ id: s.id, action: "retry" })}
                    >
                      <RotateCcw className="h-4 w-4" />
                    </Button>
                  )}
                  {s.state === "downloaded" && (
                    <Button
                      variant="ghost"
                      size="icon"
                      title="Delete download"
                      aria-label={`Delete the downloaded theme for ${s.title}`}
                      disabled={seriesAction.isPending}
                      onClick={() => seriesAction.mutate({ id: s.id, action: "delete" })}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </FieldGroup>
  );
}
