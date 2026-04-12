import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import { Check, Monitor, MoonStar, SunMedium, type LucideIcon } from "lucide-react"

import { useTheme, type Theme } from "@/hooks/use-theme"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { TooltipContent, TooltipProvider, TooltipRoot, TooltipTrigger } from "@/components/ui/tooltip"

type ThemeOption = {
  value: Theme
  label: string
  icon: LucideIcon
}

const options: ThemeOption[] = [
  { value: "light", label: "Light", icon: SunMedium },
  { value: "dark", label: "Dark", icon: MoonStar },
  { value: "system", label: "System", icon: Monitor },
]

export function ThemeToggle() {
  const { theme, resolvedTheme, setTheme } = useTheme()
  const ActiveIcon = resolvedTheme === "dark" ? MoonStar : SunMedium

  return (
    <TooltipProvider>
      <TooltipRoot>
        <DropdownMenu.Root>
          <TooltipTrigger asChild>
            <DropdownMenu.Trigger asChild>
              <Button variant="outline" size="icon" aria-label="Switch theme">
                <ActiveIcon className="h-4 w-4" />
              </Button>
            </DropdownMenu.Trigger>
          </TooltipTrigger>
          <DropdownMenu.Portal>
            <DropdownMenu.Content
              sideOffset={8}
              className="z-[400] min-w-40 rounded-xl border border-[var(--border)] bg-[var(--surface)] p-1 shadow-lg"
            >
              {options.map((option) => (
                <DropdownMenu.Item
                  key={option.value}
                  onSelect={() => setTheme(option.value)}
                  className={cn(
                    "flex cursor-pointer items-center gap-2 rounded-lg px-3 py-2 text-sm outline-none transition-colors duration-150",
                    "text-[var(--text-primary)] focus:bg-[var(--background-muted)] hover:bg-[var(--background-muted)]",
                  )}
                >
                  <option.icon className="h-4 w-4" />
                  <span className="flex-1">{option.label}</span>
                  {theme === option.value ? <Check className="h-4 w-4 text-[var(--accent)]" /> : null}
                </DropdownMenu.Item>
              ))}
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
        <TooltipContent>Switch theme</TooltipContent>
      </TooltipRoot>
    </TooltipProvider>
  )
}
