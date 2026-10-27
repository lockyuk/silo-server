import { useId, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { v2 } from "@/api/v2/request";
import { Input } from "@/components/ui/input";

import { FieldGroup } from "./FieldGroup";
import { SettingFieldRow } from "./SettingField";

const QUERY_KEY = ["admin", "filmography-settings"] as const;

/** Where person pages send titles the library lacks. */
export function FilmographySettings() {
  const client = useQueryClient();
  const inputId = useId();
  const { data } = useQuery({
    queryKey: QUERY_KEY,
    queryFn: () => v2("GET /api/v2/admin/filmography/settings"),
  });
  const [draft, setDraft] = useState<string | null>(null);
  const save = useMutation({
    mutationFn: (request_url: string) =>
      v2("PUT /api/v2/admin/filmography/settings", { body: { request_url } }),
    onSuccess: (next) => {
      client.setQueryData(QUERY_KEY, next);
      toast.success(next.request_url ? "Request server saved" : "Request links turned off");
    },
    onError: () => toast.error("Enter a full http:// or https:// address"),
  });

  return (
    <FieldGroup
      label="Filmography"
      description="Person pages list everything someone worked on. Titles your libraries don't have can link to your request server."
    >
      <SettingFieldRow
        label="Request server (Jellyseerr or Overseerr)"
        htmlFor={inputId}
        description="For example https://requests.example.com. Leave empty to hide request links. Use an address viewers can reach from their devices."
      >
        <Input
          id={inputId}
          type="url"
          placeholder="https://requests.example.com"
          className="w-full sm:w-80"
          value={draft ?? data?.request_url ?? ""}
          disabled={!data || save.isPending}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={() => {
            if (draft != null && draft.trim() !== (data?.request_url ?? "")) {
              save.mutate(draft.trim(), { onSettled: () => setDraft(null) });
            } else {
              setDraft(null);
            }
          }}
        />
      </SettingFieldRow>
    </FieldGroup>
  );
}
