import { createContext, useCallback, useContext, useRef, useState } from "react";
import type { ReactNode } from "react";

type ToastKind = "ok" | "err";
interface Toast {
  id: number;
  kind: ToastKind;
  text: string;
}

interface ToastApi {
  ok: (text: string) => void;
  err: (text: string) => void;
}

const ToastCtx = createContext<ToastApi | null>(null);

// useToast surfaces transient success/error banners for mutations. Lightweight
// on purpose — a stacked, auto-dismissing list in the corner.
export function useToast(): ToastApi {
  const ctx = useContext(ToastCtx);
  if (!ctx) throw new Error("useToast must be used within ToastProvider");
  return ctx;
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const seq = useRef(0);

  const push = useCallback((kind: ToastKind, text: string) => {
    const id = ++seq.current;
    setToasts((t) => [...t, { id, kind, text }]);
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 5000);
  }, []);

  const api: ToastApi = {
    ok: (text) => push("ok", text),
    err: (text) => push("err", text),
  };

  return (
    <ToastCtx.Provider value={api}>
      {children}
      <div className="fixed bottom-4 right-4 z-[60] flex flex-col gap-2">
        {toasts.map((t) => (
          <div
            key={t.id}
            role="status"
            className="border bg-[var(--color-panel)] px-3 py-2 text-xs shadow-lg"
            style={{
              borderColor: t.kind === "ok" ? "var(--color-ok)" : "var(--color-err)",
              color: t.kind === "ok" ? "var(--color-ok)" : "var(--color-err)",
            }}
          >
            {t.text}
          </div>
        ))}
      </div>
    </ToastCtx.Provider>
  );
}
