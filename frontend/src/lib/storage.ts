export interface IndexedRecording {
  id: string;
  deviceId: string;
  title: string;
  recordedAt: string;
  duration: string;
  durationMs: number;
  location?: string;
  transcriptText?: string;
  indexedAt?: string;
}

const INDEX_KEY = 'google_recorder_local_index_v1';
const DB_NAME = 'GoogleRecorderDB';
const DB_VERSION = 1;
const STORE_NAME = 'recordings';

export function getLocalIndex(): Record<string, IndexedRecording> {
  if (typeof window === 'undefined') return {};
  try {
    const raw = localStorage.getItem(INDEX_KEY);
    return raw ? JSON.parse(raw) : {};
  } catch {
    return {};
  }
}

export function saveToLocalIndex(recordings: IndexedRecording[]): void {
  if (typeof window === 'undefined') return;
  const current = getLocalIndex();
  for (const r of recordings) {
    current[r.id] = {
      ...current[r.id],
      ...r,
      indexedAt: new Date().toISOString(),
    };
  }
  try {
    localStorage.setItem(INDEX_KEY, JSON.stringify(current));
  } catch (e) {
    // Expected to fail if index exceeds 5MB localStorage quota; IndexedDB handles persistence
  }
}

function openDB(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    if (typeof window === 'undefined' || !window.indexedDB) {
      return reject(new Error('IndexedDB not supported'));
    }
    const req = indexedDB.open(DB_NAME, DB_VERSION);
    req.onupgradeneeded = () => {
      const db = req.result;
      if (!db.objectStoreNames.contains(STORE_NAME)) {
        db.createObjectStore(STORE_NAME, { keyPath: 'id' });
      }
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

export async function getLocalIndexAsync(): Promise<Record<string, IndexedRecording>> {
  try {
    const db = await openDB();
    return new Promise((resolve) => {
      const tx = db.transaction(STORE_NAME, 'readonly');
      const store = tx.objectStore(STORE_NAME);
      const req = store.getAll();
      req.onsuccess = () => {
        const records: IndexedRecording[] = req.result || [];
        const map: Record<string, IndexedRecording> = {};
        for (const r of records) {
          map[r.id] = r;
        }
        resolve(map);
      };
      req.onerror = () => resolve(getLocalIndex());
    });
  } catch {
    return getLocalIndex();
  }
}

export async function saveToLocalIndexAsync(recordings: IndexedRecording[]): Promise<void> {
  // First update synchronous localStorage as fast cache
  saveToLocalIndex(recordings);

  try {
    const db = await openDB();
    const tx = db.transaction(STORE_NAME, 'readwrite');
    const store = tx.objectStore(STORE_NAME);
    for (const r of recordings) {
      store.put({
        ...r,
        indexedAt: new Date().toISOString(),
      });
    }
  } catch (e) {
    console.error('IndexedDB save error:', e);
  }
}

export function clearLocalIndex(): void {
  if (typeof window === 'undefined') return;
  localStorage.removeItem(INDEX_KEY);
  if (window.indexedDB) {
    indexedDB.deleteDatabase(DB_NAME);
  }
}
