import { useQuery } from "@tanstack/react-query"

import { api } from "@/lib/api"

export function useAudit(token: string) {
  return useQuery({
    queryKey: ["audit", token],
    queryFn: () => api.audit(token),
  })
}
