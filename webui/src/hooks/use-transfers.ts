import { useQuery } from "@tanstack/react-query"

import { api } from "@/lib/api"

export function useTransfers(token: string) {
  return useQuery({
    queryKey: ["transfers", token],
    queryFn: () => api.transfers(token),
  })
}
