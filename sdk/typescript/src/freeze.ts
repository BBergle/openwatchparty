/**
 * @internal Freezes a value the SDK created and every object and array inside it, then returns it (SDK.md §16.6).
 * Never pass it an app object: candidates, refs and adapters stay the app's.
 */
export function frozen<V>(value: V): V {
  if (typeof value === "object" && value !== null && !Object.isFrozen(value)) {
    Object.freeze(value);
    for (const v of Object.values(value)) frozen(v);
  }
  return value;
}

/** @internal A read-only type with its top-level members writable, for building a value before freezing it. */
export type Writable<T> = { -readonly [K in keyof T]: T[K] };
