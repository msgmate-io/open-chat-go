import { cn } from "@open-chat-go/ui";
import { getCookie, setCookie, removeCookie } from 'typescript-cookie';
import { PersistStorage, StorageValue } from 'zustand/middleware';
import { isDeviceOnlineRuntime } from "@open-chat-go/ui";

export { cn };

export class APIRequestError extends Error {
  status: number;
  code?: string;
  payload?: unknown;

  constructor(message: string, status: number, code?: string, payload?: unknown) {
    super(message);
    this.name = "APIRequestError";
    this.status = status;
    this.code = code;
    this.payload = payload;
  }
}


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

export const fetcher = async (...args: [RequestInfo, RequestInit?]) => {
  const [input, init] = args
  let nextInit: RequestInit = {
    ...(init || {}),
    credentials: init?.credentials ?? "include",
  }

  if (typeof window !== "undefined") {
    const headers = new Headers(init?.headers)
    headers.set("X-OpenChat-Device-Online", String(isDeviceOnlineRuntime()))
    nextInit = {
      ...(init || {}),
      credentials: init?.credentials ?? "include",
      headers,
    }
  }

  const res = await fetch(input, nextInit)

  if (!res.ok) {
    const contentType = res.headers.get("content-type") || ""
    let message = `Request failed with status ${res.status}`
    let code: string | undefined
    let payload: unknown

    if (contentType.includes("application/json")) {
      const json = await res.json().catch(() => null) as Record<string, unknown> | null
      if (json && typeof json === "object") {
        payload = json
        if (typeof json.message === "string" && json.message.trim()) {
          message = json.message
        }
        if (typeof json.error === "string" && json.error.trim()) {
          code = json.error
        }
      }
    } else {
      const errorText = await res.text().catch(() => "")
      if (errorText.trim()) {
        message = errorText
      }
    }

    throw new APIRequestError(message, res.status, code, payload)
  }

  return res.json()
}
