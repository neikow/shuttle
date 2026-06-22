import { useEffect, useRef, useState } from "react";
import { fetchEventSource } from "@microsoft/fetch-event-source";
import { getToken } from "../auth";
import type { ShuttleEvent } from "../types";
import { Empty, Panel, Sha } from "../components/ui";

const TYPE_COLOR: Record<string, string> = {
  "deploy.succeeded": "var(--color-ok)",
  "deploy.failed": "var(--color-err)",
  "deploy.log": "var(--color-muted)",
  "deploy.queued": "var(--color-warn)",
  "deploy.rolled_back": "var(--color-accent)",
  "rollback.queued": "var(--color-warn)",
  "drift.detected": "var(--color-warn)",
  "service.removed": "var(--color-muted)",
  "volumes.purged": "var(--color-muted)",
};

interface Row {
  k: number;
  ev: ShuttleEvent;
}

export function Events() {
  const [rows, setRows] = useState<Row[]>([]);
  const [connected, setConnected] = useState(false);
  const seq = useRef(0);

  useEffect(() => {
    const ctrl = new AbortController();
    fetchEventSource("/events", {
      headers: { Authorization: `Bearer ${getToken() ?? ""}` },
      signal: ctrl.signal,
      onopen: async () => setConnected(true),
      onmessage: (msg) => {
        if (!msg.data) return;
        try {
          const ev = JSON.parse(msg.data) as ShuttleEvent;
          setRows((prev) => [{ k: seq.current++, ev }, ...prev].slice(0, 200));
        } catch {
          /* ignore non-JSON keep-alives */
        }
      },
      onerror: () => setConnected(false),
    });
    return () => ctrl.abort();
  }, []);

  return (
    <Panel
      title="Live events"
      actions={
        <span className="inline-flex items-center gap-1.5 text-xs text-[var(--color-muted)]">
          <span
            className="inline-block h-2 w-2"
            style={{ background: connected ? "var(--color-ok)" : "var(--color-err)" }}
          />
          {connected ? "streaming" : "disconnected"}
        </span>
      }
    >
      {rows.length === 0 ? (
        <Empty>Waiting for events…</Empty>
      ) : (
        <div className="max-h-[70vh] overflow-auto">
          {rows.map(({ k, ev }) => (
            <div
              key={k}
              className="flex items-start gap-3 border-b border-[var(--color-border)]/50 px-3 py-2"
            >
              <span className="mono w-44 shrink-0 text-xs" style={{ color: TYPE_COLOR[ev.type] }}>
                {ev.type}
              </span>
              <div className="min-w-0 flex-1 text-xs">
                {ev.service && <span className="font-medium">{ev.service}</span>}
                {ev.host && <span className="text-[var(--color-muted)]"> @{ev.host}</span>}
                {ev.sha && (
                  <span className="ml-2">
                    <Sha value={ev.sha} />
                  </span>
                )}
                {ev.message && <span className="ml-2 text-[var(--color-muted)]">{ev.message}</span>}
              </div>
              <span className="shrink-0 text-xs text-[var(--color-muted)]">
                {new Date(ev.time).toLocaleTimeString()}
              </span>
            </div>
          ))}
        </div>
      )}
    </Panel>
  );
}
