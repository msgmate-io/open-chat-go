import { NextResponse } from 'next/server';
import { cookies } from 'next/headers'

const SERVER_ROUTE = "http://backend:1984"

const AUTH_REDIRECTS = [{
  expr: new RegExp("/login?"),
  to: "/chat"
}]

const UNAUTH_REDIRECTS = [{
  expr: new RegExp("/chat?"),
  to: "/login"
}]

export async function middleware(request: Request) {
  const requestUrl = new URL(request.url);
  const reqUrl = request.url.toString().replace("http://", "").replace("https://", "")
  const firstSlash = reqUrl.indexOf("/", 0)
  const pathName = reqUrl.substring(firstSlash, reqUrl.length)

  const requestHeaders = new Headers(request.headers);
  requestHeaders.set('x-url', request.url)
  requestHeaders.set('x-pathname', pathName)

  const cookieStore = await cookies()
  const sessionIdPresent = cookieStore.get("session_id") || false
  let isAuthenticated = false

  if(sessionIdPresent){
    const res = await fetch(`${SERVER_ROUTE}/api/v1/user/self`, { 
      method: "GET" ,
      headers: request.headers      
    })
    if(res.ok){
      isAuthenticated = true
    }
  }
  requestHeaders.set('x-user-authenticated', isAuthenticated.toString())
  const redirectChecks = isAuthenticated ? AUTH_REDIRECTS : UNAUTH_REDIRECTS
  
  for(let red of redirectChecks){
    if(red.expr.exec(pathName)){
      return NextResponse.redirect(new URL(red.to, requestUrl.origin))
    }
  }

  return NextResponse.next({
    request: {
      headers: requestHeaders,
    }
  });
}

export const config = {
  matcher: ['/((?!api|_next/static|_next/image|.*\\.png$).*)'],
}