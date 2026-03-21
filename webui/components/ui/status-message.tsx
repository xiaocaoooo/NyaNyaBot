"use client";

import { Chip } from "@heroui/react";

interface StatusMessageProps {
  children: string;
  tone: "success" | "error" | "info";
}

export function StatusMessage({ children, tone }: StatusMessageProps) {
  const color = tone === "success" ? "success" : tone === "error" ? "danger" : "primary";

  return (
    <Chip className="h-auto rounded-md px-1 py-2" color={color} radius="sm" variant="flat">
      <span className="text-sm leading-5">{children}</span>
    </Chip>
  );
}
