import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";

import NotificationsAdminSettings from "./NotificationsAdminSettings";
import { fakeSettingsForm } from "./settingsForm.test-support";

const useSettingsFormMock = vi.fn();
const restartKeysMock = vi.fn(() => new Set<string>());
const updateSettingsMock = vi.fn(() => Promise.resolve({ values: {}, restart_required: false }));

const mocks = vi.hoisted(() => ({
  copyTextToClipboard: vi.fn(),
  toastError: vi.fn(),
  toastSuccess: vi.fn(),
}));

vi.mock("@/lib/clipboard", () => ({
  copyTextToClipboard: (...args: unknown[]) => mocks.copyTextToClipboard(...args),
}));

vi.mock("sonner", () => ({
  toast: { error: mocks.toastError, success: mocks.toastSuccess },
}));

vi.mock("@/hooks/queries/admin/settings", () => ({
  useUpdateServerSettings: () => ({ mutateAsync: updateSettingsMock, isPending: false }),
}));

vi.mock("@/hooks/useSettingsForm", () => ({
  useSettingsForm: (...args: unknown[]) => useSettingsFormMock(...args),
}));

vi.mock("@/hooks/useRestartKeys", () => ({
  useRestartKeys: () => restartKeysMock(),
}));

vi.mock("@/hooks/queries/admin/serverNotificationChannels", () => ({
  useServerNotificationChannels: () => ({ data: [] }),
}));

const makeForm = (overrides: Record<string, string> = {}) =>
  fakeSettingsForm(
    {
      "notifications.release_events_enabled": "true",
      "notifications.fanout_enabled": "true",
      "notifications.ui_enabled": "true",
      "notifications.web_push_enabled": "true",
      "notifications.apple_push_delivery_enabled": "true",
      "notifications.android_push_delivery_enabled": "true",
      "notifications.push_relay_url": "https://push.siloserver.org",
      "notifications.push_relay_deployment_id": "01DEPLOYMENT",
      "notifications.push_relay_key_prefix": "cap_v1_test",
      // Relative so the "renews automatically" (not-yet-expired) branch
      // stays stable — a hardcoded date turned into a time bomb once the
      // calendar passed it.
      "notifications.push_relay_expires_at": new Date(
        Date.now() + 30 * 24 * 60 * 60 * 1000,
      ).toISOString(),
      ...overrides,
    },
    {
      save: vi.fn(() => Promise.resolve()),
      sensitiveConfigured: ["notifications.push_relay_api_key"],
    },
  );

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

  return (
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/admin/settings/notifications"]}>
        <NotificationsAdminSettings />
      </MemoryRouter>
    </QueryClientProvider>
  );
}

describe("NotificationsAdminSettings", () => {
  beforeEach(() => {
    localStorage.clear();
    restartKeysMock.mockReturnValue(new Set<string>());
    updateSettingsMock.mockClear();
    mocks.copyTextToClipboard.mockReset();
    mocks.copyTextToClipboard.mockResolvedValue(undefined);
    mocks.toastError.mockReset();
    mocks.toastSuccess.mockReset();
  });

  it("keeps the warning and opens the relay controls when re-registration is required", async () => {
    // Parked by an admin's clear or the relay: first-use registration will not
    // undo that, so the admin has to act.
    const form = makeForm({
      "notifications.push_relay_reregistration_required": "true",
      "notifications.push_relay_deployment_id": "",
    });
    form.sensitiveConfigured = [];
    useSettingsFormMock.mockReturnValue(form);

    render(renderPage());

    expect(screen.getByText("Re-registration required")).toHaveClass("text-amber-500");
    await userEvent.click(screen.getByRole("button", { name: /Silo Push Relay/ }));
    expect(screen.getByRole("button", { name: "Re-register relay" })).toBeInTheDocument();
  });

  it("says so up top, and opens the switches, when the pipeline is paused", () => {
    useSettingsFormMock.mockReturnValue(makeForm({ "notifications.fanout_enabled": "false" }));

    render(renderPage());

    expect(screen.getByRole("status")).toHaveTextContent(/Sending is paused/);
    expect(screen.getByRole("switch", { name: "Work out who wants it" })).not.toBeChecked();
  });

});
