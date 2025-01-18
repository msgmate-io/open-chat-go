import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"
import { getCookie, setCookie, removeCookie } from 'typescript-cookie';
import { PersistStorage, StorageValue } from 'zustand/middleware';


export const cookiesStorage = <T>(): PersistStorage<T> => ({
  getItem: (name: string) => {
    const value = getCookie(name);
    return value ? JSON.parse(value) : null;
  },
  setItem: (name: string, value: StorageValue<T>) => {
    setCookie(name, JSON.stringify(value), { expires: 1 });
  },
  removeItem: (name: string) => {
    removeCookie(name);
  }
})

export const fetcher = (...args: [RequestInfo, RequestInit?]) => fetch(...args).then(res => res.json())

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
