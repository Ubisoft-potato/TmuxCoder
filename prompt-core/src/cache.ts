import type { ResolvedPrompt } from "./types"
import { promptLogger } from "./logger"

export interface CacheEntry {
  value: ResolvedPrompt
  timestamp: number
  ttl: number
}

export class PromptCache {
  private cache = new Map<string, CacheEntry>()
  private cleanupInterval?: NodeJS.Timeout

  constructor(
    private config: {
      enabled: boolean
      ttl?: number
      maxSize?: number
      cleanupIntervalMs?: number
    }
  ) {
    if (config.enabled) {
      this.cleanupInterval = setInterval(
        () => this.cleanup(),
        config.cleanupIntervalMs || 60000
      )
    }
  }

  static generateKey(agent: string, sessionID: string, modelID?: string): string {
    return `${agent}:${sessionID}:${modelID || "default"}`
  }

  get(key: string): ResolvedPrompt | null {
    if (!this.config.enabled) return null

    const entry = this.cache.get(key)
    if (!entry) return null

    const now = Date.now()
    if (now - entry.timestamp > entry.ttl * 1000) {
      this.cache.delete(key)
      return null
    }

    return entry.value
  }

  set(key: string, value: ResolvedPrompt, ttl?: number): void {
    if (!this.config.enabled) return

    if (this.config.maxSize && this.cache.size >= this.config.maxSize) {
      const oldestKey = this.findOldestKey()
      if (oldestKey) {
        this.cache.delete(oldestKey)
      }
    }

    this.cache.set(key, {
      value,
      timestamp: Date.now(),
      ttl: ttl || this.config.ttl || 300,
    })
  }

  clearSession(sessionID: string): void {
    for (const key of this.cache.keys()) {
      if (key.includes(sessionID)) {
        this.cache.delete(key)
      }
    }
  }

  clear(): void {
    this.cache.clear()
  }

  private cleanup(): void {
    const now = Date.now()
    let deletedCount = 0

    for (const [key, entry] of this.cache.entries()) {
      if (now - entry.timestamp > entry.ttl * 1000) {
        this.cache.delete(key)
        deletedCount++
      }
    }

    if (deletedCount > 0) {
      promptLogger.info("[PromptCache] Cleaned up expired entries", { deletedCount })
    }
  }

  private findOldestKey(): string | null {
    let oldestKey: string | null = null
    let oldestTime = Date.now()

    for (const [key, entry] of this.cache.entries()) {
      if (entry.timestamp < oldestTime) {
        oldestTime = entry.timestamp
        oldestKey = key
      }
    }

    return oldestKey
  }

  dispose(): void {
    if (this.cleanupInterval) {
      clearInterval(this.cleanupInterval)
    }
    this.cache.clear()
  }
}
