import { useEffect, useMemo, useRef, useState } from "react";
import { Loader2, Mail, Plus, RotateCcw, Send, Trash2, TriangleAlert } from "lucide-react";
import { toast } from "sonner";

import { SettingsPageHeader } from "@/components/settings/SettingsPageHeader";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  previewEmailAlert,
  useDeleteEmailAlertRecipient,
  useEmailAlerts,
  useRewindEmailAlertRecipient,
  useRunEmailAlerts,
  useSaveEmailAlertRecipient,
  useSaveEmailAlertSettings,
  useSendEmailAlertTest,
  type EmailAlertPreviewMode,
  type EmailAlertRecipientInput,
  type EmailAlertRecipientStatus,
  type EmailAlertSettings,
  type EmailAlertsOverview,
} from "@/hooks/queries/admin/emailAlerts";

const WEEKDAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
const HOURS = Array.from({ length: 24 }, (_, h) => h);

const MODE_LABELS: Record<string, string> = {
  instant: "Instant",
  instant_daily: "Instant + daily digest",
  instant_weekly: "Instant + weekly digest",
  daily: "Daily digest",
  weekly: "Weekly digest",
};

const COLORS: [keyof EmailAlertSettings["theme"], string][] = [
  ["accent_color", "Accent (buttons, highlights)"],
  ["background_color", "Background"],
  ["card_color", "Cards"],
  ["text_color", "Text"],
  ["muted_color", "Secondary text"],
];

// The template variables, grouped as the email alerts plugin documented them.
const VARS: [string, string, string[]][] = [
  [
    "Anywhere",
    "Values for the whole email.",
    [
      "server_name",
      "server_url",
      "date",
      "short_date",
      "time",
      "period",
      "summary",
      "total_count",
      "movie_count",
      "episode_count",
      "show_count",
      "more_count",
      "recipient_name",
      "recipient_first_name",
      "recipient_email",
      "first_display_title",
      "first_title",
      "first_show_name",
      "first_movie_title",
      "manage_url",
      "unsubscribe_url",
    ],
  ],
  [
    "Theme",
    "Set on the Design tab.",
    [
      "header_title",
      "intro",
      "footer_text",
      "button_text",
      "logo_url",
      "accent_color",
      "background_color",
      "card_color",
      "text_color",
      "muted_color",
    ],
  ],
  [
    "Conditions",
    "Use {{#name}}…{{/name}} to show a block only when true, or {{^name}}…{{/name}} when false.",
    [
      "has_items",
      "has_shows",
      "has_movies",
      "has_movies_and_shows",
      "single_item",
      "has_more",
      "is_instant",
      "is_digest",
      "is_daily",
      "is_weekly",
      "is_test",
    ],
  ],
  [
    "Lists",
    "Repeat a block for each entry, for example {{#shows}}…{{/shows}}.",
    ["shows", "movies", "episodes", "items"],
  ],
  [
    "Inside {{#shows}}",
    "One entry per show, with its new episodes grouped.",
    [
      "show_name",
      "show_synopsis",
      "show_synopsis_full",
      "show_url",
      "show_web_url",
      "show_poster",
      "show_backdrop",
      "show_backdrop_bg",
      "show_year",
      "show_genres",
      "show_rating",
      "show_status",
      "show_content_rating",
      "show_tagline",
      "network",
      "season_count",
      "cast",
      "library_name",
      "episode_count",
      "episodes_label",
      "episode_range",
      "single_episode",
      "multiple_episodes",
      "first_episode_name",
      "first_episode_code",
      "first_episode_synopsis",
      "first_episode_image",
      "first_url",
      "episodes",
    ],
  ],
  [
    "Inside {{#episodes}}",
    "Each episode. Show values are available here too.",
    [
      "episode_name",
      "episode_code",
      "season_number",
      "episode_number",
      "season_label",
      "episode_synopsis",
      "episode_synopsis_full",
      "episode_image",
      "has_episode_image",
      "air_date",
      "runtime",
      "url",
      "display_title",
      "show_name",
      "show_synopsis",
      "show_poster",
      "network",
      "library_name",
      "added_date",
      "is_first",
      "is_last",
    ],
  ],
  [
    "Inside {{#movies}}",
    "Each new movie.",
    [
      "title",
      "year",
      "display_title",
      "synopsis",
      "synopsis_full",
      "tagline",
      "genres",
      "rating",
      "imdb_rating",
      "content_rating",
      "runtime",
      "runtime_minutes",
      "release_date",
      "poster",
      "backdrop",
      "backdrop_bg",
      "has_poster",
      "cast",
      "studios",
      "url",
      "web_url",
      "library_name",
      "added_date",
    ],
  ],
  [
    "Inside {{#items}}",
    "Every new item in one list. Check {{#is_movie}} or {{#is_episode}}.",
    [
      "is_movie",
      "is_episode",
      "type",
      "title",
      "display_title",
      "synopsis",
      "poster",
      "backdrop",
      "url",
    ],
  ],
];
const LIST_VARS = new Set(["shows", "movies", "episodes", "items"]);
const CONDITION_VARS = new Set([
  ...(VARS[2]?.[2] ?? []),
  "single_episode",
  "multiple_episodes",
  "has_poster",
  "has_episode_image",
  "is_first",
  "is_last",
  "is_movie",
  "is_episode",
]);

const fieldClass =
  "border-input bg-background focus-visible:ring-ring h-9 w-full rounded-md border px-3 text-sm focus-visible:ring-2 focus-visible:outline-none";

function formatTime(value?: string | null) {
  if (!value) return "–";
  return new Date(value).toLocaleString(undefined, {
    weekday: "short",
    day: "numeric",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function Panel({
  title,
  description,
  children,
  className = "",
}: {
  title?: string;
  description?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <section className={`border-border bg-card rounded-xl border p-5 ${className}`}>
      {title && <h3 className="mb-1 text-base font-semibold">{title}</h3>}
      {description && <p className="text-muted-foreground mb-4 text-sm">{description}</p>}
      {children}
    </section>
  );
}

function Field({
  label,
  hint,
  children,
  htmlFor,
}: {
  label: string;
  hint?: React.ReactNode;
  children: React.ReactNode;
  htmlFor?: string;
}) {
  return (
    <div className="mb-4 space-y-1.5">
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
      {hint && <p className="text-muted-foreground text-xs">{hint}</p>}
    </div>
  );
}

function ToggleRow({
  label,
  hint,
  checked,
  onChange,
}: {
  label: string;
  hint?: string;
  checked: boolean;
  onChange: (value: boolean) => void;
}) {
  return (
    <div className="mb-3 flex items-start justify-between gap-4">
      <div>
        <div className="text-sm">{label}</div>
        {hint && <div className="text-muted-foreground text-xs">{hint}</div>}
      </div>
      <Switch checked={checked} onCheckedChange={onChange} aria-label={label} />
    </div>
  );
}

function NumberInput({
  value,
  onChange,
  min,
  max,
  id,
}: {
  value: number;
  onChange: (value: number) => void;
  min?: number;
  max?: number;
  id?: string;
}) {
  return (
    <Input
      id={id}
      type="number"
      min={min}
      max={max}
      value={Number.isFinite(value) ? value : 0}
      onChange={(e) => onChange(Number(e.target.value))}
      className="max-w-40"
    />
  );
}

/**
 * The new-media emails: templates with a live preview, design, recipients,
 * schedule and the send log. A port of the email alerts plugin's admin page.
 */
export default function EmailAlertsSettings() {
  const { data, isLoading, error } = useEmailAlerts();
  const save = useSaveEmailAlertSettings();
  const [draft, setDraft] = useState<EmailAlertSettings | null>(null);
  const [tab, setTab] = useState("overview");

  // The draft starts from the saved settings and is replaced whenever a save
  // returns them.
  const saved = data?.settings as EmailAlertSettings | undefined;
  const [draftOf, setDraftOf] = useState<EmailAlertSettings | undefined>(undefined);
  if (saved !== draftOf) {
    setDraftOf(saved);
    setDraft(saved ? structuredClone(saved) : null);
  }

  const dirty = useMemo(
    () => !!draft && !!saved && JSON.stringify(draft) !== JSON.stringify(saved),
    [draft, saved],
  );

  if (isLoading || (!draft && !error)) {
    return (
      <div className="space-y-4" role="status" aria-label="Loading email alerts">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-40 w-full" />
      </div>
    );
  }
  if (error || !data || !draft) {
    return (
      <div className="flex h-full flex-col">
        <SettingsPageHeader title="Email alerts" />
        <p className="text-muted-foreground text-sm">
          Email alerts aren't available: {(error as Error | null)?.message ?? "no mail sender"}.
        </p>
      </div>
    );
  }

  const update = (fn: (s: EmailAlertSettings) => void) =>
    setDraft((prev) => {
      if (!prev) return prev;
      const next = structuredClone(prev);
      fn(next);
      return next;
    });

  const onSave = () =>
    save.mutate(draft, {
      onSuccess: () => toast.success("Email alert settings saved"),
      onError: (e) => toast.error((e as Error).message),
    });

  return (
    <div className="flex h-full flex-col">
      <SettingsPageHeader
        title="Email alerts"
        actions={
          <div className="flex items-center gap-3">
            {dirty && <span className="text-muted-foreground text-sm">Unsaved changes</span>}
            {dirty && (
              <Button variant="ghost" size="sm" onClick={() => setDraft(structuredClone(saved!))}>
                Discard
              </Button>
            )}
            <Button size="sm" disabled={!dirty || save.isPending} onClick={onSave}>
              {save.isPending && <Loader2 className="h-4 w-4 animate-spin" />}
              Save changes
            </Button>
          </div>
        }
      />
      <p className="text-muted-foreground mb-4 text-sm">
        New episodes and movies by email, as they arrive or in daily and weekly digests.{" "}
        {data.server_name} links to {data.public_url || "no public address yet"}.
      </p>
      {data.problems.length > 0 && (
        <div className="mb-4 space-y-1 rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-sm">
          {data.problems.map((p) => (
            <div key={p} className="flex items-start gap-2">
              <TriangleAlert className="mt-0.5 h-4 w-4 flex-shrink-0 text-amber-500" />
              {p}
            </div>
          ))}
        </div>
      )}

      <Tabs value={tab} onValueChange={setTab} className="flex-1">
        <TabsList className="mb-4 flex-wrap">
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="templates">Templates</TabsTrigger>
          <TabsTrigger value="design">Design</TabsTrigger>
          <TabsTrigger value="recipients">Recipients</TabsTrigger>
          <TabsTrigger value="schedule">Schedule &amp; sign-up</TabsTrigger>
          <TabsTrigger value="log">Send log</TabsTrigger>
        </TabsList>
        <TabsContent value="overview">
          <OverviewTab data={data} draft={draft} />
        </TabsContent>
        <TabsContent value="templates">
          <TemplatesTab
            draft={draft}
            defaults={data.defaults as EmailAlertSettings}
            update={update}
          />
        </TabsContent>
        <TabsContent value="design">
          <DesignTab draft={draft} update={update} />
        </TabsContent>
        <TabsContent value="recipients">
          <RecipientsTab data={data} />
        </TabsContent>
        <TabsContent value="schedule">
          <ScheduleTab draft={draft} update={update} />
        </TabsContent>
        <TabsContent value="log">
          <LogTab data={data} />
        </TabsContent>
      </Tabs>
    </div>
  );
}

function TestSender({ draft, label }: { draft?: EmailAlertSettings; label: string }) {
  const test = useSendEmailAlertTest();
  const [email, setEmail] = useState("");
  const [mode, setMode] = useState<EmailAlertPreviewMode>("instant");
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Input
        type="email"
        placeholder="you@example.com"
        value={email}
        onChange={(e) => setEmail(e.target.value)}
        className="max-w-72"
        aria-label="Test address"
      />
      <select
        className={`${fieldClass} max-w-52`}
        value={mode}
        onChange={(e) => setMode(e.target.value as EmailAlertPreviewMode)}
        aria-label="Send as"
      >
        <option value="instant">As an instant alert</option>
        <option value="daily">As a daily digest</option>
        <option value="weekly">As a weekly digest</option>
      </select>
      <Button
        variant="outline"
        disabled={!email || test.isPending}
        onClick={() =>
          test.mutate(
            { email, mode, settings: draft },
            {
              onSuccess: (r) => toast.success(`Sent “${r.subject}” with ${r.items} items`),
              onError: (e) => toast.error((e as Error).message),
            },
          )
        }
      >
        {test.isPending ? (
          <Loader2 className="h-4 w-4 animate-spin" />
        ) : (
          <Send className="h-4 w-4" />
        )}
        {label}
      </Button>
    </div>
  );
}

function OverviewTab({ data, draft }: { data: EmailAlertsOverview; draft: EmailAlertSettings }) {
  const run = useRunEmailAlerts();
  const active = data.recipients.filter((r) => r.active).length;
  const report = run.data ?? data.report;
  return (
    <div className="space-y-4">
      <div className="grid gap-4 sm:grid-cols-3">
        <Panel>
          <div className="text-muted-foreground text-sm">Recipients receiving alerts</div>
          <div className="text-3xl font-semibold">{active}</div>
        </Panel>
        <Panel>
          <div className="text-muted-foreground text-sm">Emails in the log</div>
          <div className="text-3xl font-semibold">{data.log.filter((l) => !l.error).length}</div>
        </Panel>
        <Panel>
          <div className="text-muted-foreground text-sm">Last run</div>
          <div className="text-lg font-semibold">{formatTime(report?.time)}</div>
          {report && (
            <div className="text-muted-foreground text-xs">
              {report.skipped ||
                `${report.sent} sent to ${report.recipients} due${report.errors?.length ? `, ${report.errors.length} failed` : ""}`}
            </div>
          )}
        </Panel>
      </div>
      <Panel
        title="Send now"
        description="Sends anything due: instant alerts for new items, and any digest whose time has passed. Alerts also go out automatically every minute."
      >
        <Button
          disabled={run.isPending}
          onClick={() =>
            run.mutate(undefined, {
              onSuccess: (r) =>
                r.errors?.length
                  ? toast.error(r.errors.join("; "))
                  : toast.success(r.skipped || `${r.sent} email${r.sent === 1 ? "" : "s"} sent`),
              onError: (e) => toast.error((e as Error).message),
            })
          }
        >
          {run.isPending ? (
            <Loader2 className="h-4 w-4 animate-spin" />
          ) : (
            <Mail className="h-4 w-4" />
          )}
          Check and send now
        </Button>
      </Panel>
      <Panel
        title="Send a test email"
        description="Sends the current templates, including unsaved edits, with the newest items on the server."
      >
        <TestSender draft={draft} label="Send test" />
      </Panel>
    </div>
  );
}

function TemplatesTab({
  draft,
  defaults,
  update,
}: {
  draft: EmailAlertSettings;
  defaults: EmailAlertSettings;
  update: (fn: (s: EmailAlertSettings) => void) => void;
}) {
  const [which, setWhich] = useState<"html" | "text">("html");
  const [pane, setPane] = useState<"preview" | "vars">("preview");
  const [mode, setMode] = useState<EmailAlertPreviewMode>("instant");
  const [source, setSource] = useState<"latest" | "sample">("latest");
  const [preview, setPreview] = useState<{
    subject: string;
    html: string;
    text: string;
    note?: string;
    error?: string;
  } | null>(null);
  const [refresh, setRefresh] = useState(0);
  const bodyRef = useRef<HTMLTextAreaElement>(null);
  const subjectRef = useRef<HTMLInputElement>(null);
  const lastFocus = useRef<"body" | "subject">("body");

  // Re-render shortly after edits stop.
  const previewKey = JSON.stringify([draft, mode, source, refresh]);
  useEffect(() => {
    const controller = new AbortController();
    const timer = setTimeout(() => {
      previewEmailAlert(draft, mode, source, controller.signal)
        .then((r) => setPreview(r))
        .catch((e: Error) => {
          if (!controller.signal.aborted)
            setPreview({ subject: "", html: "", text: "", error: e.message });
        });
    }, 500);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- previewKey serializes every input
  }, [previewKey]);

  const insert = (name: string) => {
    let text = `{{${name}}}`;
    if (LIST_VARS.has(name)) text = `{{#${name}}}\n\n{{/${name}}}`;
    else if (CONDITION_VARS.has(name)) text = `{{#${name}}}{{/${name}}}`;
    const target = lastFocus.current === "subject" ? subjectRef.current : bodyRef.current;
    if (!target) return;
    const start = target.selectionStart ?? target.value.length;
    const end = target.selectionEnd ?? start;
    const value = target.value.slice(0, start) + text + target.value.slice(end);
    update((s) => {
      if (lastFocus.current === "subject") s.templates.subject = value;
      else s.templates[which] = value;
    });
    requestAnimationFrame(() => {
      target.focus();
      target.setSelectionRange(start + text.length, start + text.length);
    });
  };

  const customKeys = (draft.custom_vars ?? []).map((v) => v.key).filter(Boolean);
  const groups: [string, string, string[]][] = customKeys.length
    ? [...VARS, ["Your custom variables", "Set on the Design tab.", customKeys]]
    : VARS;

  return (
    <Panel>
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <div className="bg-muted inline-flex rounded-md p-0.5" role="group" aria-label="Template">
          {(["html", "text"] as const).map((t) => (
            <Button
              key={t}
              size="sm"
              variant={which === t ? "secondary" : "ghost"}
              aria-pressed={which === t}
              onClick={() => setWhich(t)}
            >
              {t === "html" ? "HTML" : "Plain text"}
            </Button>
          ))}
        </div>
        <div className="flex-1" />
        <Button
          size="sm"
          variant="outline"
          onClick={() =>
            update((s) => {
              s.templates = structuredClone(defaults.templates);
            })
          }
        >
          <RotateCcw className="h-4 w-4" />
          Reset to default
        </Button>
      </div>
      <Field
        label="Subject"
        hint="Uses the same variables. Line breaks and extra spaces are collapsed."
        htmlFor="tpl-subject"
      >
        <Input
          id="tpl-subject"
          ref={subjectRef}
          className="font-mono text-xs"
          spellCheck={false}
          value={draft.templates.subject}
          onFocus={() => (lastFocus.current = "subject")}
          onChange={(e) => update((s) => void (s.templates.subject = e.target.value))}
        />
      </Field>
      <div className="grid gap-4 xl:grid-cols-2">
        <div>
          <textarea
            ref={bodyRef}
            aria-label="Template body"
            spellCheck={false}
            className="border-input bg-background h-[560px] w-full rounded-md border p-3 font-mono text-xs leading-relaxed"
            value={draft.templates[which]}
            onFocus={() => (lastFocus.current = "body")}
            onChange={(e) => update((s) => void (s.templates[which] = e.target.value))}
          />
          <p className="text-muted-foreground mt-1 text-xs">
            Mustache syntax: <code>{"{{name}}"}</code> inserts a value,{" "}
            <code>{"{{#shows}}…{{/shows}}"}</code> repeats a block for each show (or shows it when a
            value is set), <code>{"{{^x}}…{{/x}}"}</code> shows a block when a value is empty. Click
            a variable to insert it.
          </p>
        </div>
        <div className="min-w-0">
          <div className="mb-2 flex flex-wrap items-center gap-2">
            <select
              className={`${fieldClass} w-auto`}
              value={mode}
              onChange={(e) => setMode(e.target.value as EmailAlertPreviewMode)}
              aria-label="Preview as"
            >
              <option value="instant">Instant alert</option>
              <option value="daily">Daily digest</option>
              <option value="weekly">Weekly digest</option>
            </select>
            <select
              className={`${fieldClass} w-auto`}
              value={source}
              onChange={(e) => setSource(e.target.value as "latest" | "sample")}
              aria-label="Preview content"
            >
              <option value="latest">Newest items</option>
              <option value="sample">Sample data</option>
            </select>
            <Button size="sm" variant="outline" onClick={() => setRefresh((n) => n + 1)}>
              Refresh
            </Button>
            <div className="flex-1" />
            <div
              className="bg-muted inline-flex rounded-md p-0.5"
              role="group"
              aria-label="Side pane"
            >
              {(["preview", "vars"] as const).map((p) => (
                <Button
                  key={p}
                  size="sm"
                  variant={pane === p ? "secondary" : "ghost"}
                  aria-pressed={pane === p}
                  onClick={() => setPane(p)}
                >
                  {p === "preview" ? "Preview" : "Variables"}
                </Button>
              ))}
            </div>
          </div>
          {pane === "preview" ? (
            <div>
              {preview?.error ? (
                <div className="rounded-md border border-red-500/40 bg-red-500/10 p-3 font-mono text-xs">
                  {preview.error}
                </div>
              ) : (
                <>
                  <div className="bg-muted mb-2 truncate rounded-md px-3 py-2 text-sm font-medium">
                    {preview?.subject || "–"}
                  </div>
                  {which === "html" ? (
                    <iframe
                      title="Email preview"
                      sandbox=""
                      srcDoc={preview?.html ?? ""}
                      className="h-[520px] w-full rounded-md border bg-white"
                    />
                  ) : (
                    <pre className="bg-muted h-[520px] overflow-auto rounded-md p-3 text-xs whitespace-pre-wrap">
                      {preview?.text}
                    </pre>
                  )}
                </>
              )}
              {preview?.note && (
                <p className="text-muted-foreground mt-1 text-xs">{preview.note}</p>
              )}
            </div>
          ) : (
            <div className="h-[560px] space-y-2 overflow-auto rounded-md border p-3 text-xs">
              {groups.map(([title, hint, names], i) => (
                <details key={title} open={i === 0 || title.startsWith("Inside {{#shows")}>
                  <summary className="cursor-pointer text-sm font-medium">{title}</summary>
                  <p className="text-muted-foreground my-1">{hint}</p>
                  <div className="flex flex-wrap gap-1">
                    {names.map((n) => (
                      <button
                        key={n}
                        type="button"
                        className="bg-muted hover:bg-accent rounded px-1.5 py-0.5 font-mono"
                        title="Insert"
                        onClick={() => insert(n)}
                      >
                        {`{{${n}}}`}
                      </button>
                    ))}
                  </div>
                </details>
              ))}
              <p className="text-muted-foreground pt-2">
                Three braces, <code>{"{{{name}}}"}</code>, insert a value without HTML-escaping it.{" "}
                <code>{"{{! note }}"}</code> is a comment.
              </p>
            </div>
          )}
        </div>
      </div>
      <div className="mt-4">
        <TestSender draft={draft} label="Send test of these templates" />
      </div>
    </Panel>
  );
}

function DesignTab({
  draft,
  update,
}: {
  draft: EmailAlertSettings;
  update: (fn: (s: EmailAlertSettings) => void) => void;
}) {
  const theme = draft.theme;
  const text = (
    key: "logo_url" | "header_title" | "intro" | "button_text" | "footer_text",
    label: string,
    hint?: React.ReactNode,
  ) => (
    <Field label={label} hint={hint} htmlFor={`theme-${key}`}>
      <Input
        id={`theme-${key}`}
        value={theme[key] ?? ""}
        onChange={(e) => update((s) => void (s.theme[key] = e.target.value))}
      />
    </Field>
  );
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Panel
        title="Colours"
        description={
          <>
            Available in templates as <code>{"{{accent_color}}"}</code>,{" "}
            <code>{"{{background_color}}"}</code> and so on.
          </>
        }
      >
        {COLORS.map(([key, label]) => {
          const value = String(theme[key] ?? "");
          return (
            <Field key={key} label={label}>
              <div className="flex items-center gap-2">
                <input
                  type="color"
                  aria-label={label}
                  value={/^#[0-9a-f]{6}$/i.test(value) ? value : "#000000"}
                  onChange={(e) => update((s) => void ((s.theme[key] as string) = e.target.value))}
                  className="h-9 w-12 cursor-pointer rounded border"
                />
                <Input
                  className="max-w-36 font-mono"
                  value={value}
                  onChange={(e) => update((s) => void ((s.theme[key] as string) = e.target.value))}
                />
              </div>
            </Field>
          );
        })}
      </Panel>
      <Panel title="Wording and branding">
        {text(
          "logo_url",
          "Logo URL",
          "A public PNG or JPG. Leave empty to show the server name instead.",
        )}
        {text(
          "header_title",
          "Heading",
          <>
            Can use variables, for example <code>{"New on {{server_name}}"}</code>.
          </>,
        )}
        {text("intro", "Intro line")}
        {text("button_text", "Button text")}
        {text("footer_text", "Footer")}
        <ToggleRow
          label="Show the backdrop behind each show and movie"
          checked={theme.backdrop_behind_show}
          onChange={(v) => update((s) => void (s.theme.backdrop_behind_show = v))}
        />
        <ToggleRow
          label="Open links in the Silo app on phones that have it"
          hint="Links go through a short page on your server that opens the app, or the web page when it isn't installed. {{web_url}} always goes straight to the web."
          checked={theme.open_in_app}
          onChange={(v) => update((s) => void (s.theme.open_in_app = v))}
        />
        <Field
          label="Backdrop darkness (%)"
          hint="How strongly the card colour is laid over the backdrop so text stays readable."
          htmlFor="theme-dim"
        >
          <NumberInput
            id="theme-dim"
            min={0}
            max={95}
            value={theme.backdrop_dim}
            onChange={(v) => update((s) => void (s.theme.backdrop_dim = v))}
          />
        </Field>
      </Panel>
      <Panel
        title="Custom variables"
        description={
          <>
            Your own values for templates. A variable named <code>signoff</code> is available as{" "}
            <code>{"{{signoff}}"}</code> and <code>{"{{custom.signoff}}"}</code>.
          </>
        }
      >
        {(draft.custom_vars ?? []).length === 0 && (
          <p className="text-muted-foreground mb-3 text-sm">None yet.</p>
        )}
        {(draft.custom_vars ?? []).map((v, i) => (
          <div key={i} className="mb-2 flex gap-2">
            <Input
              className="max-w-40 font-mono"
              placeholder="name"
              aria-label="Variable name"
              value={v.key}
              onChange={(e) =>
                update((s) => {
                  const cv = s.custom_vars?.[i];
                  if (cv) cv.key = e.target.value;
                })
              }
            />
            <Input
              placeholder="value"
              aria-label="Variable value"
              value={v.value}
              onChange={(e) =>
                update((s) => {
                  const cv = s.custom_vars?.[i];
                  if (cv) cv.value = e.target.value;
                })
              }
            />
            <Button
              variant="ghost"
              size="icon"
              aria-label="Remove variable"
              onClick={() => update((s) => void s.custom_vars!.splice(i, 1))}
            >
              <Trash2 className="h-4 w-4" />
            </Button>
          </div>
        ))}
        <Button
          variant="outline"
          size="sm"
          onClick={() =>
            update((s) => void (s.custom_vars = [...(s.custom_vars ?? []), { key: "", value: "" }]))
          }
        >
          <Plus className="h-4 w-4" />
          Add variable
        </Button>
      </Panel>
      <Panel title="Content">
        <Field
          label="Synopsis length"
          hint="Characters before synopses are shortened. 0 keeps them whole."
          htmlFor="d-syn"
        >
          <NumberInput
            id="d-syn"
            min={0}
            max={5000}
            value={draft.delivery.synopsis_length}
            onChange={(v) => update((s) => void (s.delivery.synopsis_length = v))}
          />
        </Field>
        <ToggleRow
          label="Embed posters and episode stills in the email"
          hint="They show even when the reader can't reach your server."
          checked={draft.delivery.embed_images}
          onChange={(v) => update((s) => void (s.delivery.embed_images = v))}
        />
        <Field label="Most images per email" htmlFor="d-img">
          <NumberInput
            id="d-img"
            min={0}
            max={150}
            value={draft.delivery.max_images}
            onChange={(v) => update((s) => void (s.delivery.max_images = v))}
          />
        </Field>
        <Field
          label="Most items per email"
          hint={
            <>
              Anything beyond this is counted in <code>{"{{more_count}}"}</code>.
            </>
          }
          htmlFor="d-items"
        >
          <NumberInput
            id="d-items"
            min={1}
            max={500}
            value={draft.delivery.max_items}
            onChange={(v) => update((s) => void (s.delivery.max_items = v))}
          />
        </Field>
      </Panel>
    </div>
  );
}

function recipientIncludes(
  r: EmailAlertRecipientStatus,
  libraries: EmailAlertsOverview["libraries"],
) {
  const types = r.types?.length
    ? r.types.map((t) => (t === "movie" ? "Movies" : "Episodes")).join(", ")
    : "Movies, episodes";
  const libs = r.library_ids?.length
    ? r.library_ids.map((id) => libraries.find((l) => l.id === id)?.name ?? `#${id}`).join(", ")
    : "every library";
  const what =
    r.kind === "profile"
      ? r.content === "all"
        ? "Everything new · "
        : "Shows they follow · "
      : "";
  return `${what}${types} · ${libs}`;
}

function RecipientsTab({ data }: { data: EmailAlertsOverview }) {
  const [editing, setEditing] = useState<EmailAlertRecipientStatus | "new" | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<EmailAlertRecipientStatus | null>(null);
  const remove = useDeleteEmailAlertRecipient();
  const rewind = useRewindEmailAlertRecipient();
  return (
    <Panel>
      <div className="mb-3 flex items-center">
        <h3 className="text-base font-semibold">Recipients</h3>
        <div className="flex-1" />
        <Button size="sm" onClick={() => setEditing("new")}>
          <Plus className="h-4 w-4" />
          Add recipient
        </Button>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead className="text-muted-foreground text-left text-xs">
            <tr>
              <th className="py-2 pr-3 font-medium">Recipient</th>
              <th className="py-2 pr-3 font-medium">Delivery</th>
              <th className="py-2 pr-3 font-medium">Includes</th>
              <th className="py-2 pr-3 font-medium">Status</th>
              <th className="py-2 pr-3 font-medium">Last sent</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {data.recipients.length === 0 && (
              <tr>
                <td colSpan={6} className="text-muted-foreground py-6 text-center">
                  No recipients yet. Add an address, or let people turn email on in their own
                  notification settings.
                </td>
              </tr>
            )}
            {data.recipients.map((r) => (
              <tr key={r.key} className="border-border border-t align-top">
                <td className="py-2 pr-3">
                  <div className="font-medium">{r.name || r.email}</div>
                  <div className="text-muted-foreground text-xs">
                    {r.name ? r.email : ""}{" "}
                    {r.kind === "profile" && <Badge variant="secondary">Profile</Badge>}
                  </div>
                </td>
                <td className="py-2 pr-3">
                  {MODE_LABELS[r.mode] ?? r.mode}
                  {r.next_due && (
                    <div className="text-muted-foreground text-xs">
                      Next digest {formatTime(r.next_due)}
                    </div>
                  )}
                </td>
                <td className="text-muted-foreground py-2 pr-3 text-xs">
                  {recipientIncludes(r, data.libraries)}
                </td>
                <td className="py-2 pr-3 text-xs">
                  {!r.active ? (
                    <Badge variant="outline">Off</Badge>
                  ) : r.failures ? (
                    <Badge variant="destructive">Failing ({r.failures})</Badge>
                  ) : r.held_until ? (
                    <span className="text-muted-foreground">
                      Held until {formatTime(r.held_until)}
                    </span>
                  ) : (
                    <Badge variant="secondary">Active</Badge>
                  )}
                </td>
                <td className="text-muted-foreground py-2 pr-3 text-xs">
                  {formatTime(r.last_sent)}
                </td>
                <td className="py-2 text-right whitespace-nowrap">
                  <Button size="sm" variant="ghost" onClick={() => setEditing(r)}>
                    Edit
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    title="Send the last 24 hours again on the next run"
                    onClick={() =>
                      rewind.mutate(
                        { key: r.key, hours: 24 },
                        {
                          onSuccess: () =>
                            toast.success("The last 24 hours go out again on the next send"),
                        },
                      )
                    }
                  >
                    Resend 24h
                  </Button>
                  <Button
                    size="icon"
                    variant="ghost"
                    aria-label="Remove"
                    onClick={() => setConfirmDelete(r)}
                  >
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="text-muted-foreground mt-3 text-xs">
        Profiles appear here once they verify an address and turn email on in their notification
        settings. Recipient changes save immediately.
      </p>
      {editing && (
        <RecipientDialog
          data={data}
          recipient={editing === "new" ? null : editing}
          onClose={() => setEditing(null)}
        />
      )}
      <Dialog open={!!confirmDelete} onOpenChange={(open) => !open && setConfirmDelete(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {confirmDelete?.kind === "profile"
                ? "Turn this profile's email off?"
                : "Remove recipient?"}
            </DialogTitle>
            <DialogDescription>
              {confirmDelete?.kind === "profile"
                ? `${confirmDelete?.email} stops getting emails. The profile can turn them back on in its settings.`
                : `${confirmDelete?.email} stops getting emails and is removed from the list.`}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setConfirmDelete(null)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={() =>
                confirmDelete &&
                remove.mutate(confirmDelete.key, {
                  onSuccess: () => setConfirmDelete(null),
                  onError: (e) => toast.error((e as Error).message),
                })
              }
            >
              {confirmDelete?.kind === "profile" ? "Turn off" : "Remove"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Panel>
  );
}

function RecipientDialog({
  data,
  recipient,
  onClose,
}: {
  data: EmailAlertsOverview;
  recipient: EmailAlertRecipientStatus | null;
  onClose: () => void;
}) {
  const save = useSaveEmailAlertRecipient();
  const isProfile = recipient?.kind === "profile";
  const [form, setForm] = useState<EmailAlertRecipientInput>(() => ({
    name: recipient?.name ?? "",
    email: recipient?.email ?? "",
    enabled: recipient?.enabled ?? true,
    mode: (recipient?.mode ?? "instant") as EmailAlertRecipientInput["mode"],
    content: (recipient?.content ?? "all") as EmailAlertRecipientInput["content"],
    types: recipient?.types ?? [],
    library_ids: recipient?.library_ids ?? [],
    max_age_hours: recipient?.max_age_hours ?? 0,
  }));
  const types = form.types?.length ? form.types : ["episode", "movie"];
  const toggleType = (t: string, on: boolean) => {
    const next = on ? [...new Set([...types, t])] : types.filter((x) => x !== t);
    // Unticking both would read as "everything"; keep at least one.
    if (next.length === 0) return;
    setForm({ ...form, types: next.length === 2 ? [] : next });
  };
  const toggleLibrary = (id: number, on: boolean) => {
    const current = form.library_ids ?? [];
    setForm({ ...form, library_ids: on ? [...current, id] : current.filter((x) => x !== id) });
  };
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>
            {recipient ? `Edit ${recipient.name || recipient.email}` : "Add recipient"}
          </DialogTitle>
          {isProfile && (
            <DialogDescription>
              A profile's address is its own; it changes it in its notification settings.
            </DialogDescription>
          )}
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate(
              { key: recipient?.key, recipient: form },
              {
                onSuccess: () => {
                  toast.success("Recipient saved");
                  onClose();
                },
                onError: (err) => toast.error((err as Error).message),
              },
            );
          }}
        >
          {!isProfile && (
            <>
              <Field label="Name" htmlFor="r-name">
                <Input
                  id="r-name"
                  placeholder="Optional"
                  value={form.name ?? ""}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                />
              </Field>
              <Field label="Email" htmlFor="r-email">
                <Input
                  id="r-email"
                  type="email"
                  required
                  value={form.email ?? ""}
                  onChange={(e) => setForm({ ...form, email: e.target.value })}
                />
              </Field>
            </>
          )}
          {isProfile && (
            <Field label="What to send" htmlFor="r-content">
              <select
                id="r-content"
                className={fieldClass}
                value={form.content}
                onChange={(e) =>
                  setForm({
                    ...form,
                    content: e.target.value as EmailAlertRecipientInput["content"],
                  })
                }
              >
                <option value="followed">Shows they follow</option>
                <option value="all">Everything new they can watch</option>
              </select>
            </Field>
          )}
          <Field label="Delivery" htmlFor="r-mode">
            <select
              id="r-mode"
              className={fieldClass}
              value={form.mode}
              onChange={(e) =>
                setForm({ ...form, mode: e.target.value as EmailAlertRecipientInput["mode"] })
              }
            >
              <option value="instant">Instant: shortly after new media arrives</option>
              <option value="instant_daily">Instant, plus a daily digest</option>
              <option value="instant_weekly">Instant, plus a weekly digest</option>
              <option value="daily">Daily digest</option>
              <option value="weekly">Weekly digest</option>
            </select>
          </Field>
          <div className="mb-4 space-y-2">
            <Label>Include</Label>
            {(
              [
                ["episode", "TV episodes"],
                ["movie", "Movies"],
              ] as const
            ).map(([t, label]) => (
              <label key={t} className="flex items-center gap-2 text-sm">
                <Checkbox
                  checked={types.includes(t)}
                  onCheckedChange={(v) => toggleType(t, v === true)}
                />
                {label}
              </label>
            ))}
          </div>
          <Field label="Count as new if added within" htmlFor="r-age">
            <select
              id="r-age"
              className={fieldClass}
              value={form.max_age_hours ?? 0}
              onChange={(e) => setForm({ ...form, max_age_hours: Number(e.target.value) })}
            >
              {data.max_age_options.map((o) => (
                <option key={o.hours} value={o.hours}>
                  {o.label}
                </option>
              ))}
            </select>
          </Field>
          <div className="mb-4 space-y-2">
            <Label>Libraries</Label>
            <p className="text-muted-foreground text-xs">Leave all unticked for every library.</p>
            {data.libraries.map((l) => (
              <label key={l.id} className="flex items-center gap-2 text-sm">
                <Checkbox
                  checked={(form.library_ids ?? []).includes(l.id)}
                  onCheckedChange={(v) => toggleLibrary(l.id, v === true)}
                />
                {l.name}
              </label>
            ))}
          </div>
          <ToggleRow
            label="Enabled"
            checked={form.enabled}
            onChange={(v) => setForm({ ...form, enabled: v })}
          />
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={save.isPending}>
              {save.isPending && <Loader2 className="h-4 w-4 animate-spin" />}
              Save recipient
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function ScheduleTab({
  draft,
  update,
}: {
  draft: EmailAlertSettings;
  update: (fn: (s: EmailAlertSettings) => void) => void;
}) {
  const zones = useMemo(() => {
    try {
      return (Intl as unknown as { supportedValuesOf(k: string): string[] }).supportedValuesOf(
        "timeZone",
      );
    } catch {
      return [];
    }
  }, []);
  const tzNow = useMemo(() => {
    try {
      return new Date().toLocaleTimeString(undefined, {
        timeZone: draft.schedule.timezone || undefined,
        hour: "2-digit",
        minute: "2-digit",
      });
    } catch {
      return "";
    }
  }, [draft.schedule.timezone]);
  const hourSelect = (id: string, value: number, onChange: (v: number) => void) => (
    <select
      id={id}
      className={fieldClass}
      value={value}
      onChange={(e) => onChange(Number(e.target.value))}
    >
      {HOURS.map((h) => (
        <option key={h} value={h}>
          {String(h).padStart(2, "0")}:00
        </option>
      ))}
    </select>
  );
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Panel title="Digest schedule">
        <Field
          label="Time zone"
          hint={
            tzNow
              ? `It's ${tzNow} there now. Empty uses the server's time zone.`
              : "Empty uses the server's time zone."
          }
          htmlFor="s-tz"
        >
          <Input
            id="s-tz"
            list="email-alert-zones"
            placeholder="Europe/London"
            value={draft.schedule.timezone}
            onChange={(e) => update((s) => void (s.schedule.timezone = e.target.value))}
          />
          <datalist id="email-alert-zones">
            {zones.map((z) => (
              <option key={z} value={z} />
            ))}
          </datalist>
        </Field>
        <Field label="Daily digest at" htmlFor="s-daily">
          {hourSelect("s-daily", draft.schedule.daily_hour, (v) =>
            update((s) => void (s.schedule.daily_hour = v)),
          )}
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Weekly digest on" htmlFor="s-wday">
            <select
              id="s-wday"
              className={fieldClass}
              value={draft.schedule.weekly_day}
              onChange={(e) => update((s) => void (s.schedule.weekly_day = Number(e.target.value)))}
            >
              {WEEKDAYS.map((d, i) => (
                <option key={d} value={i}>
                  {d}
                </option>
              ))}
            </select>
          </Field>
          <Field label="at" htmlFor="s-whour">
            {hourSelect("s-whour", draft.schedule.weekly_hour, (v) =>
              update((s) => void (s.schedule.weekly_hour = v)),
            )}
          </Field>
        </div>
      </Panel>
      <Panel title="Instant alerts">
        <Field
          label="Wait after an item arrives (minutes)"
          hint="Gives metadata and artwork time to arrive, and batches a run of new episodes into one email."
          htmlFor="s-settle"
        >
          <NumberInput
            id="s-settle"
            min={0}
            max={240}
            value={draft.delivery.settle_minutes}
            onChange={(v) => update((s) => void (s.delivery.settle_minutes = v))}
          />
        </Field>
        <Field
          label="Minimum gap between instant emails (minutes)"
          hint="Per recipient. The first item goes out straight away; anything arriving within the gap is held and sent together. 0 turns this off."
          htmlFor="s-gap"
        >
          <NumberInput
            id="s-gap"
            min={0}
            max={1440}
            value={draft.delivery.instant_gap_minutes}
            onChange={(v) => update((s) => void (s.delivery.instant_gap_minutes = v))}
          />
        </Field>
        <Field
          label="Count as new if added within"
          hint="Default for every recipient; each can choose their own. Anything older is never emailed, even after an outage. Weekly digests reach back at least a week."
          htmlFor="s-age"
        >
          <select
            id="s-age"
            className={fieldClass}
            value={draft.delivery.max_age_hours}
            onChange={(e) =>
              update((s) => void (s.delivery.max_age_hours = Number(e.target.value)))
            }
          >
            {[
              [24, "24 hours"],
              [48, "48 hours"],
              [72, "3 days"],
              [168, "1 week"],
              [336, "2 weeks"],
              [720, "30 days"],
            ].map(([h, label]) => (
              <option key={h} value={h}>
                {label}
              </option>
            ))}
          </select>
        </Field>
        <Field
          label="Most instant emails per day"
          hint="Per recipient. Past it, new items wait for the first email after midnight. 0 means no limit."
          htmlFor="s-cap"
        >
          <NumberInput
            id="s-cap"
            min={0}
            max={500}
            value={draft.delivery.instant_daily_cap}
            onChange={(v) => update((s) => void (s.delivery.instant_daily_cap = v))}
          />
        </Field>
        <ToggleRow
          label="Pause all sending"
          checked={draft.delivery.paused}
          onChange={(v) => update((s) => void (s.delivery.paused = v))}
        />
      </Panel>
      <Panel title="Self-service sign-up">
        <ToggleRow
          label="Let profiles email themselves"
          hint="From Settings → Notifications. Off stops every profile's email; added recipients carry on."
          checked={draft.self_service.enabled}
          onChange={(v) => update((s) => void (s.self_service.enabled = v))}
        />
        <p className="text-muted-foreground text-xs">
          Profiles always confirm their address by email first. Every email includes a manage link
          and one-click unsubscribe, which work without signing in.
        </p>
      </Panel>
    </div>
  );
}

function LogTab({ data }: { data: EmailAlertsOverview }) {
  return (
    <Panel>
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead className="text-muted-foreground text-left text-xs">
            <tr>
              <th className="py-2 pr-3 font-medium">When</th>
              <th className="py-2 pr-3 font-medium">To</th>
              <th className="py-2 pr-3 font-medium">Type</th>
              <th className="py-2 pr-3 font-medium">Items</th>
              <th className="py-2 font-medium">Subject or error</th>
            </tr>
          </thead>
          <tbody>
            {data.log.length === 0 && (
              <tr>
                <td colSpan={5} className="text-muted-foreground py-6 text-center">
                  Nothing sent yet.
                </td>
              </tr>
            )}
            {data.log.map((l, i) => (
              <tr key={`${l.sent_at}-${i}`} className="border-border border-t">
                <td className="text-muted-foreground py-2 pr-3 text-xs whitespace-nowrap">
                  {formatTime(l.sent_at)}
                </td>
                <td className="py-2 pr-3">{l.email}</td>
                <td className="py-2 pr-3 capitalize">{l.leg}</td>
                <td className="py-2 pr-3">{l.items}</td>
                <td className={`py-2 ${l.error ? "text-destructive" : ""}`}>
                  {l.error || l.subject}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Panel>
  );
}
