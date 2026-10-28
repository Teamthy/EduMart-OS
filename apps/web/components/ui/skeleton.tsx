import { cn } from "@/lib/utils";

/** Loading placeholder. Always paired with an aria-busy region in the consumer. */
export function Skeleton({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("animate-pulse rounded-md bg-muted", className)} {...props} />;
}
