import { createSignal, type Accessor } from "solid-js";

/** Every persisted key lives under one versioned namespace. Bump the version
 * when a stored shape changes incompatibly; old keys are simply ignored. */
const STORAGE_NAMESPACE = "devyard:v1:";

function storage(): Storage | undefined {
  try {
    return globalThis.localStorage;
  } catch {
    return undefined;
  }
}

/** Reads a persisted JSON value. `parse` validates/normalizes the raw value
 * and returns undefined to fall back. */
export function readPersisted<T>(key: string, fallback: T, parse?: (raw: unknown) => T | undefined): T {
  const raw = storage()?.getItem(STORAGE_NAMESPACE + key);
  if (raw == null) return fallback;
  try {
    const value: unknown = JSON.parse(raw);
    if (parse) return parse(value) ?? fallback;
    return value as T;
  } catch {
    return fallback;
  }
}

export function writePersisted(key: string, value: unknown): void {
  try {
    storage()?.setItem(STORAGE_NAMESPACE + key, JSON.stringify(value));
  } catch {
    // Quota or privacy mode: persistence is best-effort.
  }
}


/** A signal mirrored into localStorage. */
export function createPersistedSignal<T>(
  key: string,
  fallback: T,
  parse?: (raw: unknown) => T | undefined,
): [Accessor<T>, (value: T | ((prev: T) => T)) => void] {
  const [value, setValue] = createSignal<T>(readPersisted(key, fallback, parse));
  const set = (next: T | ((prev: T) => T)) => {
    const resolved = setValue(next as never) as T;
    writePersisted(key, resolved);
  };
  return [value, set];
}

export const isBoolean = (v: unknown): boolean | undefined => (typeof v === "boolean" ? v : undefined);
export const oneOf =
  <T extends string>(...values: T[]) =>
  (v: unknown): T | undefined =>
    values.includes(v as T) ? (v as T) : undefined;
