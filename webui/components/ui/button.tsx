"use client";

import { Button as HeroButton, type ButtonProps as HeroButtonProps } from "@heroui/react";

import { cn } from "@/lib/utils/cn";

export type AppButtonTone = "primary" | "neutral" | "danger" | "ghost";

export interface AppButtonProps extends HeroButtonProps {
  tone?: AppButtonTone;
}

const toneStyles: Record<AppButtonTone, string> = {
  primary: "data-[hover=true]:opacity-90",
  neutral: "border border-border/70 bg-surface text-text hover:bg-surface-elevated",
  danger: "data-[hover=true]:opacity-90",
  ghost: "bg-transparent text-text hover:bg-surface-elevated",
};

export function AppButton({
  tone = "primary",
  className,
  color,
  variant,
  ...props
}: AppButtonProps) {
  return (
    <HeroButton
      className={cn(
        "min-w-fit font-medium transition-transform data-[pressed=true]:scale-[0.98]",
        toneStyles[tone],
        className,
      )}
      color={color ?? (tone === "danger" ? "danger" : tone === "primary" ? "primary" : "default")}
      radius="md"
      variant={variant ?? (tone === "ghost" ? "light" : "solid")}
      {...props}
    />
  );
}
