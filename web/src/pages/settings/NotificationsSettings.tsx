import { captureProfileRequestContext, isCapturedProfileAuthorityActive } from "@/api/client";
import {
  notificationScope,
  requireNotificationAuthority,
  captureNotificationAuthority,
} from "@/api/v2/notifications";
import { useState } from "react";
import { Link } from "react-router";
import {
  AlertTriangle,
  KeyRound,
  Loader2,
  Pencil,
  Plus,
  Send,
  Trash2,
  Webhook as WebhookIcon,
} from "lucide-react";
import { toast } from "sonner";
import { PageHeader } from "@/components/page/PageHeader";
import { INVALID_EMAIL_MESSAGE, isValidEmail } from "@/lib/email";
import type {
  NotificationEmailPreferences,
  NotificationPreferences,
  NotificationWebhook,
  NotificationWebhookInput,
  NotificationWebhookTestResult,
  NotificationEmailPreferencesUpdate,
} from "@/api/types";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { SigningSecretDialog } from "@/components/SigningSecretDialog";
import { SettingsGroup } from "@/components/settings/SettingsGroup";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import {
  useClearEmailNotificationAddress,
  useEmailNotificationPreferences,
  useNotificationPreferences,
  useRequestEmailNotificationAddress,
  useUpdateEmailNotificationPreferences,
  useUpdateNotificationPreferences,
} from "@/hooks/queries/notifications";
import {
  useCreateNotificationWebhook,
  useDeleteNotificationWebhook,
  useNotificationCapability,
  useNotificationWebhooks,
  useRotateNotificationWebhookSecret,
  useTestNotificationWebhook,
  useUpdateNotificationWebhook,
} from "@/hooks/queries/notificationWebhooks";
import { useAvailableUserLibraries } from "@/hooks/queries/libraries";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { formatRelativeTime } from "@/lib/date";

const REASON_FIELDS = [
  { key: "notify_favorites", label: "Favorites" },
  { key: "notify_watchlist", label: "Watchlist" },
  { key: "notify_continue_watching", label: "Continue Watching" },
  { key: "notify_next_up", label: "Next Up" },
] as const;

// Webhooks additionally carry the request.fulfilled toggle; it is not an
// episode reason, so the profile preferences section keeps REASON_FIELDS.
const WEBHOOK_NOTIFY_FIELDS = [
  ...REASON_FIELDS,
  { key: "notify_new_releases", label: "New releases" },
  { key: "notify_requests", label: "Requests" },
] as const;

// "Everything new" preferences: deliveries that need no series relationship.
const NEW_RELEASE_FIELDS = [
  {
    key: "notify_new_episodes",
    label: "Every new episode",
    hint: "Any show in your libraries, followed or not",
  },
  { key: "notify_new_movies", label: "Every new movie", hint: "Any movie added to your libraries" },
] as const;

const WEEKDAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

type WebhookNotifyKey = (typeof WEBHOOK_NOTIFY_FIELDS)[number]["key"];

function PreferencesSection() {
  const { data: prefs, isLoading } = useNotificationPreferences();
  const updatePrefs = useUpdateNotificationPreferences();
  const { data: capability } = useNotificationCapability();

  if (isLoading || !prefs) {
    return (
      <SettingsGroup title="New Release Notifications">
        <Skeleton className="h-24 w-full" />
      </SettingsGroup>
    );
  }

  return (
    <SettingsGroup
      title="New Release Notifications"
      description={
        <>
          Choose which series relationships notify this profile when a new episode arrives, or ask
          to hear about everything new.
          {capability?.series?.available && (
            <>
              {" "}
              <Link
                to="/notifications?view=series"
                className="text-foreground underline underline-offset-4"
              >
                See which series notify you
              </Link>
            </>
          )}
        </>
      }
    >
      <div className="flex items-center justify-between gap-3">
        <div>
          <div className="text-sm font-medium">Enable notifications</div>
          <div className="text-muted-foreground text-xs">Master switch for this profile</div>
        </div>
        <Switch
          checked={prefs.enabled}
          onCheckedChange={(checked) => updatePrefs.mutate({ enabled: checked })}
        />
      </div>
      {REASON_FIELDS.map((field) => (
        <div key={field.key} className="flex items-center justify-between gap-3">
          <div className="text-sm">{field.label}</div>
          <Switch
            checked={prefs[field.key as keyof NotificationPreferences] as boolean}
            disabled={!prefs.enabled}
            onCheckedChange={(checked) => updatePrefs.mutate({ [field.key]: checked })}
          />
        </div>
      ))}
      <div className="pt-2 text-sm font-medium">Everything new</div>
      {NEW_RELEASE_FIELDS.map((field) => (
        <div key={field.key} className="flex items-center justify-between gap-3">
          <div>
            <div className="text-sm">{field.label}</div>
            <div className="text-muted-foreground text-xs">{field.hint}</div>
          </div>
          <Switch
            checked={Boolean(prefs[field.key as keyof NotificationPreferences])}
            disabled={!prefs.enabled}
            onCheckedChange={(checked) => updatePrefs.mutate({ [field.key]: checked })}
          />
        </div>
      ))}
    </SettingsGroup>
  );
}

/**
 * Destination address for this profile's emails. There is no account-email
 * fallback: the profile receives nothing until an address is verified here.
 * Changing it queues a verification request for the new address; the old address
 * keeps receiving mail until the link is clicked. Removing the address also
 * turns the channel off. Child profiles cannot set addresses.
 */
function EmailDestinationRow({ prefs }: { prefs: NotificationEmailPreferences }) {
  const [editing, setEditing] = useState(false);
  const [address, setAddress] = useState("");
  const requestAddress = useRequestEmailNotificationAddress();
  const clearAddress = useClearEmailNotificationAddress();

  const hasAddress = prefs.custom_email !== "";

  const submit = () => {
    const trimmed = address.trim();
    if (!trimmed || requestAddress.isPending) {
      return;
    }
    if (!isValidEmail(trimmed)) {
      toast.error(INVALID_EMAIL_MESSAGE);
      return;
    }
    requestAddress.mutate(trimmed, {
      onSuccess: () => {
        setEditing(false);
        setAddress("");
      },
    });
  };

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-3">
        <div>
          <div className="text-sm">Deliver to</div>
          <div className="text-muted-foreground text-xs">
            {hasAddress ? prefs.custom_email : "No address set — verify one to receive emails"}
          </div>
        </div>
        {prefs.can_edit_address && (
          <div className="flex items-center gap-2">
            {hasAddress && (
              <Button
                variant="ghost"
                size="sm"
                disabled={clearAddress.isPending || requestAddress.isPending}
                onClick={() => clearAddress.mutate()}
              >
                Remove
              </Button>
            )}
            <Button
              variant="outline"
              size="sm"
              disabled={requestAddress.isPending}
              onClick={() => setEditing((value) => !value)}
            >
              {editing ? "Cancel" : hasAddress ? "Change" : "Add address"}
            </Button>
          </div>
        )}
      </div>
      {editing && (
        <div className="flex items-center gap-2">
          <Input
            type="email"
            disabled={requestAddress.isPending}
            placeholder="name@example.com"
            value={address}
            onChange={(event) => setAddress(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                submit();
              }
            }}
            className="max-w-xs"
          />
          <Button size="sm" disabled={requestAddress.isPending || !address.trim()} onClick={submit}>
            {requestAddress.isPending && <Loader2 className="mr-1 h-3 w-3 animate-spin" />}
            Request verification
          </Button>
        </div>
      )}
      {prefs.pending_email !== "" && (
        <div className="text-xs text-amber-500">
          Verification pending for {prefs.pending_email}. Open the verification link when the email
          arrives to activate the address.
        </div>
      )}
      {!prefs.can_edit_address && (
        <div className="text-muted-foreground text-xs">
          Child profiles can't receive email notifications.
        </div>
      )}
    </div>
  );
}

function Pill({
  on,
  label,
  disabled,
  onClick,
}: {
  on: boolean;
  label: string;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      aria-pressed={on}
      disabled={disabled}
      onClick={onClick}
      className={`rounded-full border px-3 py-1 text-sm transition-colors disabled:opacity-50 ${
        on
          ? "border-primary bg-primary text-primary-foreground"
          : "border-border text-muted-foreground hover:text-foreground"
      }`}
    >
      {label}
    </button>
  );
}

/** Movie and TV libraries this profile can open, for the library pills. */
function useNotificationLibraries() {
  const { data } = useAvailableUserLibraries({ includeHidden: true });
  return (data ?? []).filter((l) => l.type === "movies" || l.type === "series");
}

/**
 * Library pills: none picked means every library. Picking narrows it;
 * "All libraries" clears the choice.
 */
function LibraryPills({
  selected,
  disabled,
  onChange,
}: {
  selected: number[];
  disabled?: boolean;
  onChange: (ids: number[]) => void;
}) {
  const libraries = useNotificationLibraries();
  if (libraries.length === 0) return null;
  return (
    <div className="space-y-2">
      <div>
        <div className="text-sm">Libraries</div>
        <div className="text-muted-foreground text-xs">Only new items from these libraries</div>
      </div>
      <div className="flex flex-wrap gap-2">
        <Pill
          on={selected.length === 0}
          label="All libraries"
          disabled={disabled}
          onClick={() => onChange([])}
        />
        {libraries.map((library) => {
          const on = selected.includes(library.id);
          return (
            <Pill
              key={library.id}
              on={on}
              label={library.name}
              disabled={disabled}
              onClick={() =>
                onChange(
                  on ? selected.filter((id) => id !== library.id) : [...selected, library.id],
                )
              }
            />
          );
        })}
      </div>
    </div>
  );
}

/** Which delivery systems the administrator has switched on. */
function NotificationStatus() {
  const capability = useNotificationCapability();
  const caps = capability.data;
  if (!caps) return null;
  const email = caps.email?.available ?? false;
  const push = (caps.android_push?.available ?? false) || (caps.apple_push?.available ?? false);
  const item = (label: string, on: boolean) => (
    <div className="flex items-center gap-2">
      <span
        className={`h-2.5 w-2.5 rounded-full ${on ? "bg-emerald-500" : "bg-muted-foreground/40"}`}
        aria-hidden
      />
      <span className="text-sm font-medium">{label}</span>
      <span className={`text-sm ${on ? "text-emerald-500" : "text-muted-foreground"}`}>
        {on ? "Active" : "Inactive"}
      </span>
    </div>
  );
  return (
    <div className="border-border bg-card rounded-xl border p-4">
      <div className="flex flex-wrap gap-x-8 gap-y-2">
        {item("Email", email)}
        {item("Push", push)}
      </div>
      {(!email || !push) && (
        <p className="text-muted-foreground mt-2 text-xs">
          Inactive systems are switched off by the server administrator, so their settings below are
          hidden.
        </p>
      )}
    </div>
  );
}

function EmailSection() {
  const capability = useNotificationCapability();
  const emailCap = capability.data?.email;
  const available = emailCap?.available ?? false;
  const { data: prefs, isLoading } = useEmailNotificationPreferences(available);
  const updatePrefs = useUpdateEmailNotificationPreferences();

  if (!available) {
    return null;
  }
  if (isLoading || !prefs) {
    return (
      <SettingsGroup title="Email Notifications">
        <Skeleton className="h-16 w-full" />
      </SettingsGroup>
    );
  }

  const allowInstant = emailCap?.modes.includes("per_episode") ?? false;
  const digestHour = String(emailCap?.digest_hour ?? 8).padStart(2, "0");
  const weekday = WEEKDAYS[emailCap?.digest_weekday ?? 0] ?? "Sunday";
  const hasAddress = prefs.custom_email !== "";
  const pending = updatePrefs.isPending || !hasAddress;
  const save = (update: Partial<NotificationEmailPreferencesUpdate>) =>
    updatePrefs.mutate(update as NotificationEmailPreferencesUpdate);
  const anyOn = prefs.instant || prefs.daily || prefs.weekly;

  return (
    <SettingsGroup
      title="Email Notifications"
      description="Each profile verifies its own address and picks any mix of instant emails and digests."
    >
      <EmailDestinationRow prefs={prefs} />
      <div className="space-y-2">
        <div>
          <div className="text-sm">Send</div>
          <div className="text-muted-foreground text-xs">
            {!hasAddress
              ? "Add and verify an address to choose what to receive"
              : anyOn
                ? [
                    prefs.instant && "an email shortly after something new arrives",
                    prefs.daily && `a daily digest around ${digestHour}:00`,
                    prefs.weekly && `a weekly digest on ${weekday} around ${digestHour}:00`,
                  ]
                    .filter(Boolean)
                    .join(", ")
                    .replace(/^./, (c) => c.toUpperCase())
                : "Nothing: email is off for this profile"}
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          <Pill
            on={prefs.instant}
            label="Instant"
            disabled={pending || (!allowInstant && !prefs.instant)}
            onClick={() => save({ instant: !prefs.instant })}
          />
          <Pill
            on={prefs.daily}
            label="Daily digest"
            disabled={pending}
            onClick={() => save({ daily: !prefs.daily })}
          />
          <Pill
            on={prefs.weekly}
            label="Weekly digest"
            disabled={pending}
            onClick={() => save({ weekly: !prefs.weekly })}
          />
        </div>
        {!allowInstant && (
          <div className="text-muted-foreground text-xs">
            Instant emails are turned off by the administrator.
          </div>
        )}
      </div>
      {anyOn && (
        <>
          <LibraryPills
            selected={prefs.library_ids ?? []}
            disabled={updatePrefs.isPending}
            onChange={(ids) => save({ library_ids: ids })}
          />
          <div className="space-y-2">
            <div className="text-sm">What to send</div>
            <div className="flex flex-wrap gap-2">
              <Pill
                on={prefs.content !== "all"}
                label="Shows I follow"
                disabled={updatePrefs.isPending}
                onClick={() => save({ content: "followed" })}
              />
              <Pill
                on={prefs.content === "all"}
                label="Everything new"
                disabled={updatePrefs.isPending}
                onClick={() => save({ content: "all" })}
              />
            </div>
          </div>
        </>
      )}
    </SettingsGroup>
  );
}

function PushSection() {
  const capability = useNotificationCapability();
  const caps = capability.data;
  const available =
    (caps?.android_push?.available ?? false) || (caps?.apple_push?.available ?? false);
  const { data: prefs } = useNotificationPreferences();
  const updatePrefs = useUpdateNotificationPreferences();
  if (!available || !prefs) return null;
  const on = prefs.push_enabled ?? true;
  return (
    <SettingsGroup
      title="Push Notifications"
      description="Instant alerts on the phones where you've signed in to the Silo app."
    >
      <div className="space-y-2">
        <div>
          <div className="text-sm">Instant notifications</div>
          <div className="text-muted-foreground text-xs">
            {on
              ? "A push shortly after something new arrives"
              : "Off: notifications stay in your inbox"}
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          <Pill
            on={on}
            label="On"
            disabled={updatePrefs.isPending || !prefs.enabled}
            onClick={() => updatePrefs.mutate({ push_enabled: true })}
          />
          <Pill
            on={!on}
            label="Off"
            disabled={updatePrefs.isPending || !prefs.enabled}
            onClick={() => updatePrefs.mutate({ push_enabled: false })}
          />
        </div>
      </div>
      {on && (
        <LibraryPills
          selected={prefs.push_library_ids ?? []}
          disabled={updatePrefs.isPending || !prefs.enabled}
          onChange={(ids) => updatePrefs.mutate({ push_library_ids: ids })}
        />
      )}
    </SettingsGroup>
  );
}

function WebhookFormDialog({
  open,
  onOpenChange,
  webhook,
  globalPrefs,
  onSecret,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  webhook: NotificationWebhook | null;
  globalPrefs: NotificationPreferences | undefined;
  onSecret: (secret: string) => void;
}) {
  const create = useCreateNotificationWebhook();
  const update = useUpdateNotificationWebhook();
  const [name, setName] = useState(webhook?.name ?? "");
  const [url, setUrl] = useState("");
  const [reasons, setReasons] = useState<Record<WebhookNotifyKey, boolean>>({
    notify_favorites: webhook?.notify_favorites ?? true,
    notify_watchlist: webhook?.notify_watchlist ?? true,
    notify_continue_watching: webhook?.notify_continue_watching ?? true,
    notify_next_up: webhook?.notify_next_up ?? true,
    notify_new_releases: webhook?.notify_new_releases ?? true,
    notify_requests: webhook?.notify_requests ?? true,
  });
  const pending = create.isPending || update.isPending;
  const editing = webhook != null;

  const submit = () => {
    const input: NotificationWebhookInput = { name: name.trim(), ...reasons };
    if (url.trim()) {
      input.url = url.trim();
    }
    if (editing) {
      update.mutate(
        { id: webhook.id, etag: webhook.etag ?? "", ...input },
        {
          onSuccess: () => onOpenChange(false),
        },
      );
      return;
    }
    if (!input.url) {
      toast.error("A webhook URL is required");
      return;
    }
    const authority = captureNotificationAuthority();
    create.mutate(input, {
      onSuccess: (created) => {
        try {
          requireNotificationAuthority(authority);
        } catch {
          return;
        }
        onOpenChange(false);
        toast.success(`Webhook "${created.name}" created`);
        if (created.signing_secret) {
          onSecret(created.signing_secret);
        }
      },
      onError: (error) => {
        try {
          requireNotificationAuthority(authority);
        } catch {
          return;
        }
        toast.error(error instanceof Error ? error.message : "Failed to create webhook");
      },
    });
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{editing ? `Edit "${webhook.name}"` : "Add webhook"}</DialogTitle>
          <DialogDescription>
            Discord webhook URLs render as native embeds. Any other HTTPS endpoint receives signed
            JSON.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="webhook-name">Name</Label>
            <Input
              id="webhook-name"
              value={name}
              maxLength={64}
              placeholder="Family Discord"
              onChange={(event) => setName(event.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="webhook-url">{editing ? "Replace URL (optional)" : "URL"}</Label>
            <Input
              id="webhook-url"
              value={url}
              placeholder={
                editing
                  ? `Currently pointing at ${webhook.url_host}`
                  : "https://discord.com/api/webhooks/…"
              }
              onChange={(event) => setUrl(event.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label>Send notifications for</Label>
            {WEBHOOK_NOTIFY_FIELDS.map((field) => {
              // Requests have no per-profile reason toggle; only the master
              // switch suppresses them.
              const globallyDisabled =
                globalPrefs != null &&
                (!globalPrefs.enabled ||
                  (field.key !== "notify_requests" &&
                    !(globalPrefs[field.key as keyof NotificationPreferences] as boolean)));
              return (
                <div key={field.key} className="flex items-center justify-between gap-3">
                  <div className="text-sm">
                    {field.label}
                    {globallyDisabled && (
                      <span className="text-muted-foreground ml-2 text-xs">
                        disabled in profile preferences
                      </span>
                    )}
                  </div>
                  <Switch
                    checked={reasons[field.key]}
                    disabled={globallyDisabled}
                    onCheckedChange={(checked) =>
                      setReasons((current) => ({ ...current, [field.key]: checked }))
                    }
                  />
                </div>
              );
            })}
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={pending || !name.trim()}>
            {pending && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
            {editing ? "Save" : "Create"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function WebhookCard({
  webhook,
  onEdit,
  onSecret,
}: {
  webhook: NotificationWebhook;
  onEdit: () => void;
  onSecret: (secret: string) => void;
}) {
  const update = useUpdateNotificationWebhook();
  const remove = useDeleteNotificationWebhook();
  const authority = captureProfileRequestContext();
  const test = useTestNotificationWebhook();
  const rotate = useRotateNotificationWebhookSecret();
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleteIntent, setDeleteIntent] = useState<{ id: string; etag: string } | null>(null);
  const [testResult, setTestResult] = useState<NotificationWebhookTestResult | null>(null);

  const lastSuccess = formatRelativeTime(webhook.last_success_at);
  const lastFailure = formatRelativeTime(webhook.last_failure_at);
  const failing =
    webhook.last_failure_at != null &&
    (webhook.last_success_at == null || webhook.last_failure_at > webhook.last_success_at);
  const enabledReasons = WEBHOOK_NOTIFY_FIELDS.filter(
    (field) => webhook[field.key as keyof NotificationWebhook] as boolean,
  ).map((field) => field.label);

  return (
    <div className="border-border/60 space-y-2 rounded-xl border p-4">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-medium">{webhook.name}</span>
        <Badge variant="secondary">{webhook.type}</Badge>
        <span className="text-muted-foreground text-xs">{webhook.url_host}</span>
        <div className="ml-auto flex items-center gap-1.5">
          <span className="text-muted-foreground text-xs">
            {webhook.enabled ? "Enabled" : "Disabled"}
          </span>
          <Switch
            checked={webhook.enabled}
            onCheckedChange={(checked) =>
              update.mutate({ id: webhook.id, etag: webhook.etag ?? "", enabled: checked })
            }
          />
        </div>
      </div>

      <div className="text-muted-foreground text-xs">
        {enabledReasons.length === WEBHOOK_NOTIFY_FIELDS.length
          ? "All reasons"
          : enabledReasons.length > 0
            ? enabledReasons.join(" · ")
            : "No reasons selected"}
      </div>

      {lastSuccess && !failing && (
        <div className="text-muted-foreground text-xs">Last success: {lastSuccess}</div>
      )}
      {failing && (
        <div className="flex items-start gap-1.5 text-xs text-amber-500">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <span>
            {webhook.disabled_reason
              ? `Disabled: ${webhook.disabled_reason}`
              : `Last failure${lastFailure ? ` ${lastFailure}` : ""}: ${
                  webhook.last_failure_message || `HTTP ${webhook.last_failure_status ?? "error"}`
                }. Check the destination URL.`}
          </span>
        </div>
      )}
      {testResult && (
        <div className={`text-xs ${testResult.ok ? "text-emerald-500" : "text-amber-500"}`}>
          Test {testResult.ok ? "succeeded" : "failed"}
          {testResult.http_status ? ` (HTTP ${testResult.http_status}` : " ("}
          {`${testResult.duration_ms}ms)`}
          {testResult.message ? ` — ${testResult.message}` : ""}
        </div>
      )}

      <div className="flex flex-wrap gap-1.5 pt-1">
        <Button
          variant="outline"
          size="sm"
          disabled={test.isPending}
          onClick={() =>
            test.mutate(webhook.id, {
              onSuccess: (result) => {
                if (authority && isCapturedProfileAuthorityActive(authority)) setTestResult(result);
              },
              onError: () => {
                if (authority && isCapturedProfileAuthorityActive(authority))
                  toast.error("Test request failed");
              },
            })
          }
        >
          {test.isPending ? (
            <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
          ) : (
            <Send className="mr-1.5 h-3.5 w-3.5" />
          )}
          Test
        </Button>
        <Button variant="outline" size="sm" onClick={onEdit}>
          <Pencil className="mr-1.5 h-3.5 w-3.5" />
          Edit
        </Button>
        {webhook.type === "generic" && (
          <Button
            variant="outline"
            size="sm"
            disabled={rotate.isPending}
            onClick={() =>
              rotate.mutate(webhook.id, {
                onSuccess: (result) => onSecret(result.signing_secret),
              })
            }
          >
            <KeyRound className="mr-1.5 h-3.5 w-3.5" />
            Rotate secret
          </Button>
        )}
        <Button
          variant="outline"
          size="sm"
          className="text-destructive"
          onClick={() => {
            setDeleteIntent({ id: webhook.id, etag: webhook.etag ?? "" });
            setConfirmDelete(true);
          }}
        >
          <Trash2 className="mr-1.5 h-3.5 w-3.5" />
          Delete
        </Button>
      </div>

      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={`Delete "${webhook.name}"?`}
        description="Notifications will stop posting to this destination. This cannot be undone."
        confirmLabel="Delete"
        variant="destructive"
        isPending={remove.isPending}
        onConfirm={() => {
          if (deleteIntent)
            remove.mutate(deleteIntent, { onSettled: () => setConfirmDelete(false) });
        }}
      />
      {/* The edit dialog is hosted by the parent so state resets per webhook. */}
      {update.isPending && <span className="sr-only">Saving…</span>}
    </div>
  );
}

function WebhooksSection() {
  const capability = useNotificationCapability();
  const webhooksAvailable = capability.data?.webhooks.available ?? false;
  const { data: webhooks, isLoading } = useNotificationWebhooks(webhooksAvailable);
  const { data: globalPrefs } = useNotificationPreferences();
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<NotificationWebhook | null>(null);
  const [secret, setSecret] = useState<string | null>(null);

  const maxPerProfile = capability.data?.webhooks.max_per_profile ?? 10;
  const atLimit = (webhooks?.length ?? 0) >= maxPerProfile;

  if (!webhooksAvailable) {
    return null;
  }

  return (
    <>
      <SettingsGroup
        title="Webhooks"
        description="Send this profile's notifications to a webhook URL. Discord URLs render as native embeds; other URLs receive signed JSON."
      >
        {isLoading ? (
          <Skeleton className="h-24 w-full" />
        ) : (
          <>
            {(webhooks ?? []).map((webhook) => (
              <WebhookCard
                key={`${notificationScope()}:${webhook.id}`}
                webhook={webhook}
                onSecret={setSecret}
                onEdit={() => {
                  setEditing(webhook);
                  setFormOpen(true);
                }}
              />
            ))}
            {(webhooks ?? []).length === 0 && (
              <div className="text-muted-foreground flex items-center gap-2 text-sm">
                <WebhookIcon className="h-4 w-4" />
                No webhooks yet.
              </div>
            )}
            <div>
              <Button
                variant="outline"
                size="sm"
                disabled={atLimit}
                onClick={() => {
                  setEditing(null);
                  setFormOpen(true);
                }}
              >
                <Plus className="mr-1.5 h-4 w-4" />
                Add webhook
              </Button>
              {atLimit && (
                <span className="text-muted-foreground ml-2 text-xs">
                  Limit of {maxPerProfile} webhooks reached
                </span>
              )}
            </div>
          </>
        )}
      </SettingsGroup>

      {formOpen && (
        <WebhookFormDialog
          key={`${notificationScope()}:${editing?.id ?? "new"}`}
          open={formOpen}
          onOpenChange={(open) => {
            setFormOpen(open);
            if (!open) {
              setEditing(null);
            }
          }}
          webhook={editing}
          globalPrefs={globalPrefs}
          onSecret={setSecret}
        />
      )}
      <SigningSecretDialog secret={secret} onClose={() => setSecret(null)} />
    </>
  );
}

export default function NotificationsSettings() {
  useDocumentTitle("Notification Settings");

  return (
    <div className="space-y-6">
      <PageHeader
        title="Notifications"
        subtitle="New episodes and movies by email, push, or webhook."
      />

      <NotificationStatus />

      <PreferencesSection />

      <EmailSection />

      <PushSection />

      <WebhooksSection />
    </div>
  );
}
