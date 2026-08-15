"use client";

import { useCallback, useEffect, useState } from "react";

import { apiFetch } from "./api";

export function useApi<T>(path: string | null) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(path !== null);

  const reload = useCallback(() => {
    if (path === null) return;
    setLoading(true);
    apiFetch<T>(path)
      .then((result) => {
        setData(result);
        setError(null);
      })
      .catch((err: Error) => setError(err.message))
      .finally(() => setLoading(false));
  }, [path]);

  useEffect(reload, [reload]);

  return { data, error, loading, reload };
}
