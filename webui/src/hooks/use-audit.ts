import { keepPreviousData, useQuery } from "@tanstack/react-query"

import { api } from "@/lib/api"
import type { AuditFilters } from "@/lib/types"

export function useAudit(token: string, filters: AuditFilters = {}, page = 1) {
  return useQuery({
    queryKey: ["audit", token, filters, page],
    queryFn: () => api.audit(token, filters, page, 50),
    placeholderData: keepPreviousData,
  })
}
