import { createContext, useContext } from "react";
import type { Role } from "./role";

// RoleContext carries the authenticated caller's role to every view so they can
// gate which mutation actions they render. Defaults to "read" (least privilege)
// until /whoami resolves.
const RoleContext = createContext<Role>("read");

export const RoleProvider = RoleContext.Provider;

export function useRole(): Role {
  return useContext(RoleContext);
}
