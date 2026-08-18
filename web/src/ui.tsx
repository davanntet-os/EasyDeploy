// Shared UI feedback primitives: a global activity indicator (the animated
// bar at the top of the app), toast notifications, and a useAction hook that
// ties a button's local busy state to both.

import { useCallback, useState, useSyncExternalStore } from "react";
import { Icon } from "./icons";

// --- activity store (count of in-flight operations) ---
let activityCount = 0;
const activityListeners = new Set<() => void>();
function emitActivity() {
  activityListeners.forEach((l) => l());
}
export const activity = {
  begin() {
    activityCount++;
    emitActivity();
  },
  end() {
    activityCount = Math.max(0, activityCount - 1);
    emitActivity();
  },
  subscribe(l: () => void) {
    activityListeners.add(l);
    return () => activityListeners.delete(l);
  },
  get() {
    return activityCount;
  },
};

// --- toast store ---
export type ToastKind = "success" | "error" | "info";
export interface Toast {
  id: number;
  kind: ToastKind;
  message: string;
}
let toasts: Toast[] = [];
let toastId = 0;
const toastListeners = new Set<() => void>();
function emitToasts() {
  toastListeners.forEach((l) => l());
}
export function toast(kind: ToastKind, message: string) {
  const id = ++toastId;
  toasts = [...toasts, { id, kind, message }];
  emitToasts();
  setTimeout(() => {
    toasts = toasts.filter((t) => t.id !== id);
    emitToasts();
  }, 3800);
}
function dismissToast(id: number) {
  toasts = toasts.filter((t) => t.id !== id);
  emitToasts();
}

// --- run: wrap an async action with activity + toasts ---
export async function run<T>(
  promise: Promise<T>,
  opts: { success?: string } = {}
): Promise<T | undefined> {
  activity.begin();
  try {
    const result = await promise;
    if (opts.success) toast("success", opts.success);
    return result;
  } catch (e) {
    toast("error", String((e as Error).message || e));
    return undefined;
  } finally {
    activity.end();
  }
}

// useAction returns [busy, runAction]. runAction runs the async work, tracks a
// local busy flag for the calling button, and surfaces global feedback.
export function useAction(): [boolean, <T>(p: Promise<T>, opts?: { success?: string }) => Promise<T | undefined>] {
  const [busy, setBusy] = useState(false);
  const runAction = useCallback(async <T,>(p: Promise<T>, opts?: { success?: string }) => {
    setBusy(true);
    try {
      return await run(p, opts);
    } finally {
      setBusy(false);
    }
  }, []);
  return [busy, runAction];
}

// --- components ---

export function ActivityBar() {
  const count = useSyncExternalStore(activity.subscribe, activity.get);
  return <div className={`activity-bar ${count > 0 ? "on" : ""}`} aria-hidden="true" />;
}

export function Toaster() {
  const snapshot = useSyncExternalStore(
    (l) => {
      toastListeners.add(l);
      return () => toastListeners.delete(l);
    },
    () => toasts
  );
  return (
    <div className="toaster">
      {snapshot.map((t) => (
        <div key={t.id} className={`toast ${t.kind}`} role="status">
          <span className="toast-icon">
            {t.kind === "success" ? <Icon.Check /> : t.kind === "error" ? <Icon.Alert /> : <Icon.Refresh />}
          </span>
          <span>{t.message}</span>
          <button className="toast-x" onClick={() => dismissToast(t.id)} aria-label="Dismiss">
            <Icon.Close size={14} />
          </button>
        </div>
      ))}
    </div>
  );
}
