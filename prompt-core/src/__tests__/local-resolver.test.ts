import { describe, it, expect, beforeEach } from "vitest"
import { LocalResolver } from "../local/manager"
import type { PromptConfig } from "../types"
import { join } from "path"

describe("LocalResolver", () => {
  let resolver: LocalResolver
  let config: PromptConfig

  beforeEach(async () => {
    config = {
      mode: "local",
      local: {
        templatesDir: join(__dirname, "../../fixtures/templates"),
        parametersPath: join(__dirname, "../../fixtures/parameters.json"),
      },
      debug: false,
    }

    resolver = new LocalResolver(config)
    await resolver.initialize()
  })

  it("should initialize successfully", async () => {
    const healthy = await resolver.healthCheck()
    expect(healthy).toBe(true)
  })

  it("should resolve basic prompt", async () => {
    const result = await resolver.resolve({
      agent: "coder",
      sessionID: "test-session",
      project: {
        name: "test-project",
        path: "/test/path",
      },
    })

    expect(result.system).toBeTruthy()
    expect(result.system).toContain("test-project")
    expect(result.metadata.templateID).toBe("coder")
    expect(result.metadata.resolverType).toBe("local")
  })

  it("should apply agent parameters", async () => {
    const result = await resolver.resolve({
      agent: "reviewer",
      sessionID: "test-session",
    })

    expect(result.parameters.temperature).toBe(0.3)
    expect(result.parameters.topP).toBe(0.95)
  })

  it("should fallback to default template if not found", async () => {
    const result = await resolver.resolve({
      agent: "nonexistent",
      sessionID: "test-session",
      project: {
        name: "test-project",
        path: "/test/path",
      },
    })

    expect(result.system).toBeTruthy()
    expect(result.system).toContain("test-project")
  })

  it("should handle missing optional config gracefully", async () => {
    const minimalConfig: PromptConfig = {
      mode: "local",
      local: {
        templatesDir: join(__dirname, "../../fixtures/templates"),
      },
    }

    const minimalResolver = new LocalResolver(minimalConfig)
    await minimalResolver.initialize()

    const result = await minimalResolver.resolve({
      agent: "coder",
      sessionID: "test-session",
    })

    expect(result.system).toBeTruthy()
    expect(result.parameters.temperature).toBe(0.7) // default
  })

  it("should enrich context with runtime information", async () => {
    const result = await resolver.resolve({
      agent: "coder",
      sessionID: "test-session",
      project: {
        name: "my-project",
        path: "/path/to/project",
      },
      git: {
        branch: "main",
        isDirty: true,
      },
      model: {
        providerID: "anthropic",
        modelID: "claude-sonnet-4",
      },
    })

    expect(result.system).toContain("my-project")
    expect(result.system).toContain("claude-sonnet-4")
  })
})
