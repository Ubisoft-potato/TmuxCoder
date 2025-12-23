// Export types first
export * from "./types"

// Export base classes
export * from "./cache"
export * from "./resolver"

// Export implementations (after base classes)
export { LocalResolver } from "./local/manager"

// Import for main SDK class
import { ResolverFactory } from "./resolver"
import { PromptCache } from "./cache"
import type { PromptConfig, PromptContext, ResolvedPrompt } from "./types"
import { configurePromptLogger, promptLogger } from "./logger"

/**
 * TmuxCoder Prompt SDK main entry point
 */
export class TmuxCoderPrompts {
  private resolver?: Awaited<ReturnType<typeof ResolverFactory.create>>
  private cache: PromptCache
  private initialized = false

  constructor(private config: PromptConfig) {
    this.cache = new PromptCache(config.cache || { enabled: false })
    configurePromptLogger({
      filePath: config.logging?.filePath,
      debug: config.debug,
    })
  }

  async initialize(): Promise<void> {
    if (this.initialized) return

    // Create resolver (lazy loaded to avoid circular dependency)
    this.resolver = await ResolverFactory.create(this.config)
    await this.resolver.initialize()
    this.initialized = true

    promptLogger.debug("[TmuxCoderPrompts] SDK initialized", {
      mode: this.config.mode,
      cacheEnabled: this.config.cache?.enabled,
    })
  }

  async resolve(context: PromptContext): Promise<ResolvedPrompt> {
    if (!this.initialized || !this.resolver) {
      throw new Error("SDK not initialized. Call initialize() first.")
    }

    const cacheKey = PromptCache.generateKey(
      context.agent,
      context.sessionID,
      context.model?.modelID
    )
    const cached = this.cache.get(cacheKey)

    if (cached) {
      promptLogger.debug("[TmuxCoderPrompts] Cache hit", { cacheKey })
      return cached
    }

    const resolved = await this.resolver.resolve(context)
    this.cache.set(cacheKey, resolved)

    return resolved
  }

  clearSessionCache(sessionID: string): void {
    this.cache.clearSession(sessionID)
  }

  async healthCheck(): Promise<boolean> {
    if (!this.resolver) return false
    return this.resolver.healthCheck()
  }

  async dispose(): Promise<void> {
    if (this.resolver) {
      await this.resolver.dispose()
    }
    this.cache.dispose()
    this.initialized = false
  }
}
