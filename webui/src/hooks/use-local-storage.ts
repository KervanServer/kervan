import { useCallback, useEffect, useState } from "react"

export function useLocalStorage<T>(key: string, initialValue: T) {
  const [value, setValue] = useState<T>(() => {
    if (typeof window === "undefined") {
      return initialValue
    }

    const storedValue = window.localStorage.getItem(key)
    if (!storedValue) {
      return initialValue
    }

    try {
      return JSON.parse(storedValue) as T
    } catch {
      return initialValue
    }
  })

  useEffect(() => {
    if (typeof window === "undefined") {
      return
    }
    window.localStorage.setItem(key, JSON.stringify(value))
  }, [key, value])

  const updateValue = useCallback((nextValue: T) => {
    setValue(nextValue)
  }, [])

  return [value, updateValue] as const
}
