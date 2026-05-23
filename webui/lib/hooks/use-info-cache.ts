"use client";

import { useEffect, useState } from "react";
import { apiClient } from "@/lib/api/client";

interface CacheItem {
  name: string;
  fetchedAt: number;
}

const CACHE_TTL = 5 * 60 * 1000; // 5 minutes
const cache = new Map<string, CacheItem>();
const pendingSet = new Set<string>();

export function useInfoCache() {
  const [loading, setLoading] = useState(false);
  const [initialized, setInitialized] = useState(false);
  // 用于强制触发重渲染
  const [, setTick] = useState(0);

  useEffect(() => {
    if (initialized) return;

    const fetchBots = async () => {
      setLoading(true);
      try {
        const data = await apiClient.fetchBots();
        const now = Date.now();
        data.bots.forEach((bot) => {
          cache.set(`user:${bot.self_id}`, { name: bot.nickname, fetchedAt: now });
          bot.groups.forEach((group) => {
            cache.set(`group:${group.group_id}`, { name: group.group_name, fetchedAt: now });
          });
        });
        setInitialized(true);
      } catch (err) {
        console.error("Failed to prefetch bot info:", err);
      } finally {
        setLoading(false);
      }
    };

    fetchBots();
  }, [initialized]);

  const getName = (id: number, type: 'user' | 'group') => {
    const key = `${type}:${id}`;
    const item = cache.get(key);
    if (item && (Date.now() - item.fetchedAt < CACHE_TTL)) {
      return item.name;
    }

    if (!pendingSet.has(key)) {
      pendingSet.add(key);
      apiClient.fetchInfo(id, type).then(data => {
        cache.set(key, { name: data.name, fetchedAt: Date.now() });
        pendingSet.delete(key);
        setTick(t => t + 1);
      }).catch(err => {
        console.error(`Failed to fetch ${key}:`, err);
        pendingSet.delete(key);
      });
    }

    return String(id);
  };

  return { getName, loading };
}
