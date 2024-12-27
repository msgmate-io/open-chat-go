import { NextResponse } from 'next/server';

export function middleware(request: Request) {

  const reqUrl = request.url.toString().replace("http://", "").replace("https://", "")
  const firstSlash = reqUrl.indexOf("/", 0)
  const pathName = reqUrl.substring(firstSlash, reqUrl.length)

  const requestHeaders = new Headers(request.headers);
  requestHeaders.set('x-url', request.url)
  requestHeaders.set('x-pathname', pathName)

  return NextResponse.next({
    request: {
      headers: requestHeaders,
    }
  });
}

export const config = {
  matcher: ['/((?!api|_next/static|_next/image|.*\\.png$).*)'],
}