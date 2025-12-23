/**
 * Context information (platform-agnostic)
 */
export interface PromptContext {
  // Required fields
  agent: string                    // "coder", "reviewer", "debugger"
  sessionID: string

  // Optional fields
  project?: {
    name: string
    path: string
  }
  git?: {
    branch: string
    isDirty: boolean
    commitHash?: string
  }
  model?: {
    providerID: string             // "anthropic", "openai"
    modelID: string                // "claude-sonnet-4"
  }
  user?: {
    id: string
    email?: string
  }
  environment?: Record<string, any>
}

/**
 * Resolved Prompt configuration
 */
export interface ResolvedPrompt {
  // System prompt
  system: string

  // Model parameters
  parameters: {
    temperature?: number
    topP?: number
    maxTokens?: number
    options?: Record<string, any>
  }

  // Metadata
  metadata: {
    templateID: string             // Template ID used
    templateVersion?: string       // Template version
    variantID?: string             // A/B test variant
    experimentID?: string          // Experiment ID
    resolverType: "local" | "remote" | "hybrid"
    resolvedAt: string             // ISO timestamp
  }
}

/**
 * Configuration mode
 */
export type PromptMode = "local" | "remote" | "hybrid"

/**
 * Prompt configuration
 */
export interface PromptConfig {
  mode: PromptMode

  // Local configuration
  local?: {
    templatesDir: string
    experimentsPath?: string
    parametersPath?: string
  }

  // Remote configuration (reserved for future use)
  remote?: {
    url: string
    apiKey?: string
    timeout?: number
    fallback?: "local" | "error"
  }

  // Cache configuration
  cache?: {
    enabled: boolean
    ttl?: number                    // seconds
    maxSize?: number                // entries
  }

  logging?: {
    level?: string                  // "debug" | "info" | "warn" | "error"
    filePath?: string
    enableMetrics?: boolean
    enableTracing?: boolean
  }

  // Provider configuration
  providers?: {
    git?: {
      enabled?: boolean
      timeout?: number              // milliseconds
      cache?: {
        git_root?: number           // TTL in milliseconds
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
      directory?: string            // Custom providers directory
      namespace?: string            // Namespace prefix for custom variables
      watch?: boolean               // Enable hot-reload (experimental)
    }
  }

  // Debug mode
  debug?: boolean
}
