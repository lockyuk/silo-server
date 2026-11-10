import { Loader2 } from "lucide-react";

import { Label } from "@/components/ui/label";
import { useLibraryMediaStats } from "@/hooks/queries/admin/libraries";
import { cn } from "@/lib/utils";

const BANDS = [
  { key: "4k", label: "4K" },
  { key: "1080p", label: "1080p" },
  { key: "720p", label: "720p" },
  { key: "sd", label: "SD (below 720p)" },
  { key: "unknown", label: "Not probed yet" },
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
 * A library's video files: space on disk, then how many files and how much
 * space each resolution holds.
 */
export default function LibraryMediaStats({ libraryId }: { libraryId: number }) {
  const { data, isLoading, isError } = useLibraryMediaStats(libraryId);
  const bands = BANDS.map((band) => ({
    ...band,
    entry: data?.bands.find((b) => b.resolution === band.key),
  })).filter((band) => band.entry && band.entry.files > 0);

  return (
    <div className="space-y-1.5">
      <Label>Video files</Label>
      {isLoading && (
        <p className="text-muted-foreground flex items-center gap-2 text-xs">
          <Loader2 className="size-3 animate-spin" /> Counting files…
        </p>
      )}
      {isError && <p className="text-destructive text-xs">Could not load file statistics.</p>}
      {data && (
        <>
          <p className="text-muted-foreground text-xs">
            {data.total_files.toLocaleString()} files taking{" "}
            <span className="text-foreground font-medium">
              {formatBytes(data.total_size_bytes)}
            </span>{" "}
            on disk.
          </p>
          <div className="overflow-x-auto rounded-md border">
            <table className="w-full text-xs tabular-nums">
              <thead className="bg-muted/50 text-muted-foreground">
                <tr>
                  <th className="px-2.5 py-1.5 text-left font-medium">Resolution</th>
                  <th className="px-2.5 py-1.5 text-right font-medium">Files</th>
                  <th className="px-2.5 py-1.5 text-right font-medium">Size on disk</th>
                  <th className="px-2.5 py-1.5 text-right font-medium">Share</th>
                </tr>
              </thead>
              <tbody>
                {bands.map(({ key, label, entry }) => (
                  <tr key={key} className="border-t">
                    <td className="px-2.5 py-1.5 whitespace-nowrap">{label}</td>
                    <td className="px-2.5 py-1.5 text-right">{entry!.files.toLocaleString()}</td>
                    <td className="px-2.5 py-1.5 text-right">{formatBytes(entry!.size_bytes)}</td>
                    <td
                      className={cn(
                        "px-2.5 py-1.5 text-right",
                        data.total_files === 0 && "text-muted-foreground/50",
                      )}
                    >
                      {data.total_files > 0
                        ? `${Math.round((entry!.files / data.total_files) * 100)}%`
                        : "-"}
                    </td>
                  </tr>
                ))}
                <tr className="bg-muted/30 border-t font-medium">
                  <td className="px-2.5 py-1.5">All</td>
                  <td className="px-2.5 py-1.5 text-right">{data.total_files.toLocaleString()}</td>
                  <td className="px-2.5 py-1.5 text-right">{formatBytes(data.total_size_bytes)}</td>
                  <td className="px-2.5 py-1.5 text-right">100%</td>
                </tr>
              </tbody>
            </table>
          </div>
        </>
      )}
    </div>
  );
}
