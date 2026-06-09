import * as React from "react";
import { cn } from "../../lib/utils";

type BadgeVariant = "outline" | "muted" | "success" | "warning";

export function Badge({
  className,
  variant = "outline",
  ...props
}: React.HTMLAttributes<HTMLSpanElement> & { variant?: BadgeVariant }) {
  return (
    <span
      className={cn(
        "inline-flex h-5 shrink-0 items-center gap-1 rounded-md px-1.5 text-[11px] font-medium",
        variant === "outline" && "border border-border text-muted-foreground",
        variant === "muted" && "bg-muted text-muted-foreground",
        variant === "success" && "bg-emerald-950 text-emerald-300",
        variant === "warning" && "bg-amber-950 text-amber-300",
        className,
      )}
      {...props}
    />
  );
}
