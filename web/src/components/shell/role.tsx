"use client";

import { createContext, useContext } from "react";
import type { Role } from "@/lib/role";

const RoleContext = createContext<Role>("owner");

/** The signed-in role, read once on the server (app/layout.tsx). Display only - the middleware enforces. */
export function RoleProvider({ role, children }: { role: Role; children: React.ReactNode }) {
  return <RoleContext.Provider value={role}>{children}</RoleContext.Provider>;
}

export const useRole = () => useContext(RoleContext);
