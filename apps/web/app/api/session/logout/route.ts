import { NextResponse } from "next/server";

const API_URL = process.env.EDUMART_API_URL ?? "http://127.0.0.1:8080";

/**
 * Sign-out is a server route so the session cookie can be cleared by the API and the browser
 * redirected without exposing the API origin to the client.
 */
export async function POST(request: Request) {
  const cookieHeader = request.headers.get("cookie") ?? "";
  let setCookie: string | null = null;

  try {
    const response = await fetch(`${API_URL}/v1/auth/logout`, {
      method: "POST",
      headers: { cookie: cookieHeader, "content-type": "application/json" },
      body: "{}",
    });
    setCookie = response.headers.get("set-cookie");
  } catch {
    // Even if the API is unreachable, clear the cookie locally so the user is signed out here.
  }

  const redirect = NextResponse.redirect(new URL("/login", request.url), { status: 303 });
  if (setCookie) {
    redirect.headers.set("set-cookie", setCookie);
  } else {
    redirect.cookies.set("edumart_session", "", { path: "/", maxAge: 0 });
  }
  return redirect;
}
