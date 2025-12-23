import type { Plugin } from "@opencode-ai/plugin"
import { TmuxCoderPrompts } from "@tmuxcoder/prompt-core"
import type { PromptConfig, PromptContext } from "@tmuxcoder/prompt-core"
import { join } from "path"
import { existsSync } from "fs"
import { promptProxyLogger as logger, parseLogLevel } from "./logger"
import {
  loadCustomProviders,
  resolveContextVariables,
  setProviderConfig,
  getAvailableVariables,
} from "./variable-providers"

// ========== Monkey Patch SystemPrompt ==========
// Import SystemPrompt module for monkey patching
let SystemPrompt: any = null
let monkeyPatchApplied = false

try {
  const systemModule = await import(
    "../../packages/opencode/packages/opencode/src/session/system.ts"
  )
  SystemPrompt = systemModule.SystemPrompt

  // Preserve originals (for debugging)
  const originalEnvironment = SystemPrompt.environment
  const originalCustom = SystemPrompt.custom

  // Replace with no-op functions
  SystemPrompt.environment = async function () {
    logger.debug("SystemPrompt.environment() intercepted - returning empty", { module: "SystemPrompt" })
    return []
  }

  SystemPrompt.custom = async function () {
    logger.debug("SystemPrompt.custom() intercepted - returning empty", { module: "SystemPrompt" })
    return []
  }

  monkeyPatchApplied = true
  logger.info("Monkey patch applied successfully", { module: "SystemPrompt" })
} catch (error) {
  logger.warn("Failed to import/patch SystemPrompt - continuing without monkey patch", {
    module: "SystemPrompt",
    error: error instanceof Error ? error.message : String(error)
  })
}
// ========== End Monkey Patch ==========

export const PromptProxy: Plugin = async ({ project, directory, worktree, $ }) => {
  logger.info("Plugin bootstrap", {
    monkeyPatchActive: monkeyPatchApplied,
    directory,
    worktree,
  })
  // Find the directory containing .opencode/prompts
  let configRoot = worktree

  // First try to find git super-project root (for submodules)
  try {
    const result = await $`git -C ${worktree} rev-parse --show-superproject-working-tree`.text()
    const superProject = result.trim()
    if (superProject) {
      configRoot = superProject
      logger.info("Using super-project root", { configRoot })
    }
  } catch (error) {
    // Not a submodule, continue
  }

  // If no super-project, search upwards for .opencode/prompts
  if (configRoot === worktree) {
    let current = worktree
    let found = false

    // Search up to 5 levels
    for (let i = 0; i < 5; i++) {
      const testPath = join(current, ".opencode/prompts")
      if (existsSync(testPath)) {
        configRoot = current
        found = true
        logger.info("Found .opencode/prompts", { configRoot })
        break
      }

      const parent = join(current, "..")
      if (parent === current) break // Reached root
      current = parent
    }

    if (!found) {
      logger.warn(".opencode/prompts not found, using worktree", { worktree })
    }
  }

  // Load configuration from found root
  const config = await loadConfig(configRoot)

  // Configure providers based on config
  if (config.providers) {
    setProviderConfig(config.providers)
    logger.info("Provider configuration applied", { providers: config.providers })
  }

  // Store configRoot for use in hooks
  const projectRoot = configRoot

  // Load custom providers (respecting config.providers.custom settings)
  const customConfig = config.providers?.custom
  const providersDir = customConfig?.directory
    ? join(projectRoot, customConfig.directory)
    : join(projectRoot, ".opencode/prompts/providers")

  const customProviders = (customConfig?.enabled !== false)
    ? await loadCustomProviders(providersDir)
    : {}
  const customProviderCount = Object.keys(customProviders).length

  // Configure logger based on config
  if (config.logging?.level) {
    logger.setLevel(parseLogLevel(config.logging.level))
  }

  // Initialize SDK
  const prompts = new TmuxCoderPrompts(config)
  await prompts.initialize()

  // Get available variables and log for user visibility
  const availableVars = getAvailableVariables()

  logger.info("Initialized", {
    mode: config.mode,
    templatesDir: config.local?.templatesDir,
    cacheEnabled: config.cache?.enabled,
    customProviders: customProviderCount,
    providersEnabled: {
      git: config.providers?.git?.enabled !== false,
      time: config.providers?.time?.enabled !== false,
      system: config.providers?.system?.enabled !== false,
      custom: customConfig?.enabled !== false,
    },
  })

  // Log available variables for easy discovery
  logger.info("📋 Available template variables", {
    total: availableVars.total + customProviderCount,
    builtIn: availableVars.builtIn,
    customProviders: Object.keys(customProviders),
  })

  logger.info("💡 Quick tips", {
    variablesReference: ".opencode/prompts/VARIABLES.txt",
    templateComments: "Check .opencode/prompts/templates/*.txt for inline help",
    fullDocumentation: "docs/AVAILABLE_VARIABLES.md",
  })

  // Create parameter cache (for passing data between hooks)
  const parameterCache = new Map<string, any>()

  return {
    /**
     * Hook 1: Customize system prompt
     */
    "chat.message": async (input, output) => {
      const { sessionID, agent = "default", model } = input
      const sessionIDShort = sessionID.substring(0, 8)

      logger.debug("chat.message hook called", {
        sessionID: sessionIDShort,
        agent,
        modelID: model?.modelID,
      })

      // Overall timeout for the entire hook (15 seconds)
      const hookTimeout = 15000

      const executeHook = async () => {
        // Resolve custom variables (built-ins + custom)
        const customContext = await resolveContextVariables(
          {
            worktree: projectRoot,
            $: $,
            env: process.env,
            sessionID,
          },
          customProviders
        )

        // Build context
        const context: PromptContext = {
          agent,
          sessionID,
          project: {
            name: getProjectName(projectRoot),
            path: projectRoot,
          },
          model: model
            ? {
              providerID: model.providerID,
              modelID: model.modelID,
            }
            : undefined,
          environment: {
            NODE_ENV: process.env.NODE_ENV,
            sessionDirectory: directory,
            ...customContext,
          },
        }

        logger.debug("Context built", {
          sessionID: sessionIDShort,
          projectName: context.project?.name,
          projectPath: context.project?.path,
          gitBranch: customContext.git_branch,
          gitDirty: customContext.git_dirty,
          customProviders: customProviderCount,
          sessionDirectory: directory,
        })

        // Resolve Prompt with timeout
        const startTime = Date.now()
        const resolvePromise = prompts.resolve(context)
        const resolved = await Promise.race([
          resolvePromise,
          new Promise((_, reject) =>
            setTimeout(() => reject(new Error("Prompt resolution timeout")), 10000)
          )
        ]) as Awaited<ReturnType<typeof prompts.resolve>>
        const resolutionTime = Date.now() - startTime

        const userPromptFull = extractUserPrompt(output.parts)

        logger.info("Prompt resolved", {
          sessionID: sessionIDShort,
          templateID: resolved.metadata.templateID,
          variantID: resolved.metadata.variantID,
          systemPromptLength: resolved.system.length,
          userPromptLength: userPromptFull.length,
          resolverType: resolved.metadata.resolverType,
        })

        if (config.debug) {
          logger.debug("Prompt details", {
            sessionID: sessionIDShort,
            systemPromptFull: resolved.system,
            userPromptFull,
            parameters: resolved.parameters,
            metadata: resolved.metadata,
          })
        }

        // Performance metric
        logger.metric("prompt_resolution_time", resolutionTime, "ms", {
          sessionID: sessionIDShort,
          templateID: resolved.metadata.templateID,
        })

        // Apply to output
        output.message.system = resolved.system

        logger.info("System prompt overridden successfully", {
          sessionID: sessionIDShort,
          agent,
          templateID: resolved.metadata.templateID,
        })

        // Cache parameters for chat.params hook
        parameterCache.set(sessionID, resolved.parameters)
      }

      // Race between hook execution and timeout
      try {
        await Promise.race([
          executeHook(),
          new Promise((_, reject) =>
            setTimeout(() => reject(new Error("Hook execution timeout")), hookTimeout)
          )
        ])
      } catch (error) {
        logger.error("Hook timeout or fatal error", error instanceof Error ? error : undefined, {
          sessionID: sessionIDShort,
          hookTimeout,
        })
        // Continue without applying custom prompt
      }
    },

    /**
     * Hook 2: Customize model parameters
     */
    "chat.params": async (input, output) => {
      const { sessionID } = input
      const sessionIDShort = sessionID.substring(0, 8)

      try {
        const params = parameterCache.get(sessionID)

        if (params) {
          if (params.temperature !== undefined) {
            output.temperature = params.temperature
          }
          if (params.topP !== undefined) {
            output.topP = params.topP
          }
          if (params.options) {
            output.options = {
              ...output.options,
              ...params.options,
            }
          }

          logger.debug("Applied parameters", {
            sessionID: sessionIDShort,
            temperature: params.temperature,
            topP: params.topP,
            hasOptions: !!params.options,
          })
        }
      } catch (error) {
        logger.error("Error in chat.params hook", error instanceof Error ? error : undefined, {
          sessionID: sessionIDShort,
        })
      }
    },

    /**
     * Hook 3: Listen to events (optional)
     */
    event: async ({ event }) => {
      if (event.type === "session.completed" || event.type === "session.deleted") {
        const sessionID = (event as any).sessionID
        if (sessionID) {
          const sessionIDShort = sessionID.substring(0, 8)
          parameterCache.delete(sessionID)
          prompts.clearSessionCache(sessionID)
          logger.debug("Session cleanup", {
            sessionID: sessionIDShort,
            eventType: event.type,
          })
        }
      }
    },
  }
}

type BasicTextPart = {
  type?: string
  text?: string
  synthetic?: boolean
}

function extractUserPrompt(parts: BasicTextPart[] = []): string {
  if (!Array.isArray(parts) || parts.length === 0) {
    return ""
  }

  return parts
    .filter((part): part is BasicTextPart & { text: string } =>
      part?.type === "text" && !part.synthetic && typeof part.text === "string"
    )
    .map((part) => part.text.trim())
    .filter((text) => text.length > 0)
    .join("\n\n")
}

/**
 * Load configuration file
 */
async function loadConfig(directory: string): Promise<PromptConfig> {
  const configPath = join(directory, ".opencode/prompts/config.json")

  const defaultConfig: PromptConfig = {
    mode: "local",
    local: {
      templatesDir: join(directory, ".opencode/prompts/templates"),
      parametersPath: join(directory, ".opencode/prompts/parameters.json"),
      experimentsPath: join(directory, ".opencode/prompts/experiments.json"),
    },
    cache: {
      enabled: true,
      ttl: 300,
      maxSize: 100,
    },
    debug: process.env.TMUXCODER_DEBUG === "true",
  }

  if (existsSync(configPath)) {
    try {
      const content = await Bun.file(configPath).text()
      const userConfig = JSON.parse(content)

      return {
        ...defaultConfig,
        ...userConfig,
        local: {
          ...defaultConfig.local,
          ...userConfig.local,
        },
        cache: {
          ...defaultConfig.cache,
          ...userConfig.cache,
        },
        logging: {
          ...defaultConfig.logging,
          ...userConfig.logging,
        },
      }
    } catch (error) {
      logger.warn("Failed to load config, using defaults", {
        configPath,
        error: error instanceof Error ? error.message : String(error),
      })
    }
  }

  return defaultConfig
}


/**
 * Extract project name from directory path
 */
function getProjectName(dirPath: string): string {
  const parts = dirPath.split("/")
  return parts[parts.length - 1] || "unknown"
}
