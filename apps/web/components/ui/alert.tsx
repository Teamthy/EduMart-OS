import * as React from "react";
import { AlertCircle, CheckCircle2, Info, WifiOff } from "lucide-react";

import { statusTones } from "@edumart/shared";

import { cn } from "@/lib/utils";

type AlertTone = keyof typeof statusTones;

const icons: Record<AlertTone, typeof Info> = {
  info: Info,
  success: CheckCircle2,
  warning: AlertCircle,
  error: AlertCircle,
  offline: WifiOff,
};

interface AlertProps extends React.HTMLAttributes<HTMLDivElement> {
  tone?: AlertTone;
  title?: string;
}

/** Status banner. `role="alert"` is only used for errors so screen readers are not noisy. */
export function Alert({ tone = "info", title, className, children, ...props }: AlertProps) {
  // The colours come from the shared token file, not from literals here: a hex value buried in JSX is
  // invisible to the contrast check, and the check is what keeps a banner readable.
  const { background, border, foreground } = statusTones[tone];
  const Icon = icons[tone];
  return (
    <div
      role={tone === "error" ? "alert" : "status"}
      style={{ backgroundColor: background, borderColor: border, color: foreground }}
      className={cn("flex items-start gap-3 rounded-lg border p-3 text-sm", className)}
      {...props}
    >
      <Icon aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
      <div className="space-y-1">
        {title ? <p className="font-semibold">{title}</p> : null}
        {children ? <div className="text-sm">{children}</div> : null}
      </div>
    </div>
  );
}
