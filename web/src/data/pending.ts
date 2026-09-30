import { createStore, produce } from "solid-js/store";

/**
 * In-flight mutations keyed by "<action>|<target>". Drives button spinners
 * and prevents double-firing the same action on the same entity.
 */
const [pending, setPending] = createStore<Record<string, true>>({});

export const isPending = (key: string): boolean => pending[key] === true;

export async function withPending<T>(key: string, fn: () => Promise<T>): Promise<T | undefined> {
  if (pending[key]) return undefined;
  setPending(key, true);
  try {
    return await fn();
  } finally {
    setPending(produce((s) => void delete s[key]));
  }
}
