"use client";

import { Textarea as HeroTextarea, type TextAreaProps as HeroTextareaProps } from "@heroui/react";

import { cn } from "@/lib/utils/cn";

export function AppTextarea({ classNames, ...props }: HeroTextareaProps) {
  return (
    <HeroTextarea
      classNames={{
        inputWrapper: cn(
          "border border-border/70 bg-surface data-[hover=true]:border-primary/60 group-data-[focus=true]:border-primary",
          classNames?.inputWrapper,
        ),
        label: cn("text-muted", classNames?.label),
        input: cn("text-text placeholder:text-muted/80", classNames?.input),
      }}
      radius="md"
      variant="bordered"
      {...props}
    />
  );
}
