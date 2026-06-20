import type { ReactElement } from "react";
import { render } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ToastProvider } from "../components/Toast";
import { RoleProvider } from "../role-context";
import type { Role } from "../role";

// renderWithProviders wraps a view in the providers it needs at runtime: a fresh
// react-query client (retry off so failures surface immediately), the toast
// provider, and a role context (default admin so all actions render).
export function renderWithProviders(ui: ReactElement, role: Role = "admin") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <RoleProvider value={role}>{ui}</RoleProvider>
      </ToastProvider>
    </QueryClientProvider>,
  );
}
