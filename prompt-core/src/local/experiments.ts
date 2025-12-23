import { readFile } from "fs/promises"
import { existsSync } from "fs"
import { promptLogger } from "../logger"

export interface Experiment {
  id: string
  name: string
  enabled: boolean

  // Traffic allocation
  allocation: Record<string, number>

  // Variant configuration
  variants: Record<string, {
    temperature?: number
    topP?: number
    maxTokens?: number
    options?: Record<string, any>
  }>

  // Targeting
  targeting?: {
    agents?: string[]
    users?: string[]
    models?: string[]
  }
}

export interface ExperimentsConfig {
  experiments: Experiment[]
}

export class ExperimentManager {
  private config?: ExperimentsConfig

  constructor(private opts: { configPath: string; debug?: boolean }) {}

  async initialize(): Promise<void> {
    if (!this.opts.configPath || !existsSync(this.opts.configPath)) {
      this.config = { experiments: [] }
      return
    }

    try {
      const content = await readFile(this.opts.configPath, "utf-8")
      this.config = JSON.parse(content)

      if (this.opts.debug) {
        promptLogger.debug("[ExperimentManager] Loaded experiments", {
          count: this.config!.experiments.length,
        })
      }
    } catch (error) {
      promptLogger.error("[ExperimentManager] Failed to load experiments", error)
      this.config = { experiments: [] }
    }
  }

  /**
   * Find matching active experiment
   */
  findActiveExperiment(agent: string, sessionID: string): Experiment | null {
    if (!this.config || this.config.experiments.length === 0) {
      return null
    }

    return this.config.experiments.find((exp) => {
      // Must be enabled
      if (!exp.enabled) return false

      // Check agent targeting
      if (exp.targeting?.agents && !exp.targeting.agents.includes(agent)) {
        return false
      }

      // Can add more targeting logic here

      return true
    }) || null
  }

  /**
   * Allocate variant for session (ensures consistency)
   */
  allocateVariant(experiment: Experiment, sessionID: string): string {
    const hash = this.hashString(sessionID + experiment.id)
    const allocation = experiment.allocation

    let cumulative = 0
    for (const [variant, probability] of Object.entries(allocation)) {
      cumulative += probability
      if (hash < cumulative) {
        return variant
      }
    }

    return "control"
  }

  /**
   * Simple hash function (ensures same session always gets same variant)
   */
  private hashString(str: string): number {
    let hash = 0
    for (let i = 0; i < str.length; i++) {
      const char = str.charCodeAt(i)
      hash = (hash << 5) - hash + char
      hash = hash & hash
    }
    return Math.abs(hash) / 0x7fffffff
  }
}
