import { useState, useEffect } from 'react';

// Lazy-init + write-on-change. Values are stored as strings; primitives
// only (string/boolean/number). For booleans, persisted as '1' | '0'.
export function useLocalStorage(key, defaultValue) {
  const [value, setValue] = useState(() => {
    const raw = localStorage.getItem(key);
    if (raw === null) return defaultValue;
    if (typeof defaultValue === 'boolean') return raw === '1';
    return raw;
  });
  useEffect(() => {
    if (typeof value === 'boolean') localStorage.setItem(key, value ? '1' : '0');
    else localStorage.setItem(key, String(value));
  }, [key, value]);
  return [value, setValue];
}
