import useSWR from "swr";
import { fetcher } from "../lib/utils";

export function useCurrentUser() {
  return useSWR<{ is_admin?: boolean; name?: string; email?: string }>(
    "/api/v1/user/self",
    fetcher
  );
}
