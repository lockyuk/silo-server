import { Loader2 } from "lucide-react";

import { useLibraryMediaStats } from "@/hooks/queries/admin/libraries";

const BANDS = [
  { key: "4k", label: "4K" },
  { key: "1080p", label: "1080p" },
  { key: "720p", label: "720p" },
  { key: "sd", label: "SD" },
  { key: "unknown", label: "Unknown" },
] as const;

/** Bytes as a short decimal size (1 TB = 10^12 bytes), the way disks are sold. */
export function formatBytes(bytes: number): string {
  if (bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const i = Math.min(units.length - 1, Math.floor(Math.log10(bytes) / 3));
  const value = bytes / 1000 ** i;
  return `${value >= 100 || i === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[i]}`;
}

/**
 * A library's size on disk and how its video files split by resolution: one
 * line for the total and one per resolution with its file count and size.
 */
export default function LibraryMediaStats({ libraryId }: { libraryId: number }) {
  const { data, isLoading, isError } = useLibraryMediaStats(libraryId);

  if (isLoading) {
    return (
      <span className="text-muted-foreground flex items-center gap-1.5 text-xs">
        <Loader2 className="size-3 animate-spin" /> Counting…
      </span>
    );
  }
  if (isError || !data) {
    return <span className="text-destructive text-xs">Could not load</span>;
  }
  const bands = BANDS.map((band) => ({
    ...band,
    entry: data.bands.find((b) => b.resolution === band.key),
  })).filter((band) => band.entry && band.entry.files > 0);

  return (
    <div className="space-y-1 text-xs tabular-nums">
      <div className="text-sm font-medium">{formatBytes(data.total_size_bytes)}</div>
      <div className="text-muted-foreground">{data.total_files.toLocaleString()} files</div>
      <dl className="text-muted-foreground grid grid-cols-[auto_1fr] gap-x-3">
        {bands.map(({ key, label, entry }) => (
          <div key={key} className="contents">
            <dt>{label}</dt>
            <dd className="text-foreground">
              {entry!.files.toLocaleString()} · {formatBytes(entry!.size_bytes)}
            </dd>
          </div>
        ))}
      </dl>
    </div>
  );
}
