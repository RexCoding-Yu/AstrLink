import { load, type Store } from "@tauri-apps/plugin-store";

/** Custom values the user created, keyed by routing field name, in creation order. */
export type RoutingCustomValueMap = Record<string, string[]>;

/** One store file holds every field, under a single key. */
const STORE_FILE = "routing-custom-values.json";
const MAP_KEY = "byField";
/** A dropdown longer than this is not a suggestion list anymore. */
const MAX_VALUES_PER_FIELD = 64;
/** Routing field names only: lowercase letters, digits and underscores. */
const FIELD_NAME = /^[a-z0-9_]+$/;

/**
 * Whether `field` names a routing field we may key the map by. Names such as
 * `constructor` or `__proto__` satisfy the pattern but must never be keys, or
 * a lookup would reach into `Object.prototype` instead of our own list.
 */
function isFieldName(field: unknown): field is string {
  return (
    typeof field === "string" &&
    FIELD_NAME.test(field) &&
    !Object.prototype.hasOwnProperty.call(Object.prototype, field)
  );
}

let storePromise: Promise<Store | null> | null = null;
let cache: RoutingCustomValueMap | null = null;
// Every operation runs through this chain, so a read-modify-write cannot
// interleave with another write and drop one of the two entries.
let queue: Promise<unknown> = Promise.resolve();

/**
 * Queues `operation` behind the previous one. The chain tolerates a failed
 * operation by continuing with the next, while the caller's own promise still
 * rejects so it can report the failure.
 */
function serialize<T>(operation: () => Promise<T>): Promise<T> {
  const result = queue.then(operation, operation);
  queue = result.then(
    () => undefined,
    () => undefined,
  );
  return result;
}

/**
 * Opens the store file once per session. Any failure (no Tauri runtime, a
 * missing capability) degrades to `null` so the caller can carry on with an
 * empty map.
 */
function openStore(): Promise<Store | null> {
  storePromise ??= load(STORE_FILE, { autoSave: true }).catch(() => null);
  return storePromise;
}

function sanitize(raw: unknown): RoutingCustomValueMap {
  if (!raw || typeof raw !== "object") return {};
  const map: RoutingCustomValueMap = {};
  for (const [field, values] of Object.entries(
    raw as Record<string, unknown>,
  )) {
    if (!isFieldName(field) || !Array.isArray(values)) continue;
    map[field] = sanitizeValues(values);
  }
  return map;
}

/** Trims, drops empties and duplicates, and keeps creation order, oldest first. */
function sanitizeValues(values: unknown[]): string[] {
  const kept: string[] = [];
  for (const value of values) {
    if (typeof value !== "string") continue;
    const trimmed = value.trim();
    if (!trimmed || kept.includes(trimmed)) continue;
    kept.push(trimmed);
  }
  return kept.slice(0, MAX_VALUES_PER_FIELD);
}

/** Loads the map once, then serves the cached copy to later callers. */
async function currentMap(): Promise<RoutingCustomValueMap> {
  if (cache) return cache;
  const store = await openStore();
  if (!store) {
    cache = {};
    return cache;
  }
  const stored = await store.get(MAP_KEY).catch(() => undefined);
  cache = sanitize(stored);
  return cache;
}

/**
 * Writes `next` through to the store and mirrors it in the cache. The cache
 * only advances once the store has accepted the value; a failed flush to disk
 * afterwards is best effort, since the in-memory store already agrees with us.
 */
async function persist(next: RoutingCustomValueMap): Promise<void> {
  const store = await openStore();
  if (!store) return;
  try {
    await store.set(MAP_KEY, next);
  } catch {
    return;
  }
  cache = next;
  await store.save().catch(() => undefined);
}

/**
 * Reads the persisted custom values. Never throws; a missing or unreadable
 * store yields `{}`. The result is a copy, so callers cannot corrupt the cache.
 */
export function loadRoutingCustomValues(): Promise<RoutingCustomValueMap> {
  return serialize(async () => {
    const map = await currentMap();
    return Object.fromEntries(
      Object.entries(map).map(([field, values]) => [field, [...values]]),
    );
  }).catch(() => ({}));
}

/** Remembers one custom value for a field. Never throws. */
export function rememberRoutingCustomValue(
  field: string,
  value: string,
): Promise<void> {
  return serialize(async () => {
    const trimmed = value.trim();
    if (!isFieldName(field) || !trimmed) return;
    const map = await currentMap();
    const existing = map[field] ?? [];
    if (existing.includes(trimmed)) return;
    // Oldest first, so overflow drops the value the user is least likely to
    // want back.
    const updated = [...existing, trimmed].slice(-MAX_VALUES_PER_FIELD);
    await persist({ ...map, [field]: updated });
  }).catch(() => undefined);
}

/** Forgets one custom value for a field. Never throws. */
export function forgetRoutingCustomValue(
  field: string,
  value: string,
): Promise<void> {
  return serialize(async () => {
    const trimmed = value.trim();
    if (!isFieldName(field)) return;
    const map = await currentMap();
    const existing = map[field];
    if (!existing?.includes(trimmed)) return;
    const updated = existing.filter((entry) => entry !== trimmed);
    if (updated.length) {
      await persist({ ...map, [field]: updated });
      return;
    }
    // An empty list carries no information; drop the key entirely.
    const { [field]: _removed, ...remaining } = map;
    await persist(remaining);
  }).catch(() => undefined);
}
