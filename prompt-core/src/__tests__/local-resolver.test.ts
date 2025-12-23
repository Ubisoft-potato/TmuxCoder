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
        experimentsPath: join(__dirname, "../../fixtures/experiments.json"),
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

  it("should allocate variants consistently", async () => {
    const sessionID = "consistent-session"

    const result1 = await resolver.resolve({
      agent: "coder",
      sessionID,
      project: {
        name: "test-project",
        path: "/test/path",
      },
    })
    const result2 = await resolver.resolve({
      agent: "coder",
      sessionID,
      project: {
        name: "test-project",
        path: "/test/path",
      },
    })

    expect(result1.metadata.variantID).toBe(result2.metadata.variantID)
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

  it("should apply experiment variants", async () => {
    // Test with a session that should get variant-a
    const result = await resolver.resolve({
      agent: "coder",
      sessionID: "variant-test-session",
    })

    // Verify experiment metadata is present
    expect(result.metadata.experimentID).toBe("test-exp")
    expect(result.metadata.variantID).toBeTruthy()

    // Temperature should be from variant (0.5 or 0.7)
    expect([0.5, 0.7]).toContain(result.parameters.temperature)
  })
})
