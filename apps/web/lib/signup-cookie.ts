/**
 * Name of the httpOnly cookie holding the opaque signup-draft token. The draft itself (pathway,
 * subjects, schedule) lives in the Go API; the cookie only says *which* draft is this browser's.
 */
export const DRAFT_COOKIE = "edumart_signup";
