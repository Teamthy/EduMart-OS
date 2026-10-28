import * as React from "react";
import { ChevronDown } from "lucide-react";

import { cn } from "@/lib/utils";

/**
 * Native select, deliberately. It is the most reliable control on low-end Android browsers,
 * keyboard accessible by default, and needs no portal or focus trap.
 */
const Select = React.forwardRef<HTMLSelectElement, React.SelectHTMLAttributes<HTMLSelectElement>>(
  ({ className, children, ...props }, ref) => (
    <div className="relative">
      <select
        ref={ref}
        className={cn(
          "flex h-11 w-full appearance-none rounded-lg border border-input bg-card px-3 py-2 pr-9 text-base shadow-sm",
          "disabled:cursor-not-allowed disabled:opacity-50",
          className,
        )}
        {...props}
      >
        {children}
      </select>
      <ChevronDown aria-hidden="true" className="pointer-events-none absolute right-3 top-3.5 size-4 text-muted-foreground" />
    </div>
  ),
);
Select.displayName = "Select";

export { Select };
