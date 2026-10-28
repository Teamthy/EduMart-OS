"use client";

import { useState } from "react";

export function ShareCode({ code }: { code: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="row wrap" style={{ gap: 8 }}>
      <span className="input mono strong" style={{ maxWidth: 200, display: "flex", alignItems: "center", letterSpacing: "0.06em" }}>{code}</span>
      <button
        className="btn sm"
        type="button"
        onClick={() => {
          const url = `${window.location.origin}/register?ref=${code}`;
          navigator.clipboard?.writeText(url).catch(() => {});
          setCopied(true);
          setTimeout(() => setCopied(false), 2000);
        }}
      >
        {copied ? "Link copied" : "Copy invite link"}
      </button>
    </div>
  );
}
