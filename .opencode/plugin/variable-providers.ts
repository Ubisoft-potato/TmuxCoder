import { existsSync } from "fs"
import { readdir } from "fs/promises"
import { basename, extname, join } from "path"
import { pathToFileURL } from "url"
import { promptProxyLogger as logger } from "./logger"

export interface VariableProviderContext {
  worktree: string
  $: any
  env: NodeJS.ProcessEnv
  sessionID: string
}

export type VariableProvider = (
  ctx: VariableProviderContext
) => Promise<Record<string, any>> | Record<string, any>

export type ProviderRegistry = Record<string, VariableProvider>

/**
 * Provider configuration options
 */
export interface ProviderConfig {
  git?: {
    enabled?: boolean
    timeout?: number
    cache?: {
      git_root?: number
      git_branch?: number
      git_dirty?: number
    }
  }
  time?: {
    enabled?: boolean
  }
  system?: {
    enabled?: boolean
  }
  custom?: {
    enabled?: boolean
    directory?: string
    namespace?: string
    watch?: boolean
  }
}

/**
 * Default provider configuration
 */
export const DEFAULT_PROVIDER_CONFIG: Required<ProviderConfig> = {
  git: {
    enabled: true,
    timeout: 5000,
    cache: {
      git_root: 3600000,    // 1 hour
      git_branch: 30000,    // 30 seconds
      git_dirty: 5000,      // 5 seconds
    },
  },
  time: {
    enabled: true,
  },
  system: {
    enabled: true,
  },
  custom: {
    enabled: true,
    directory: ".opencode/prompts/providers",
    namespace: "",
    watch: false,
  },
}

// Global provider configuration (can be overridden)
let providerConfig: Required<ProviderConfig> = { ...DEFAULT_PROVIDER_CONFIG }

/**
 * Set provider configuration
 */
export function setProviderConfig(config: ProviderConfig): void {
  providerConfig = {
    git: {
      ...DEFAULT_PROVIDER_CONFIG.git,
      ...config.git,
      cache: {
        ...DEFAULT_PROVIDER_CONFIG.git.cache,
        ...config.git?.cache,
      },
    },
    time: { ...DEFAULT_PROVIDER_CONFIG.time, ...config.time },
    system: { ...DEFAULT_PROVIDER_CONFIG.system, ...config.system },
    custom: { ...DEFAULT_PROVIDER_CONFIG.custom, ...config.custom },
  }
  logger.info("Provider configuration updated", { config: providerConfig })
}

/**
 * Get current provider configuration
 */
export function getProviderConfig(): Required<ProviderConfig> {
  return providerConfig
}

/**
 * Get list of all available variables from enabled providers
 */
export function getAvailableVariables(): {
  builtIn: { [provider: string]: string[] }
  custom: string[]
  total: number
  customNamespace?: string
} {
  const builtInVars: { [provider: string]: string[] } = {}
  let totalCount = 0

  if (providerConfig.git.enabled) {
    builtInVars.git = ["git_branch", "git_dirty", "git_root"]
    totalCount += 3
  }

  if (providerConfig.time.enabled) {
    builtInVars.time = ["timestamp", "time_human", "date_ymd", "time_hms"]
    totalCount += 4
  }

  if (providerConfig.system.enabled) {
    builtInVars.system = ["os_platform", "node_env"]
    totalCount += 2
  }

  // Context variables (always available)
  builtInVars.context = [
    "project_name",
    "project_path",
    "sessionDirectory",
    "model_id",
    "model_provider",
  ]
  totalCount += 5

  return {
    builtIn: builtInVars,
    custom: [],  // Will be populated with custom provider variables
    total: totalCount,
    customNamespace: providerConfig.custom.namespace?.trim()
      ? formatNamespace(providerConfig.custom.namespace)
      : undefined,
  }
}

/**
 * Variable cache entry
 */
interface CachedVariable {
  value: any
  expiresAt: number
  ttl: number
}

/**
 * Simple TTL-based cache for variable values
 */
class VariableCache {
  private cache = new Map<string, CachedVariable>()

  /**
   * Get cached value if not expired
   */
  get(key: string): any | undefined {
    const cached = this.cache.get(key)
    if (!cached) return undefined

    if (Date.now() > cached.expiresAt) {
      this.cache.delete(key)
      return undefined
    }

    return cached.value
  }

  /**
   * Set cached value with TTL
   * @param key Cache key
   * @param value Value to cache
   * @param ttl Time to live in milliseconds
   */
  set(key: string, value: any, ttl: number): void {
    this.cache.set(key, {
      value,
      expiresAt: Date.now() + ttl,
      ttl,
    })
  }

  /**
   * Clear all cached values
   */
  clear(): void {
    this.cache.clear()
  }

  /**
   * Get cache statistics
   */
  getStats(): { size: number; keys: string[] } {
    return {
      size: this.cache.size,
      keys: Array.from(this.cache.keys()),
    }
  }
}

// Global cache instance shared across providers
const variableCache = new VariableCache()

/**
 * Git Provider: Extracts robust git information with caching
 * Uses configuration from providerConfig.git
 */
export const gitProvider: VariableProvider = async ({ worktree, $ }) => {
  const config = providerConfig.git

  try {
    const timeoutMs = config.timeout

    // Cache keys based on worktree
    const rootCacheKey = `git_root:${worktree}`
    const branchCacheKey = `git_branch:${worktree}`
    const dirtyCacheKey = `git_dirty:${worktree}`

    // git_root: Cache with configured TTL
    let gitRoot = variableCache.get(rootCacheKey)
    if (gitRoot === undefined) {
      gitRoot = worktree
      variableCache.set(rootCacheKey, gitRoot, config.cache.git_root)
    }

    // git_branch: Cache with configured TTL
    let gitBranch = variableCache.get(branchCacheKey)
    if (gitBranch === undefined) {
      const branchPromise = $`git -C ${worktree} branch --show-current`.text()
      const branch = await Promise.race([
        branchPromise,
        new Promise((_, reject) =>
          setTimeout(() => reject(new Error("Git branch command timeout")), timeoutMs)
        ),
      ]) as string

      gitBranch = branch.trim()
      variableCache.set(branchCacheKey, gitBranch, config.cache.git_branch)
      logger.debug("Git branch fetched and cached", {
        branch: gitBranch,
        ttl: `${config.cache.git_branch}ms`
      })
    } else {
      logger.debug("Git branch from cache", { branch: gitBranch })
    }

    // git_dirty: Cache with configured TTL
    let gitDirty = variableCache.get(dirtyCacheKey)
    if (gitDirty === undefined) {
      const statusPromise = $`git -C ${worktree} status --short`.text()
      const status = await Promise.race([
        statusPromise,
        new Promise((_, reject) =>
          setTimeout(() => reject(new Error("Git status command timeout")), timeoutMs)
        ),
      ]) as string

      gitDirty = status.trim().length > 0
      variableCache.set(dirtyCacheKey, gitDirty, config.cache.git_dirty)
      logger.debug("Git status fetched and cached", {
        dirty: gitDirty,
        ttl: `${config.cache.git_dirty}ms`
      })
    } else {
      logger.debug("Git status from cache", { dirty: gitDirty })
    }

    return {
      git_branch: gitBranch,
      git_dirty: gitDirty,
      git_root: gitRoot,
    }
  } catch (error) {
    logger.warn("Git provider failed", { error: String(error) })
    return {
      git_branch: "unknown",
      git_dirty: false,
      git_root: worktree,
    }
  }
}

/**
 * Time Provider: Explicitly defines time variables
 * Optimized to call toISOString() only once
 */
export const timeProvider: VariableProvider = () => {
  const now = new Date()
  const isoString = now.toISOString()
  const [datePart, timePart] = isoString.split("T")
  const [timeWithoutMs] = timePart.split(".")

  return {
    timestamp: isoString,
    time_human: now.toLocaleString(),
    date_ymd: datePart,
    time_hms: timeWithoutMs,
  }
}

/**
 * System/Env Provider
 */
export const systemProvider: VariableProvider = ({ env }) => {
  return {
    os_platform: process.platform,
    node_env: env.NODE_ENV || "development",
  }
}

const builtInProviders: ProviderRegistry = {
  git: gitProvider,
  time: timeProvider,
  system: systemProvider,
}

/**
 * Main resolver function
 */
export async function resolveContextVariables(
  ctx: VariableProviderContext,
  extraProviders: ProviderRegistry = {}
): Promise<Record<string, any>> {
  // Filter built-in providers based on configuration
  const enabledBuiltInProviders: ProviderRegistry = {}

  if (providerConfig.git.enabled) {
    enabledBuiltInProviders.git = builtInProviders.git
  }
  if (providerConfig.time.enabled) {
    enabledBuiltInProviders.time = builtInProviders.time
  }
  if (providerConfig.system.enabled) {
    enabledBuiltInProviders.system = builtInProviders.system
  }

  const providerEntries = Object.entries({
    ...enabledBuiltInProviders,
    ...extraProviders,
  })
  const customProviderNames = new Set(Object.keys(extraProviders))
  const customNamespace = providerConfig.custom.namespace?.trim()

  logger.debug("Resolving variables with providers", {
    enabled: Object.keys(enabledBuiltInProviders),
    custom: Object.keys(extraProviders),
    customNamespace: customNamespace ? formatNamespace(customNamespace) : undefined,
  })

  const results = await Promise.allSettled(
    providerEntries.map(async ([name, provider]) => {
      try {
        const start = Date.now()
        const data = await provider(ctx)
        const duration = Date.now() - start

        // Validate return value
        if (!data || typeof data !== "object" || Array.isArray(data)) {
          logger.warn(`Provider '${name}' returned invalid value (expected object)`, {
            provider: name,
            type: typeof data,
            isArray: Array.isArray(data),
            duration,
          })
          return {}
        }

        logger.debug(`Provider '${name}' finished`, { duration })
        return data
      } catch (err) {
        logger.error(
          `Provider '${name}' failed`,
          err instanceof Error ? err : undefined,
          { provider: name }
        )
        return {}
      }
    })
  )

  return results.reduce((acc, result, index) => {
    if (result.status === "fulfilled") {
      const [providerName] = providerEntries[index]
      const data = result.value

      // Detect variable name conflicts
      Object.keys(data).forEach((key) => {
        if (key in acc) {
          logger.warn(`Variable name conflict detected`, {
            variable: key,
            provider: providerName,
            previousValue: acc[key],
            newValue: data[key],
          })
        }
      })

      const isCustomProvider = customProviderNames.has(providerName)
      const normalizedData = isCustomProvider
        ? applyNamespaceToVariables(data, customNamespace)
        : data

      return { ...acc, ...normalizedData }
    }
    return acc
  }, {} as Record<string, any>)
}

function applyNamespaceToVariables(
  data: Record<string, any>,
  namespace?: string | null
): Record<string, any> {
  if (!namespace?.trim()) {
    return { ...data }
  }

  const prefix = formatNamespace(namespace)
  return Object.entries(data).reduce<Record<string, any>>((acc, [key, value]) => {
    acc[`${prefix}${key}`] = value
    return acc
  }, {})
}

function formatNamespace(namespace: string): string {
  const trimmed = namespace.trim()
  if (!trimmed) return ""
  return trimmed.endsWith("_") ? trimmed : `${trimmed}_`
}

/**
 * Load provider modules from a directory like .opencode/prompts/providers
 */
export async function loadCustomProviders(directory: string): Promise<ProviderRegistry> {
  const registry: ProviderRegistry = {}

  if (!directory || !existsSync(directory)) {
    return registry
  }

  try {
    const entries = await readdir(directory, { withFileTypes: true })

    for (const entry of entries) {
      if (!entry.isFile()) continue

      const ext = extname(entry.name).toLowerCase()
      if (![".ts", ".js", ".mjs", ".cjs"].includes(ext)) continue

      const fullPath = join(directory, entry.name)

      try {
        const moduleUrl = pathToFileURL(fullPath).href
        const mod = await import(moduleUrl)
        const providerFn: VariableProvider | undefined = mod.provider ?? mod.default

        if (typeof providerFn !== "function") {
          logger.warn("Custom provider missing function export", { path: fullPath })
          continue
        }

        const providerName: string =
          mod.name ?? mod.providerName ?? basename(entry.name, ext)

        if (registry[providerName]) {
          logger.warn("Custom provider name already registered, skipping duplicate", {
            providerName,
            path: fullPath,
          })
          continue
        }

        registry[providerName] = providerFn
        logger.info("Registered custom variable provider", {
          providerName,
          path: fullPath,
        })
      } catch (error) {
        logger.error(
          "Failed to load custom provider",
          error instanceof Error ? error : undefined,
          { path: fullPath }
        )
      }
    }
  } catch (error) {
    logger.error(
      "Failed to read custom provider directory",
      error instanceof Error ? error : undefined,
      { directory }
    )
  }

  return registry
}
