import * as React from "react"
import { isSmallScreenViewport } from "../lib/responsive"

const MOBILE_BREAKPOINT = 768

export function useIsMobile() {
  const [isMobile, setIsMobile] = React.useState<boolean | undefined>(undefined)

  React.useEffect(() => {
    const mql = window.matchMedia(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`)
    const onChange = () => {
      setIsMobile(window.innerWidth < MOBILE_BREAKPOINT)
    }
    mql.addEventListener("change", onChange)
    setIsMobile(window.innerWidth < MOBILE_BREAKPOINT)
    return () => mql.removeEventListener("change", onChange)
  }, [])

  return !!isMobile
}

// useIsSmallScreen reports whether the current viewport is a "very small"
// device (narrow *or* short, see isSmallScreenViewport). Used to collapse
// dense settings surfaces into an icon that opens a modal/sheet.
export function useIsSmallScreen() {
  const [isSmallScreen, setIsSmallScreen] = React.useState(false)

  React.useEffect(() => {
    const update = () =>
      setIsSmallScreen(isSmallScreenViewport(window.innerWidth, window.innerHeight))
    update()
    window.addEventListener("resize", update)
    window.addEventListener("orientationchange", update)
    return () => {
      window.removeEventListener("resize", update)
      window.removeEventListener("orientationchange", update)
    }
  }, [])

  return isSmallScreen
}
