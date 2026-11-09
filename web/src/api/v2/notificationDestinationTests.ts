import type { ProfileRequestContextSnapshot } from "@/api/client";
import { requireNotificationAuthority } from "./notifications";
import { v2 } from "./request";

export async function testNotificationDestination(
  id: string,
  profileContext: ProfileRequestContextSnapshot,
) {
  requireNotificationAuthority(profileContext);
  const result = await v2("POST /api/v2/notifications/webhooks/{id}/test", {
    path: { id },
    profileContext,
    retryAuthentication: false,
  });
  requireNotificationAuthority(profileContext);
  return result;
}
