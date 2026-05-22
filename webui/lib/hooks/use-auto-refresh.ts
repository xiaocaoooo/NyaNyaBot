"use client";

import { useEffect, useRef } from "react";
import { apiClient } from "@/lib/api/client";

export function useAutoRefresh(callback: () => Promise<void> | void) {
  const timerRef = useRef<NodeJS.Timeout | null>(null);
  const callbackRef = useRef(callback);

  // Update callback ref when callback changes
  useEffect(() => {
    callbackRef.current = callback;
  }, [callback]);

  useEffect(() => {
    let isMounted = true;

    const setupTimer = async () => {
      try {
        const config = await apiClient.fetchConfig();
        if (!isMounted) return;

        if (config.webui.auto_refresh) {
          const intervalMs = Math.max(1, config.webui.refresh_interval) * 1000;
          
          const tick = async () => {
            await callbackRef.current();
            if (isMounted && config.webui.auto_refresh) {
              timerRef.current = setTimeout(tick, intervalMs);
            }
          };

          timerRef.current = setTimeout(tick, intervalMs);
        }
      } catch (err) {
        console.error("Failed to setup auto refresh timer:", err);
      }
    };

    void setupTimer();

    return () => {
      isMounted = false;
      if (timerRef.current) {
        clearTimeout(timerRef.current);
      }
    };
  }, []); // Only setup once on mount
}
