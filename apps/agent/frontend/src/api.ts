import { Call } from "@wailsio/runtime";
import type { Grant, Snapshot } from "./model";
const service = "kiosentra/apps/agent/internal/ui.Bridge.";
export const api = {
  uiWarning: () => Call.ByName(service + "UIWarning") as Promise<string>,
  snapshot: () => Call.ByName(service + "Snapshot") as Promise<Snapshot>,
  authorize: (secret: string, scope: string) =>
    Call.ByName(service + "Authorize", secret, scope) as Promise<Grant>,
  action: (token: string, scope: string) =>
    Call.ByName(service + "Action", token, scope) as Promise<void>,
};
