"use client";

import { useEffect, useState } from "react";
import { WifiOff } from "lucide-react";

import { statusTones } from "@edumart/shared";

/**
 * Honest connectivity state. When the device is offline the UI says so instead of showing a
 * spinner that will never resolve.
 */
export function OfflineBanner() {
  const [offline, setOffline] = useState(false);

  useEffect(() => {
    const update = () => setOffline(!navigator.onLine);
    update();
    window.addEventListener("online", update);
    window.addEventListener("offline", update);
    return () => {
      window.removeEventListener("online", update);
      window.removeEventListener("offline", update);
    };
  }, []);

  if (!offline) return null;

  return (
    <div
      role="status"
      // Same tone as the offline alert, from the shared tokens: a colour literal here would be invisible
      // to the contrast check that keeps this banner readable.
      style={{ backgroundColor: statusTones.offline.background, color: statusTones.offline.foreground }}
      className="flex items-center justify-center gap-2 px-4 py-2 text-sm"
    >
      <WifiOff aria-hidden="true" className="size-4" />
      <span>You&apos;re offline. Saved answers stay on this device until the connection returns.</span>
    </div>
  );
}
