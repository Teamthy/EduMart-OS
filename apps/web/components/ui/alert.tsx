import * as React from "react";
import { AlertCircle, CheckCircle2, Info, WifiOff } from "lucide-react";

import { cn } from "@/lib/utils";

type AlertTone = "info" | "success" | "warning" | "error" | "offline";

const tones: Record<AlertTone, { className: string; Icon: typeof Info }> = {
  info: { className: "border-[#c9d6ef] bg-[#eef3fb] text-[#243b63]", Icon: Info },
  success: { className: "border-[#bfe3cd] bg-[#eaf7ef] text-[#0b5837]", Icon: CheckCircle2 },
  warning: { className: "border-[#f0dcae] bg-[#fdf6e6] text-[#7a5200]", Icon: AlertCircle },
  error: { className: "border-[#efc4c4] bg-[#fdeeee] text-[#8f2431]", Icon: AlertCircle },
  offline: { className: "border-[#d9d4c9] bg-[#f4f1ea] text-[#4c4a44]", Icon: WifiOff },
};

interface AlertProps extends React.HTMLAttributes<HTMLDivElement> {
  tone?: AlertTone;
  title?: string;
}

/** Status banner. `role="alert"` is only used for errors so screen readers are not noisy. */
export function Alert({ tone = "info", title, className, children, ...props }: AlertProps) {
  const { className: toneClass, Icon } = tones[tone];
  return (
    <div
      role={tone === "error" ? "alert" : "status"}
      className={cn("flex items-start gap-3 rounded-lg border p-3 text-sm", toneClass, className)}
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
