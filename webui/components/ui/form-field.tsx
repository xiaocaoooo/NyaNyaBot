import type { ReactNode } from "react";

import { cn } from "@/lib/utils/cn";

interface FormFieldProps {
  children: ReactNode;
  className?: string;
  description?: string;
  error?: string;
  label: string;
  required?: boolean;
}

export function FormField({
  children,
  className,
  description,
  error,
  label,
  required = false,
}: FormFieldProps) {
  return (
    <div className={cn("space-y-1.5", className)}>
      <p className="text-sm font-medium text-text">
        {label}
        {required ? <span className="text-danger"> *</span> : null}
      </p>
      {description ? <p className="text-xs text-muted">{description}</p> : null}
      {children}
      {error ? (
        <p aria-live="polite" className="text-xs text-danger">
          {error}
        </p>
      ) : null}
    </div>
  );
}
