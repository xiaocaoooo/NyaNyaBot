"use client";

import { Input as HeroInput, type InputProps as HeroInputProps } from "@heroui/react";

import { cn } from "@/lib/utils/cn";

export function AppInput({ classNames, ...props }: HeroInputProps) {
  return (
    <HeroInput
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
