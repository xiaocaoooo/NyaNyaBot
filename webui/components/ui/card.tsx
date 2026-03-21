"use client";

import {
  Card as HeroCard,
  CardBody as HeroCardBody,
  CardFooter as HeroCardFooter,
  CardHeader as HeroCardHeader,
  type CardProps as HeroCardProps,
} from "@heroui/react";
import type { ComponentPropsWithoutRef } from "react";

import { cn } from "@/lib/utils/cn";

export function AppCard({ className, ...props }: HeroCardProps) {
  return (
    <HeroCard
      className={cn(
        "border border-border/70 bg-surface/95 shadow-panel backdrop-blur-sm",
        className,
      )}
      radius="lg"
      shadow="none"
      {...props}
    />
  );
}

export function AppCardHeader({ className, ...props }: ComponentPropsWithoutRef<typeof HeroCardHeader>) {
  return <HeroCardHeader className={cn("flex flex-col items-start gap-1", className)} {...props} />;
}

export function AppCardBody({ className, ...props }: ComponentPropsWithoutRef<typeof HeroCardBody>) {
  return <HeroCardBody className={cn("gap-3", className)} {...props} />;
}

export function AppCardFooter({ className, ...props }: ComponentPropsWithoutRef<typeof HeroCardFooter>) {
  return <HeroCardFooter className={cn("flex items-center gap-2", className)} {...props} />;
}
