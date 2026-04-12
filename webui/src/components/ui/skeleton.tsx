import { cn } from "@/lib/utils"

type SkeletonProps = {
  className?: string
}

export function Skeleton({ className }: SkeletonProps) {
  return <div className={cn("animate-pulse rounded-xl bg-[var(--background-muted)] motion-reduce:animate-none", className)} />
}
