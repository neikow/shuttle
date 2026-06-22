import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchEventSource } from "@microsoft/fetch-event-source";
import { api } from "../api";
import { getToken } from "../auth";
import type { DeployRecord, ShuttleEvent } from "../types";
import { Modal } from "../components/Modal";
import { Button } from "../components/ui";

// A deploy is still producing output while pending/running; terminal states have
// their full logs persisted and need no live tail.
function isInProgress(status: string): boolean {
  return status === "pending" || status === "running";
}

// LogsModal shows the captured output of one deploy (GET /deploys/{id}/logs).
// Read-tier, so it's available to every signed-in role. For an in-progress
// deploy it also tails deploy.log events off the /events SSE stream, so output
// appears live before the terminal result persists the full logs.
export function LogsModal({ deploy, onClose }: { deploy: DeployRecord; onClose: () => void }) {
  const live = isInProgress(deploy.Status);
  const { data, isLoading, error } = useQuery({
    queryKey: ["deploy-logs", deploy.DeployID],
    queryFn: () => api.deployLogs(deploy.DeployID),
  });

  const [liveLines, setLiveLines] = useState<string[]>([]);
  const [streaming, setStreaming] = useState(false);
  const bottomRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!live) return;
    const ctrl = new AbortController();
    fetchEventSource("/events", {
      headers: { Authorization: `Bearer ${getToken() ?? ""}` },
      signal: ctrl.signal,
      onopen: async () => setStreaming(true),
      onmessage: (msg) => {
        if (!msg.data) return;
        try {
          const ev = JSON.parse(msg.data) as ShuttleEvent;
          if (ev.type !== "deploy.log" || ev.deploy_id !== deploy.DeployID || !ev.message) return;
          setLiveLines((prev) => [...prev, ...ev.message!.split("\n")]);
        } catch {
          /* ignore non-JSON keep-alives */
        }
      },
      onerror: () => setStreaming(false),
    });
    return () => ctrl.abort();
  }, [live, deploy.DeployID]);

  // Keep the newest live output in view (scrollIntoView is absent under jsdom).
  useEffect(() => {
    bottomRef.current?.scrollIntoView?.({ block: "nearest" });
  }, [liveLines]);

  const hasPersisted = !!data && data.length > 0;
  const hasContent = hasPersisted || liveLines.length > 0;

  return (
    <Modal
      wide
      title={
        <span>
          Logs · <span className="text-[var(--color-fg)]">{deploy.Service}</span>{" "}
          <span className="mono text-[10px] normal-case">{deploy.SHA.slice(0, 12)}</span>
          {live && (
            <span className="ml-2 inline-flex items-center gap-1 text-[10px] normal-case text-[var(--color-muted)]">
              <span
                className="inline-block h-2 w-2"
                style={{ background: streaming ? "var(--color-ok)" : "var(--color-warn)" }}
              />
              {streaming ? "streaming" : "connecting…"}
            </span>
          )}
        </span>
      }
      onClose={onClose}
      footer={
        <Button onClick={onClose} variant="primary">
          Close
        </Button>
      }
    >
      {isLoading && !live ? (
        <div className="text-[var(--color-muted)]">Loading…</div>
      ) : error && !live ? (
        <div className="text-[var(--color-err)]">Failed to load logs.</div>
      ) : !hasContent ? (
        <div className="text-[var(--color-muted)]">
          {live
            ? "Waiting for output…"
            : "No output captured for this deploy. (Older deploys recorded before logs were stored, or a deploy that failed before the agent ran, have none.)"}
        </div>
      ) : (
        <pre className="mono max-h-[60vh] overflow-auto whitespace-pre-wrap break-words bg-[var(--color-bg)] p-2 text-[11px] leading-relaxed">
          {hasPersisted
            ? data!.map((l, i) => (
                <div
                  key={i}
                  className={l.stream === "stderr" ? "text-[var(--color-err)]" : undefined}
                >
                  {l.text}
                </div>
              ))
            : liveLines.map((text, i) => <div key={i}>{text}</div>)}
          <div ref={bottomRef} />
        </pre>
      )}
    </Modal>
  );
}
