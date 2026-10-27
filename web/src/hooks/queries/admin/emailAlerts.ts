import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { v2, type V2Body, type V2Result } from "@/api/v2/request";

const EMAIL_ALERTS_KEY = ["admin", "email-alerts"] as const;

export type EmailAlertsOverview = V2Result<"GET /api/v2/admin/notifications/email-alerts">;
export type EmailAlertSettings =
  V2Body<"PUT /api/v2/admin/notifications/email-alerts/settings">["settings"];
export type EmailAlertRecipientInput =
  V2Body<"POST /api/v2/admin/notifications/email-alerts/recipients">;
export type EmailAlertRecipientStatus = EmailAlertsOverview["recipients"][number];
export type EmailAlertPreviewMode = "instant" | "daily" | "weekly";

/** Everything the email alerts page shows. */
export function useEmailAlerts() {
  return useQuery({
    queryKey: EMAIL_ALERTS_KEY,
    queryFn: () => v2("GET /api/v2/admin/notifications/email-alerts"),
    retry: false,
  });
}

function useInvalidatingMutation<TInput, TResult>(fn: (input: TInput) => Promise<TResult>) {
  const queryClient = useQueryClient();
  return useMutation({
    retry: false,
    mutationFn: fn,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: EMAIL_ALERTS_KEY });
    },
  });
}

export function useSaveEmailAlertSettings() {
  const queryClient = useQueryClient();
  return useMutation({
    retry: false,
    mutationFn: (settings: EmailAlertSettings) =>
      v2("PUT /api/v2/admin/notifications/email-alerts/settings", { body: { settings } }),
    onSuccess: (data) => queryClient.setQueryData(EMAIL_ALERTS_KEY, data),
  });
}

/** Renders unsaved settings; sends nothing. */
export function previewEmailAlert(
  settings: EmailAlertSettings,
  mode: EmailAlertPreviewMode,
  source: "latest" | "sample",
  signal?: AbortSignal,
) {
  return v2("POST /api/v2/admin/notifications/email-alerts/preview", {
    body: { settings, mode, source },
    signal,
  });
}

export function useSendEmailAlertTest() {
  return useInvalidatingMutation(
    (input: { email: string; mode: EmailAlertPreviewMode; settings?: EmailAlertSettings }) =>
      v2("POST /api/v2/admin/notifications/email-alerts/test", {
        body: input,
        retryAuthentication: false,
      }),
  );
}

export function useRunEmailAlerts() {
  return useInvalidatingMutation(() =>
    v2("POST /api/v2/admin/notifications/email-alerts/run", { retryAuthentication: false }),
  );
}

export function useSaveEmailAlertRecipient() {
  return useInvalidatingMutation(
    ({ key, recipient }: { key?: string; recipient: EmailAlertRecipientInput }) =>
      key
        ? v2("PUT /api/v2/admin/notifications/email-alerts/recipients/{key}", {
            path: { key },
            body: recipient,
          })
        : v2("POST /api/v2/admin/notifications/email-alerts/recipients", {
            body: recipient,
            retryAuthentication: false,
          }),
  );
}

export function useDeleteEmailAlertRecipient() {
  return useInvalidatingMutation((key: string) =>
    v2("DELETE /api/v2/admin/notifications/email-alerts/recipients/{key}", { path: { key } }),
  );
}

export function useRewindEmailAlertRecipient() {
  return useInvalidatingMutation(({ key, hours }: { key: string; hours: number }) =>
    v2("POST /api/v2/admin/notifications/email-alerts/recipients/{key}/rewind", {
      path: { key },
      body: { hours },
      retryAuthentication: false,
    }),
  );
}
