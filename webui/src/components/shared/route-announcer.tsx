import { useEffect, useState } from "react"
import { useLocation } from "react-router-dom"

function getPageLabel(pathname: string): string {
  switch (pathname) {
    case "/":
      return "Dashboard"
    case "/users":
      return "Users"
    case "/sessions":
      return "Sessions"
    case "/files":
      return "Files"
    case "/transfers":
      return "Transfers"
    case "/audit":
      return "Audit"
    case "/monitoring":
      return "Monitoring"
    case "/apikeys":
      return "API keys"
    case "/configuration":
      return "Configuration"
    default:
      return "Control Center"
  }
}

export function RouteAnnouncer() {
  const location = useLocation()
  const [announcement, setAnnouncement] = useState("")

  useEffect(() => {
    setAnnouncement(`${getPageLabel(location.pathname)} page loaded`)
  }, [location.pathname])

  return (
    <div className="sr-only" role="status" aria-live="polite" aria-atomic="true">
      {announcement}
    </div>
  )
}
