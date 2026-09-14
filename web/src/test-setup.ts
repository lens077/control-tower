class MemoryStorage implements Storage {
  private readonly values = new Map<string, string>();

  get length(): number {
    return this.values.size;
  }

  clear(): void {
    this.values.clear();
  }

  getItem(key: string): string | null {
    return this.values.get(key) ?? null;
  }

  key(index: number): string | null {
    return Array.from(this.values.keys())[index] ?? null;
  }

  removeItem(key: string): void {
    this.values.delete(key);
  }

  setItem(key: string, value: string): void {
    this.values.set(String(key), String(value));
  }
}

function ensureStorage(name: "localStorage" | "sessionStorage"): void {
  if (typeof globalThis[name] === "undefined") {
    Object.defineProperty(globalThis, name, {
      configurable: true,
      value: new MemoryStorage(),
    });
  }
}

ensureStorage("localStorage");
ensureStorage("sessionStorage");
