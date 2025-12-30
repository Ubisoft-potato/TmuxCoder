import { PromptResolver } from "../resolver"
import type { PromptContext, ResolvedPrompt, PromptConfig } from "../types"
import { TemplateEngine } from "./template"
import { ParameterManager } from "./parameters"
import { promptLogger } from "../logger"

export class LocalResolver extends PromptResolver {
  private templateEngine!: TemplateEngine
  private parameterManager!: ParameterManager
  private initialized = false

  constructor(config: PromptConfig) {
    super(config)

    if (!config.local) {
      throw new Error("Local config is required for LocalResolver")
    }
  }

  async initialize(): Promise<void> {
    if (this.initialized) return

    const { local } = this.config

    // Initialize template engine
    this.templateEngine = new TemplateEngine({
      templatesDir: local!.templatesDir,
      debug: this.config.debug,
    })
    await this.templateEngine.initialize()

    // Initialize parameter manager
    if (local!.parametersPath) {
      this.parameterManager = new ParameterManager({
        configPath: local!.parametersPath,
        debug: this.config.debug,
      })
      await this.parameterManager.initialize()
    } else {
      this.parameterManager = new ParameterManager({ configPath: "" })
      await this.parameterManager.initialize()
    }

    this.initialized = true

    if (this.config.debug) {
      promptLogger.debug("[LocalResolver] Initialized successfully")
    }
  }

  async resolve(context: PromptContext): Promise<ResolvedPrompt> {
    if (!this.initialized) {
      throw new Error("LocalResolver not initialized. Call initialize() first.")
    }

    const startTime = Date.now()

    try {
      // 1. Enrich context (add runtime information)
      const enrichedContext = await this.enrichContext(context)

      // 2. Resolve template
      const systemPrompt = await this.templateEngine.render(
        context.agent,
        enrichedContext
      )

      // 3. Resolve parameters
      const parameters = this.parameterManager.resolve({
        agent: context.agent,
        model: context.model?.modelID,
      })

      // 4. Build result
      const resolved: ResolvedPrompt = {
        system: systemPrompt,
        parameters,
        metadata: {
          templateID: context.agent,
          resolverType: "local",
          resolvedAt: new Date().toISOString(),
        },
      }

      if (this.config.debug) {
        const elapsed = Date.now() - startTime
        promptLogger.debug("[LocalResolver] Resolved prompt", {
          agent: context.agent,
          temperature: parameters.temperature,
          durationMs: elapsed,
        })
      }

      return resolved

    } catch (error) {
      promptLogger.error("[LocalResolver] Failed to resolve prompt", error)

      // Return safe default
      return this.getFallbackPrompt(context)
    }
  }

  /**
   * Enrich context (add runtime information)
   */
  private async enrichContext(context: PromptContext): Promise<Record<string, any>> {
    return {
      // Basic info
      agent: context.agent,
      sessionID: context.sessionID,

      // Project info
      project_name: context.project?.name || "unknown",
      project_path: context.project?.path || "",

      // Git info
      git_branch: context.git?.branch || "unknown",
      git_dirty: context.git?.isDirty || false,
      git_commit: context.git?.commitHash?.substring(0, 7) || "",

      // Model info
      model_provider: context.model?.providerID || "unknown",
      model_id: context.model?.modelID || "unknown",

      // Timestamp
      timestamp: new Date().toISOString(),
      date: new Date().toLocaleDateString(),
      time: new Date().toLocaleTimeString(),

      // User info (if available)
      user_id: context.user?.id,
      user_email: context.user?.email,

      // Custom environment variables
      ...context.environment,
    }
  }

  /**
   * Fallback: return minimal usable prompt
   */
  private getFallbackPrompt(context: PromptContext): ResolvedPrompt {
    return {
      system: `You are an AI assistant for the ${context.project?.name || "project"}.`,
      parameters: {
        temperature: 0.7,
        topP: 0.9,
      },
      metadata: {
        templateID: "fallback",
        resolverType: "local",
        resolvedAt: new Date().toISOString(),
      },
    }
  }

  async healthCheck(): Promise<boolean> {
    return this.initialized
  }
}
