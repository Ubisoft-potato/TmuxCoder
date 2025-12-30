import { readFile } from "fs/promises"
import { existsSync } from "fs"
import { promptLogger } from "../logger"

export interface ParametersConfig {
  defaults?: {
    temperature?: number
    topP?: number
    maxTokens?: number
  }

  agents?: Record<string, {
    temperature?: number
    topP?: number
    maxTokens?: number
    options?: Record<string, any>
  }>

  models?: Record<string, {
    temperature?: number
    topP?: number
    maxTokens?: number
  }>
}

export class ParameterManager {
  private config?: ParametersConfig

  constructor(private opts: { configPath: string; debug?: boolean }) {}

  async initialize(): Promise<void> {
    if (!this.opts.configPath || !existsSync(this.opts.configPath)) {
      this.config = {
        defaults: {
          temperature: 0.7,
          topP: 0.9,
        },
      }
      return
    }

    try {
      const content = await readFile(this.opts.configPath, "utf-8")
      this.config = JSON.parse(content)

      if (this.opts.debug) {
        promptLogger.debug("[ParameterManager] Loaded parameters config")
      }
    } catch (error) {
      promptLogger.error("[ParameterManager] Failed to load parameters", error)
      this.config = { defaults: { temperature: 0.7, topP: 0.9 } }
    }
  }

  /**
   * Resolve parameters (priority: agent > model > global defaults)
   */
  resolve(opts: {
    agent: string
    model?: string
  }): {
    temperature?: number
    topP?: number
    maxTokens?: number
    options?: Record<string, any>
  } {
    const { agent, model } = opts

    // Start with global defaults
    let params = { ...this.config?.defaults }

    // Apply model config
    if (model && this.config?.models?.[model]) {
      params = { ...params, ...this.config.models[model] }
    }

    // Apply agent config
    if (this.config?.agents?.[agent]) {
      params = { ...params, ...this.config.agents[agent] }
    }

    return params
  }
}
