import { useQuery } from "@tanstack/react-query";
import { api, ApiError } from "../api";
import { Empty, Panel } from "../components/ui";

export function Hosts() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["hosts"],
    queryFn: () => api.hosts(),
    refetchInterval: 15000,
  });

  return (
    <Panel title="Declared hosts">
      {isLoading ? (
        <Empty>Loading…</Empty>
      ) : error ? (
        <Empty>
          {error instanceof ApiError && error.status === 404
            ? "Host listing is not enabled on this orchestrator (enrollment off)."
            : "Failed to load hosts."}
        </Empty>
      ) : !data || data.length === 0 ? (
        <Empty>No hosts declared.</Empty>
      ) : (
        <table className="w-full text-left text-xs">
          <thead className="text-[var(--color-muted)]">
            <tr className="border-b border-[var(--color-border)]">
              <th className="px-3 py-2 font-medium">Host</th>
              <th className="px-3 py-2 font-medium">Labels</th>
            </tr>
          </thead>
          <tbody>
            {data.map((h) => (
              <tr key={h.name} className="border-b border-[var(--color-border)]/50">
                <td className="px-3 py-2 font-medium">{h.name}</td>
                <td className="px-3 py-2 text-[var(--color-muted)]">
                  {h.labels && Object.keys(h.labels).length > 0
                    ? Object.entries(h.labels)
                        .map(([k, v]) => `${k}=${v}`)
                        .join("  ")
                    : "—"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  );
}
