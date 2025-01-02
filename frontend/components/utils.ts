import { clsx, type ClassValue } from "clsx";
import { useMediaQuery } from 'react-responsive';
import { useState, useEffect } from "react";
import { twMerge } from "tailwind-merge";

const screens = {
    sm: "640px",
    // => @media (min-width: 640px) { ... }
    md: "768px",
    // => @media (min-width: 768px) { ... }
    lg: "1024px",
    // => @media (min-width: 1024px) { ... }
    xl: "1280px",
    // => @media (min-width: 1280px) { ... }
    "2xl": "1536px",
    // => @media (min-width: 1536px) { ... }
}

type BreakpointKey = keyof typeof breakpoints;

const breakpoints = screens;

export function isToday(date: Date) {
    const today = new Date();
    return date.getDate() === today.getDate() &&
        date.getMonth() === today.getMonth() &&
        date.getFullYear() === today.getFullYear();
}

export function isYesterday(date: Date) {
    const yesterday = new Date();
    yesterday.setDate(yesterday.getDate() - 1);
    return date.getDate() === yesterday.getDate() &&
        date.getMonth() === yesterday.getMonth() &&
        date.getFullYear() === yesterday.getFullYear();
}

export function isWithinLast7Days(date: Date) {
    const sevenDaysAgo = new Date();
    sevenDaysAgo.setDate(sevenDaysAgo.getDate() - 7);
    return date >= sevenDaysAgo;
}
  

export function useBreakpoint<K extends BreakpointKey>(breakpointKey: K) {
    const [queryResult, setQueryResult] = useState(true);
    // https://stackoverflow.com/a/71098593
    const bool = useMediaQuery({
      query: `(min-width: ${breakpoints[breakpointKey]})`,
    });
  
    const capitalizedKey = breakpointKey[0].toUpperCase() + breakpointKey.substring(1);
    type Key = `is${Capitalize<K>}`;
    useEffect(() => {
      // Layout sizes can only be determined client-side
      // we return 'false' by default and just set it after hidration to avoid and SSR issues
      setQueryResult(bool);
    }, []);
  
    useEffect(() => {
      if (queryResult !== bool) {
        setQueryResult(bool);
      }
    }, [bool]);
    return {
      [`is${capitalizedKey}`]: queryResult,
    } as Record<Key, boolean>;
  }


export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}