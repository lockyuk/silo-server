import { useId, useMemo, useRef, useState } from "react";
import { MobilePushPrivacyDisclosure } from "@/components/notifications/MobilePushPrivacyDisclosure";
import { useQueryClient } from "@tanstack/react-query";
import {
  ChevronDown,
  Inbox,
  KeyRound,
  Loader2,
  Mail,
  RadioTower,
  TriangleAlert,
  Webhook,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { Link } from "react-router";
import { toast } from "sonner";
import {
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  StaleApiRequestContextError,
} from "@/api/client";
import { notificationScope } from "@/api/v2/notifications";
import {
  registerNotificationRelay,
  clearNotificationRelay,
  type NotificationRelayRegistration,
} from "@/api/v2/notificationRelay";
import { Button } from "@/components/ui/button";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { AdvancedSection } from "@/components/settings/AdvancedSection";
import { SettingsPageHeader } from "@/components/settings/SettingsPageHeader";
import { Checkbox } from "@/components/ui/checkbox";
import { SettingsSubheading } from "@/components/settings/SettingsSubheading";
import { adminKeys } from "@/hooks/queries/keys";
import { useRestartKeys } from "@/hooks/useRestartKeys";
import { useSettingsForm } from "@/hooks/useSettingsForm";
import { useReportUnsavedChanges } from "@/hooks/useUnsavedChanges";
import { emailReady } from "@/lib/emailReadiness";
import { cn } from "@/lib/utils";
import { FieldGroup } from "./FieldGroup";
import { SaveBar } from "@/components/SaveBar";
import { SettingField, SettingFieldStatus } from "./SettingField";
import EmailAlertsPanel from "./EmailAlertsPanel";

/**
 * Operator kill switches for the whole pipeline. Both default on and only
 * come off to stop a flood or chase a problem, so they sit under Advanced.
 */
const PIPELINE_KEYS = ["notifications.release_events_enabled", "notifications.fanout_enabled"];

/** Batching and flood control; all advanced. */
const FANOUT_KEYS = [
  "notifications.fanout.settle_seconds",
  "notifications.fanout.max_series_burst",
  "notifications.fanout.max_event_age_hours",
];

/** Inbox and event cleanup; all advanced. */
const RETENTION_KEYS = [
  "notifications.retention.read_days",
  "notifications.retention.unread_days",
  "notifications.retention.event_days",
];

/** The page's one foot-of-page Advanced area. */
const PROCESSING_KEYS = [...PIPELINE_KEYS, ...FANOUT_KEYS, ...RETENTION_KEYS];

/** Personal webhook limits and the SSRF escape hatch; all advanced. */
const WEBHOOK_ADVANCED_KEYS = [
  "notifications.webhooks.max_per_profile",
  "notifications.webhooks.deliveries_per_minute_per_profile",
  "notifications.webhooks.allow_private_destinations",
];

const PUSH_DELIVERY_KEYS = [
  "notifications.apple_push_delivery_enabled",
  "notifications.android_push_delivery_enabled",
];

/**
 * Rows in the relay card's Advanced section: the per-platform switches (the
 * card's own switch flips both), the relay URL, and the deployment ID. Only the
 * switches go through the settings form.
 */
const PUSH_RELAY_ADVANCED_KEYS = [
  ...PUSH_DELIVERY_KEYS,
  "notifications.push_relay_url",
  "notifications.push_relay_deployment_id",
];

const EMAIL_CHANNEL_ADVANCED_KEYS = [
  "notifications.email.digest_hour",
  "notifications.email.digest_weekday",
];

/**
 * Read for the Email channel's status line only. The mail server itself is
 * edited on the Email page, since invitations and password resets send
 * through it too.
 */
const MAIL_STATUS_KEYS = ["email.enabled", "email.smtp_host", "email.from_address"];

/**
 * The Discord application. Saved by its own card inside the Discord channel
 * (a bot token has to be testable before it is committed), but listed here so
 * one form owns every value this page reads.
 */
const DISCORD_APP_KEYS = ["discord.client_id", "discord.client_secret", "discord.bot_token"];

const KEYS = [
  ...PIPELINE_KEYS,
  "notifications.ui_enabled",
  "notifications.webhooks_enabled",
  "notifications.web_push_enabled",
  ...PUSH_DELIVERY_KEYS,
  // Relay lifecycle fields are read for status but are never edited through
  // the shared settings form; credential endpoints replace them atomically.
  "notifications.push_relay_url",
  "notifications.push_relay_deployment_id",
  "notifications.push_relay_expires_at",
  "notifications.push_relay_key_prefix",
  "notifications.push_relay_reregistration_required",
  ...FANOUT_KEYS,
  ...WEBHOOK_ADVANCED_KEYS,
  "notifications.email_enabled",
  "notifications.email.allow_per_episode",
  ...EMAIL_CHANNEL_ADVANCED_KEYS,
  ...RETENTION_KEYS,
  ...MAIL_STATUS_KEYS,
  ...DISCORD_APP_KEYS,
];

const DEFAULT_PUSH_RELAY_URL = "https://push.siloserver.org";

/** Small status pill shown next to a channel title while the card is collapsed. */
function Chip({
  tone = "neutral",
  children,
}: {
  tone?: "neutral" | "positive" | "warning";
  children: React.ReactNode;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium whitespace-nowrap",
        tone === "neutral" && "border-border/70 text-muted-foreground",
        tone === "positive" && "border-emerald-500/30 bg-emerald-500/10 text-emerald-500",
        tone === "warning" && "border-amber-500/30 bg-amber-500/10 text-amber-500",
      )}
    >
      {children}
    </span>
  );
}

/**
 * A labelled cluster inside an Advanced disclosure. One element per cluster
 * means the disclosure's child rule draws a single hairline between clusters,
 * with the rows keeping their own inside each.
 */
function AdvancedCluster({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <SettingsSubheading>{label}</SettingsSubheading>
      {children}
    </div>
  );
}

interface ChannelCardProps {
  icon: LucideIcon;
  title: string;
  description: string;
  enabled: boolean;
  onEnabledChange: (enabled: boolean) => void;
  chips?: React.ReactNode;
  children?: React.ReactNode;
}

/**
 * One delivery channel as a settings row: icon, title, status chips and the
 * enable switch, with the channel's setup in an expandable body below it.
 * Settings stay editable while the channel is off so admins can configure
 * before enabling.
 *
 * The body is the channel's main content (credentials, destinations, the
 * privacy disclosure), not an Advanced tier, so it is this card's own
 * expand/collapse; tuning inside it sits behind an `AdvancedSection`.
 */
function ChannelCard({
  icon: Icon,
  title,
  description,
  enabled,
  onEnabledChange,
  chips,
  children,
}: ChannelCardProps) {
  const [open, setOpen] = useState(false);
  const bodyId = useId();
  const expandable = children != null;

  const header = (
    <>
      <span
        className={cn(
          "flex h-10 w-10 shrink-0 items-center justify-center rounded-xl transition-colors",
          enabled ? "bg-primary/15 text-primary" : "bg-muted text-muted-foreground",
        )}
      >
        <Icon className="h-5 w-5" />
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="text-sm font-medium">{title}</span>
          {chips}
        </span>
        <span className="text-muted-foreground mt-0.5 block text-xs leading-relaxed">
          {description}
        </span>
      </span>
    </>
  );

  return (
    <div>
      <div className="flex items-center gap-3 py-3.5">
        {expandable ? (
          <button
            type="button"
            aria-expanded={open}
            aria-controls={bodyId}
            onClick={() => setOpen((current) => !current)}
            className="flex min-w-0 flex-1 cursor-pointer items-center gap-3 text-left"
          >
            {header}
            <ChevronDown
              className={cn(
                "text-muted-foreground h-4 w-4 shrink-0 transition-transform duration-200",
                open && "rotate-180",
              )}
            />
          </button>
        ) : (
          <div className="flex min-w-0 flex-1 items-center gap-3">{header}</div>
        )}
        <Switch
          checked={enabled}
          onCheckedChange={(value) => {
            onEnabledChange(value);
            // Enabling a channel usually means configuring it next.
            if (value && expandable) setOpen(true);
          }}
          aria-label={`Enable ${title}`}
        />
      </div>
      {expandable && open && (
        <div
          id={bodyId}
          className="animate-in fade-in-0 slide-in-from-top-1 pb-4 pl-[3.25rem] duration-200"
        >
          {children}
        </div>
      )}
    </div>
  );
}

function RegisterRelayRow({
  relayURL,
  deploymentID,
  keyPrefix,
  expiresAt,
  reregistrationRequired,
  urlEdited,
  onRegistered,
}: {
  relayURL: string;
  deploymentID: string;
  keyPrefix: string;
  expiresAt: string;
  reregistrationRequired: boolean;
  urlEdited: boolean;
  onRegistered: (submittedRelayURL: string) => void;
}) {
  const queryClient = useQueryClient();
  const authority = captureProfileRequestContext();
  const inFlight = useRef(false);
  const [pending, setPending] = useState(false);
  const [confirmClear, setConfirmClear] = useState(false);
  const [result, setResult] = useState<NotificationRelayRegistration | null>(null);

  const configured = deploymentID.trim() !== "";
  const actionLabel = reregistrationRequired
    ? "Re-register relay"
    : configured
      ? "Rotate credential"
      : "Register relay";
  const expiration = expiresAt ? new Date(expiresAt) : null;
  const expirationValid = expiration != null && !Number.isNaN(expiration.getTime());
  const renewalStatus = expirationValid
    ? expiration.getTime() <= Date.now()
      ? "Expired; Silo renews it before the next delivery."
      : `Expires ${expiration.toLocaleString()}; Silo renews automatically.`
    : configured
      ? "Expiration unknown; Silo refreshes the credential on its next renewal."
      : reregistrationRequired
        ? "No relay credential is registered."
        : "Not registered yet; Silo registers on its first push.";

  const registerRelay = async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    setPending(true);
    setResult(null);
    try {
      if (!authority) throw new StaleApiRequestContextError();
      const response = await registerNotificationRelay(relayURL, authority);
      setResult(response);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: adminKeys.serverSettings() }),
        queryClient.invalidateQueries({
          queryKey: [...adminKeys.serverSettings(), "sensitive-status"] as const,
        }),
      ]);
      if (!isCapturedProfileAuthorityActive(authority)) return;
      onRegistered(relayURL);
      toast.success("Push relay registered");
    } catch (error) {
      if (authority && isCapturedProfileAuthorityActive(authority))
        toast.error(error instanceof Error ? error.message : "Relay registration failed");
    } finally {
      inFlight.current = false;
      setPending(false);
    }
  };

  const clearRelay = async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    setPending(true);
    setResult(null);
    try {
      if (!authority) throw new StaleApiRequestContextError();
      await clearNotificationRelay(authority);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: adminKeys.serverSettings() }),
        queryClient.invalidateQueries({
          queryKey: [...adminKeys.serverSettings(), "sensitive-status"] as const,
        }),
      ]);
      if (!isCapturedProfileAuthorityActive(authority)) return;
      setConfirmClear(false);
      toast.success("Push relay credential cleared");
    } catch (error) {
      if (authority && isCapturedProfileAuthorityActive(authority))
        toast.error(error instanceof Error ? error.message : "Failed to clear relay credential");
    } finally {
      inFlight.current = false;
      setPending(false);
    }
  };

  return (
    <div className="space-y-3 py-3">
      <SettingField
        label="Deployment ID"
        description="Created for you when you register."
        type="text"
        value={deploymentID}
        onChange={() => {}}
        disabled
      />
      <div className="flex flex-wrap items-center gap-2 py-2">
        <Button variant="outline" size="sm" disabled={pending} onClick={() => void registerRelay()}>
          {pending ? (
            <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
          ) : (
            <KeyRound className="mr-1.5 h-3.5 w-3.5" />
          )}
          {actionLabel}
        </Button>
        {configured && (
          <Button
            variant="ghost"
            size="sm"
            disabled={pending}
            onClick={() => setConfirmClear(true)}
          >
            Clear credential
          </Button>
        )}
      </div>
      {reregistrationRequired && (
        <div className="text-xs text-amber-500">
          The relay credential was cleared, or the relay disabled this deployment. Re-register to
          create a new deployment. Other relay rejections recover automatically.
        </div>
      )}
      <div className="text-muted-foreground space-y-1 text-xs">
        {keyPrefix && <div>Credential: {keyPrefix}</div>}
        <div>{renewalStatus}</div>
      </div>
      {urlEdited && (
        <div className="text-muted-foreground text-xs">Register to apply the new relay URL.</div>
      )}
      {result && (
        <div className="text-xs text-emerald-500">
          Credential ready for {result.deployment_id}
          {result.key_prefix ? `, key ${result.key_prefix}` : ""}
          {result.relay_request_id ? `, relay ${result.relay_request_id}` : ""}
        </div>
      )}
      <AlertDialog open={confirmClear} onOpenChange={setConfirmClear}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Clear the push relay credential?</AlertDialogTitle>
            <AlertDialogDescription>
              Mobile push delivery stops until a relay is registered again.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={pending}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={pending}
              onClick={() => void clearRelay()}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {pending ? "Clearing..." : "Clear credential"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Discord application credentials
// ---------------------------------------------------------------------------

export default function NotificationsAdminSettings() {
  const form = useSettingsForm({ keys: useMemo(() => KEYS, []) });
  const restartKeys = useRestartKeys();
  // Local draft for the relay URL; null means "show the saved value".
  const [pushRelayURLDraft, setPushRelayURLDraft] = useState<string | null>(null);
  // The relay URL draft lives outside useSettingsForm (only registration
  // persists it), so the navigation guard and reload prompt only see it
  // through this report. Above the loading return: hooks must be unconditional.
  useReportUnsavedChanges(
    pushRelayURLDraft !== null &&
      pushRelayURLDraft !==
        (form.getValue("notifications.push_relay_url") || DEFAULT_PUSH_RELAY_URL),
  );

  if (form.isLoading) {
    return (
      <div className="space-y-6">
        <Skeleton className="h-7 w-44" />
        <div className="space-y-3">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-[72px] w-full rounded-2xl" />
          ))}
        </div>
      </div>
    );
  }

  // Without a settings snapshot every value reads as empty, which would show
  // the default-on toggles (mobile push included) as off. Do not render
  // controls against a guess.
  if (form.loadError || !form.loaded) {
    return (
      <div className="border-destructive/30 bg-destructive/5 rounded-2xl border px-4 py-3.5 text-sm">
        <p className="font-medium">Couldn't load notification settings</p>
        <p className="text-muted-foreground mt-0.5 text-xs">
          The values shown here would not reflect what the server is doing. Reload to try again.
        </p>
        <Button
          type="button"
          variant="outline"
          className="mt-3"
          onClick={() => window.location.reload()}
        >
          Reload
        </Button>
      </div>
    );
  }

  // Kill switches default to enabled when unset; the backend treats any
  // unrecognized value as the default, so an empty stored value means "on".
  const toggleValue = (key: string) => form.getValue(key) || "true";
  const isOn = (key: string) => toggleValue(key) === "true";
  const setToggle = (key: string) => (value: boolean) =>
    form.setValue(key, value ? "true" : "false");
  // Numeric settings fall back to their server-side defaults when unset;
  // surface the effective default instead of an empty input.
  const numberValue = (key: string, fallback: string) => form.getValue(key) || fallback;
  const needsRestart = (key: string) => restartKeys.has(key);
  const allRestart = (keys: string[]) => keys.every((key) => restartKeys.has(key));
  const anyDirty = (keys: string[]) => keys.some(form.isDirty);

  const releaseEventsOn = isOn("notifications.release_events_enabled");
  const fanoutOn = isOn("notifications.fanout_enabled");
  const uiOn = isOn("notifications.ui_enabled");
  const emailOn = isOn("notifications.email_enabled");
  // Mobile push defaults on (the effective snapshot carries the default);
  // Discord and personal webhooks are opt-in.
  const applePushOn = form.getValue("notifications.apple_push_delivery_enabled") === "true";
  const androidPushOn = form.getValue("notifications.android_push_delivery_enabled") === "true";
  const mobilePushOn = applePushOn || androidPushOn;
  const webhooksOn = form.getValue("notifications.webhooks_enabled") === "true";

  // The relay URL is not part of the settings form: the server only persists
  // it through the registration endpoint, alongside the credentials it mints.
  const savedPushRelayURL = form.getValue("notifications.push_relay_url") || DEFAULT_PUSH_RELAY_URL;
  const pushRelayURL = pushRelayURLDraft ?? savedPushRelayURL;
  const pushRelayURLEdited = pushRelayURL !== savedPushRelayURL;
  const pushRelayDeploymentID = form.getValue("notifications.push_relay_deployment_id");
  const pushRelayKeyPrefix = form.getValue("notifications.push_relay_key_prefix");
  const pushRelayExpiresAt = form.getValue("notifications.push_relay_expires_at");
  const pushRelayReregistrationRequired =
    form.getValue("notifications.push_relay_reregistration_required") === "true";
  const pushRelayAPIKeyReady = form.sensitiveConfigured.includes(
    "notifications.push_relay_api_key",
  );
  const allowPrivate =
    form.getValue("notifications.webhooks.allow_private_destinations") === "true";

  // Mail readiness mirrors the server's rule (shared with the Dashboard's
  // attention notice): the outbound switch, a server, AND a sender address —
  // legacy rows and single-key writes can store enabled-without-sender, which
  // cannot send.
  const mailReady = emailReady(
    form.getValue("email.enabled") === "true",
    form.getValue("email.smtp_host"),
    form.getValue("email.from_address"),
  );
  // The Discord application is configured in the Discord channel card below,
  // next to the delivery settings it gates. "Configured" mirrors the server's
  // own rule (DiscordConfigured): client id, client secret, AND bot token —
  // a partial save (say, only the bot token) must not read as connected.

  // The kill switches live under Advanced, so a pipeline someone switched off
  // has to say so up here: every channel below is idle while it is.
  const pausedMessage = !releaseEventsOn
    ? "New content is not being recorded, so nothing can be sent. Turn it back on under Processing."
    : !fanoutOn
      ? "Sending is paused; new content waits in the queue. Turn it back on under Processing."
      : null;

  return (
    <div className="flex h-full flex-col">
      <SettingsPageHeader title="Notifications" />

      <div className="flex-1 space-y-5">
        {pausedMessage && (
          <div
            role="status"
            className="flex items-start gap-2 rounded-2xl border border-amber-500/25 bg-amber-500/5 px-4 py-3 text-xs text-amber-600 dark:text-amber-400"
          >
            <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0" />
            <p>{pausedMessage}</p>
          </div>
        )}

        {/* ── Master switch: which notification systems run at all ── */}
        <FieldGroup
          label="Notification systems"
          description="Tick the systems that are active. An unticked system sends nothing and its settings are hidden from people's notification pages."
        >
          <div className="flex flex-wrap gap-6 py-3">
            <label className="flex items-center gap-2 text-sm font-medium">
              <Checkbox
                checked={emailOn}
                onCheckedChange={(v) =>
                  form.setValue("notifications.email_enabled", String(v === true))
                }
              />
              Email
            </label>
            <label className="flex items-center gap-2 text-sm font-medium">
              <Checkbox
                checked={mobilePushOn}
                onCheckedChange={(v) => {
                  form.setValue("notifications.apple_push_delivery_enabled", String(v === true));
                  form.setValue("notifications.android_push_delivery_enabled", String(v === true));
                }}
              />
              Push
            </label>
          </div>
        </FieldGroup>

        {/* ── Delivery channels ── */}
        <FieldGroup
          label="Delivery channels"
          description="Where notifications go. A channel's setup stays editable while it is off, so it can be ready before it is turned on."
        >
          <ChannelCard
            icon={Inbox}
            title="In-App"
            description="Notification inbox in the web, mobile, and TV apps."
            enabled={uiOn}
            onEnabledChange={setToggle("notifications.ui_enabled")}
          />

          <ChannelCard
            icon={RadioTower}
            title="Silo Push Relay"
            description="Mobile push through Silo's relay, delivered by APNs or FCM."
            enabled={mobilePushOn}
            onEnabledChange={(enabled) => {
              form.setValue("notifications.apple_push_delivery_enabled", String(enabled));
              form.setValue("notifications.android_push_delivery_enabled", String(enabled));
            }}
            chips={
              pushRelayReregistrationRequired ? (
                <Chip tone="warning">Re-registration required</Chip>
              ) : pushRelayAPIKeyReady ? (
                <Chip tone="positive">Relay configured</Chip>
              ) : (
                // A server that never registered does so on its first send
                // (internal/notifications/push_sender.go), so a fresh install
                // has nothing to fix here.
                <Chip>Registers on first send</Chip>
              )
            }
          >
            <div className="settings-field-list">
              <MobilePushPrivacyDisclosure />
              <AdvancedSection
                id="notifications.push-relay"
                count={PUSH_RELAY_ADVANCED_KEYS.length}
                forceOpen={
                  anyDirty(PUSH_DELIVERY_KEYS) ||
                  pushRelayURLEdited ||
                  pushRelayReregistrationRequired
                }
              >
                <SettingField
                  label="Apple Push (APNs)"
                  type="toggle"
                  value={String(applePushOn)}
                  onChange={(value) =>
                    form.setValue("notifications.apple_push_delivery_enabled", value)
                  }
                  restartRequired={needsRestart("notifications.apple_push_delivery_enabled")}
                />
                <SettingField
                  label="Android Push (FCM)"
                  type="toggle"
                  value={String(androidPushOn)}
                  onChange={(value) =>
                    form.setValue("notifications.android_push_delivery_enabled", value)
                  }
                  restartRequired={needsRestart("notifications.android_push_delivery_enabled")}
                />
                <SettingField
                  label="Relay URL"
                  description="Saved when you register below."
                  type="text"
                  value={pushRelayURL}
                  onChange={(v) => setPushRelayURLDraft(v)}
                  restartRequired={needsRestart("notifications.push_relay_url")}
                />
                <RegisterRelayRow
                  key={notificationScope()}
                  relayURL={pushRelayURL}
                  deploymentID={pushRelayDeploymentID}
                  keyPrefix={pushRelayKeyPrefix}
                  expiresAt={pushRelayExpiresAt}
                  reregistrationRequired={pushRelayReregistrationRequired}
                  urlEdited={pushRelayURLEdited}
                  onRegistered={(submittedRelayURL) =>
                    setPushRelayURLDraft((currentDraft) => {
                      const currentURL = currentDraft ?? savedPushRelayURL;
                      return currentURL === submittedRelayURL ? null : currentDraft;
                    })
                  }
                />
              </AdvancedSection>
            </div>
          </ChannelCard>

          <ChannelCard
            icon={Mail}
            title="Email"
            description="New episodes and movies by email, as they arrive or in digests."
            enabled={emailOn}
            onEnabledChange={setToggle("notifications.email_enabled")}
            chips={
              <>
                {/* Neutral either way: a server that runs without mail on
                    purpose is not broken, and the Dashboard lists what is. */}
                <Chip tone={mailReady ? "positive" : "neutral"}>
                  {mailReady ? "Mail server set up" : "Mail server not set up"}
                </Chip>
              </>
            }
          >
            <div className="settings-field-list">
              <div className="py-3.5">
                <SettingFieldStatus tone={mailReady ? "ok" : "muted"}>
                  <span>
                    {mailReady
                      ? "Mail server set up. Change it on the "
                      : "Mail server not set up. No email can send until it is; set it up on the "}
                    <Link
                      to="/admin/settings/email"
                      className="text-foreground font-medium underline underline-offset-2"
                    >
                      Email page
                    </Link>
                    .
                  </span>
                </SettingFieldStatus>
              </div>
              <SettingField
                label="Let people pick an email per episode"
                description="Off sends them the daily summary instead."
                type="toggle"
                value={toggleValue("notifications.email.allow_per_episode")}
                onChange={(v) => form.setValue("notifications.email.allow_per_episode", v)}
                restartRequired={needsRestart("notifications.email.allow_per_episode")}
              />
              <div className="py-3.5">
                <SettingFieldStatus tone="muted">
                  <span>
                    Templates, design, recipients and the digest schedule are in{" "}
                    <a
                      href="#email-alerts"
                      className="text-foreground font-medium underline underline-offset-2"
                    >
                      Email alerts
                    </a>{" "}
                    below.
                  </span>
                </SettingFieldStatus>
              </div>
            </div>
          </ChannelCard>

          <ChannelCard
            icon={Webhook}
            title="Personal Webhooks"
            description="Webhooks people create for themselves, Discord or generic."
            enabled={webhooksOn}
            onEnabledChange={setToggle("notifications.webhooks_enabled")}
            chips={
              allowPrivate ? <Chip tone="warning">Private destinations allowed</Chip> : undefined
            }
          >
            <AdvancedSection
              id="notifications.webhooks"
              count={WEBHOOK_ADVANCED_KEYS.length}
              forceOpen={anyDirty(WEBHOOK_ADVANCED_KEYS)}
            >
              <SettingField
                label="Webhooks per person"
                type="number"
                value={numberValue("notifications.webhooks.max_per_profile", "10")}
                onChange={(v) => form.setValue("notifications.webhooks.max_per_profile", v)}
                restartRequired={needsRestart("notifications.webhooks.max_per_profile")}
              />
              <SettingField
                label="Deliveries per person"
                // The limiter is in memory on each process
                // (internal/notifications/webhook_sender.go), so a cluster
                // allows this many per process, not per server.
                description="Counted by each server process on its own. Calls over the limit are dropped; the inbox notification still arrives."
                unit="per minute"
                type="number"
                value={numberValue(
                  "notifications.webhooks.deliveries_per_minute_per_profile",
                  "60",
                )}
                onChange={(v) =>
                  form.setValue("notifications.webhooks.deliveries_per_minute_per_profile", v)
                }
                restartRequired={needsRestart(
                  "notifications.webhooks.deliveries_per_minute_per_profile",
                )}
              />
              <SettingField
                label="Allow webhooks to private addresses"
                description="Allows LAN and localhost destinations; development only."
                type="toggle"
                value={form.getValue("notifications.webhooks.allow_private_destinations")}
                onChange={(v) =>
                  form.setValue("notifications.webhooks.allow_private_destinations", v)
                }
                restartRequired={needsRestart("notifications.webhooks.allow_private_destinations")}
              />
              {allowPrivate && (
                <div className="flex items-start gap-2 py-3 text-xs text-amber-500">
                  <TriangleAlert className="mt-0.5 h-4 w-4 flex-shrink-0" />
                  <p>
                    Any user with webhook access can make this server send requests to internal
                    network addresses.
                  </p>
                </div>
              )}
            </AdvancedSection>
          </ChannelCard>
        </FieldGroup>

        <section id="email-alerts" aria-label="Email alerts" className="scroll-mt-4">
          <EmailAlertsPanel />
        </section>

        {/* ── Processing: everything here has a working default ── */}
        <FieldGroup
          label="Processing"
          description="How Silo notices new content, groups it into messages, and how long it keeps them."
          restartAll={allRestart(PROCESSING_KEYS)}
          dirty={anyDirty(PROCESSING_KEYS)}
        >
          <AdvancedSection
            id="notifications.processing"
            count={PROCESSING_KEYS.length}
            // A switched-off pipeline is the one thing in here that stops every
            // channel, so it does not stay folded away.
            forceOpen={anyDirty(PROCESSING_KEYS) || pausedMessage !== null}
          >
            <AdvancedCluster label="Pipeline">
              <SettingField
                label="Notice new content"
                description="Record new items during library scans."
                type="toggle"
                value={toggleValue("notifications.release_events_enabled")}
                onChange={(v) => form.setValue("notifications.release_events_enabled", v)}
                restartRequired={needsRestart("notifications.release_events_enabled")}
              />
              <SettingField
                label="Work out who wants it"
                description="Match each item against everyone's preferences."
                type="toggle"
                value={toggleValue("notifications.fanout_enabled")}
                onChange={(v) => form.setValue("notifications.fanout_enabled", v)}
                restartRequired={needsRestart("notifications.fanout_enabled")}
              />
            </AdvancedCluster>
            <AdvancedCluster label="Grouping and flood control">
              <SettingField
                label="Settle window"
                description="Items that finish scanning together arrive as one notification."
                unit="seconds"
                type="number"
                value={numberValue("notifications.fanout.settle_seconds", "30")}
                onChange={(v) => form.setValue("notifications.fanout.settle_seconds", v)}
                restartRequired={needsRestart("notifications.fanout.settle_seconds")}
              />
              <SettingField
                label="Max messages per show"
                description="Anything past this is skipped for that batch."
                type="number"
                value={numberValue("notifications.fanout.max_series_burst", "3")}
                onChange={(v) => form.setValue("notifications.fanout.max_series_burst", v)}
                restartRequired={needsRestart("notifications.fanout.max_series_burst")}
              />
              <SettingField
                label="Max content age"
                description="Older items are dropped instead of arriving as stale news."
                unit="hours"
                type="number"
                value={numberValue("notifications.fanout.max_event_age_hours", "72")}
                onChange={(v) => form.setValue("notifications.fanout.max_event_age_hours", v)}
                restartRequired={needsRestart("notifications.fanout.max_event_age_hours")}
              />
            </AdvancedCluster>
            <AdvancedCluster label="Retention">
              <SettingField
                label="Read notifications"
                unit="days"
                type="number"
                value={numberValue("notifications.retention.read_days", "90")}
                onChange={(v) => form.setValue("notifications.retention.read_days", v)}
                restartRequired={needsRestart("notifications.retention.read_days")}
              />
              <SettingField
                label="Unread notifications"
                unit="days"
                type="number"
                value={numberValue("notifications.retention.unread_days", "180")}
                onChange={(v) => form.setValue("notifications.retention.unread_days", v)}
                restartRequired={needsRestart("notifications.retention.unread_days")}
              />
              <SettingField
                label="Sent history"
                description="Record of what Silo already notified about."
                unit="days"
                type="number"
                value={numberValue("notifications.retention.event_days", "30")}
                onChange={(v) => form.setValue("notifications.retention.event_days", v)}
                restartRequired={needsRestart("notifications.retention.event_days")}
              />
            </AdvancedCluster>
          </AdvancedSection>
        </FieldGroup>
      </div>

      <SaveBar
        dirtyCount={form.dirtyCount}
        onSave={form.save}
        onDiscard={form.discard}
        isSaving={form.isSaving}
      />
    </div>
  );
}
