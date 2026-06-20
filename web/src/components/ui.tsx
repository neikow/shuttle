import clsx from "clsx";
import type { ReactNode } from "react";

export function Panel({
  title,
  actions,
  children,
  className,
}: {
  title?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={clsx("border border-[var(--color-border)] bg-[var(--color-panel)]", className)}>
      {(title || actions) && (
        <div className="flex items-center justify-between border-b border-[var(--color-border)] px-3 py-2">
          <div className="text-xs font-medium uppercase tracking-wide text-[var(--color-muted)]">
            {title}
          </div>
          {actions}
        </div>
      )}
      {children}
    </div>
  );
}

const STATUS_COLORS: Record<string, string> = {
  succeeded: "var(--color-ok)",
  success: "var(--color-ok)",
  failed: "var(--color-err)",
  error: "var(--color-err)",
  pending: "var(--color-warn)",
  queued: "var(--color-warn)",
  running: "var(--color-accent)",
};

export function StatusDot({ status }: { status: string }) {
  const color = STATUS_COLORS[status.toLowerCase()] ?? "var(--color-muted)";
  return (
    <span className="inline-flex items-center gap-1.5">
      <span className="inline-block h-2 w-2" style={{ background: color }} />
      <span className="mono text-xs">{status}</span>
    </span>
  );
}

export function Sha({ value }: { value?: string }) {
  if (!value) return <span className="text-[var(--color-muted)]">—</span>;
  return <span className="mono text-xs">{value.slice(0, 12)}</span>;
}

export function Button({
  children,
  onClick,
  disabled,
  variant = "default",
  className,
}: {
  children: ReactNode;
  onClick?: () => void;
  disabled?: boolean;
  variant?: "default" | "primary";
  className?: string;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className={clsx(
        "border px-2.5 py-1 text-xs transition-colors disabled:opacity-40",
        variant === "primary"
          ? "border-[var(--color-accent)] bg-[var(--color-accent)]/10 text-[var(--color-accent)] hover:bg-[var(--color-accent)]/20"
          : "border-[var(--color-border)] bg-[var(--color-panel-2)] hover:border-[var(--color-muted)]",
        className,
      )}
    >
      {children}
    </button>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="px-3 py-8 text-center text-xs text-[var(--color-muted)]">{children}</div>;
}
