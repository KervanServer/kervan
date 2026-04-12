import type { ComponentPropsWithoutRef, ReactNode } from "react"
import * as TooltipPrimitive from "@radix-ui/react-tooltip"

import { cn } from "@/lib/utils"

type TooltipProps = {
  content: ReactNode
  children: ReactNode
  sideOffset?: number
}

export function Tooltip({ content, children, sideOffset = 8 }: TooltipProps) {
  return (
    <TooltipPrimitive.Provider>
      <TooltipPrimitive.Root>
        <TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger>
        <TooltipPrimitive.Portal>
          <TooltipPrimitive.Content
            sideOffset={sideOffset}
            className="z-[600] rounded-md bg-[var(--surface)] px-2 py-1 text-xs text-[var(--text-primary)] shadow-md"
          >
            {content}
          </TooltipPrimitive.Content>
        </TooltipPrimitive.Portal>
      </TooltipPrimitive.Root>
    </TooltipPrimitive.Provider>
  )
}

export const TooltipProvider = TooltipPrimitive.Provider
export const TooltipRoot = TooltipPrimitive.Root
export const TooltipTrigger = TooltipPrimitive.Trigger

export function TooltipContent({
  className,
  ...props
}: ComponentPropsWithoutRef<typeof TooltipPrimitive.Content>) {
  return (
    <TooltipPrimitive.Portal>
      <TooltipPrimitive.Content
        sideOffset={8}
        className={cn(
          "z-[600] rounded-md bg-[var(--surface)] px-2 py-1 text-xs text-[var(--text-primary)] shadow-md",
          className,
        )}
        {...props}
      />
    </TooltipPrimitive.Portal>
  )
}
