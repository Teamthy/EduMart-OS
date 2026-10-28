"use client";

import { useEffect, useState } from "react";
import { WifiOff } from "lucide-react";

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
    <div role="status" className="flex items-center justify-center gap-2 bg-[#f4f1ea] px-4 py-2 text-sm text-[#4c4a44]">
      <WifiOff aria-hidden="true" className="size-4" />
      <span>You&apos;re offline. Saved answers stay on this device until the connection returns.</span>
    </div>
  );
}
