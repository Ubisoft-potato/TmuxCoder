import type { PromptContext, ResolvedPrompt, PromptConfig } from "./types"

/**
 * Prompt Resolver abstract interface
 */
export abstract class PromptResolver {
  constructor(protected config: PromptConfig) {}

  /**
   * Initialize Resolver
   */
  abstract initialize(): Promise<void>

  /**
   * Resolve Prompt
   */
  abstract resolve(context: PromptContext): Promise<ResolvedPrompt>

  /**
   * Release resources (optional)
   */
  async dispose(): Promise<void> {}

  /**
   * Health check (optional)
   */
  async healthCheck(): Promise<boolean> {
    return true
  }
}

/**
 * Resolver Factory
 */
export class ResolverFactory {
  static async create(config: PromptConfig): Promise<PromptResolver> {
    switch (config.mode) {
      case "local": {
        // Lazy import to avoid circular dependency
        const { LocalResolver } = await import("./local/manager.js")
        return new LocalResolver(config)
      }

      case "remote":
        throw new Error("Remote mode not implemented yet")

      case "hybrid":
        throw new Error("Hybrid mode not implemented yet")

      default:
        throw new Error(`Unknown mode: ${config.mode}`)
    }
  }
}
