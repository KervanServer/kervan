import { ThemeProvider as NextThemeProvider, type ThemeProviderProps } from "next-themes"

export function ThemeProvider(props: ThemeProviderProps) {
  return <NextThemeProvider {...props} />
}


