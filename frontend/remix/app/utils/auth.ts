export async function checkAuth() {
  const response = await fetch("/auth-check", {
    credentials: "include",
  });
  
  const isAuthorized = response.headers.get("X-Authorized") === "true";
  return { isAuthorized };
} 