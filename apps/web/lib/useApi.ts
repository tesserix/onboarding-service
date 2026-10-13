"use client";

import { useCallback, useEffect, useState } from "react";

import { apiFetch } from "./api";

type Result<T> = { path: string; version: number; data: T | null; error: string | null };

export function useApi<T>(path: string | null) {
  const [result, setResult] = useState<Result<T> | null>(null);
  const [version, setVersion] = useState(0);
  const reload = useCallback(() => setVersion((value) => value + 1), []);

  useEffect(() => {
    if (path === null) return;
    let current = true;
    apiFetch<T>(path)
      .then((data) => {
        if (current) setResult({ path, version, data, error: null });
      })
      .catch((err: Error) => {
        if (current) {
          setResult((previous) => ({
            path,
            version,
            data: previous?.path === path ? previous.data : null,
            error: err.message,
          }));
        }
      });
    return () => {
      current = false;
    };
  }, [path, version]);

  const visible = result?.path === path ? result : null;
  return {
    data: visible?.data ?? null,
    error: visible?.error ?? null,
    loading: path !== null && (!visible || visible.version !== version),
    reload,
  };
}
