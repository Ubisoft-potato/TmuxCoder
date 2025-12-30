#!/usr/bin/env ts-node
/**
 * Quick Validation Script
 * Tests key architecture requirements
 */

import { TmuxCoderPrompts } from "../src/index"
import { join } from "path"

const PASS = "✅"
const FAIL = "❌"
const WARN = "⚠️"

async function main() {
  console.log("🧪 Quick Validation - Prompt Core\n")
  console.log("=" .repeat(60))

  let passed = 0
  let failed = 0
  let warnings = 0

  // Test 1: SDK Initialization
  console.log("\n1️⃣  Testing SDK Initialization...")
  try {
    const sdk = new TmuxCoderPrompts({
      mode: "local",
      local: {
        templatesDir: join(__dirname, "../fixtures/templates"),
        parametersPath: join(__dirname, "../fixtures/parameters.json"),
      },
      cache: { enabled: true, ttl: 300 },
    })
    await sdk.initialize()
    console.log(`${PASS} SDK initialized successfully`)
    passed++
  } catch (error) {
    console.log(`${FAIL} SDK initialization failed:`, error)
    failed++
    return
  }

  // Test 2: Session State Management
  console.log("\n2️⃣  Testing Session State Management...")
  try {
    const sdk = new TmuxCoderPrompts({
      mode: "local",
      local: {
        templatesDir: join(__dirname, "../fixtures/templates"),
      },
      cache: { enabled: true, ttl: 1 },  // 1 second TTL
    })
    await sdk.initialize()

    const sessionID = "test-session"

    // First request (cache miss)
    const start1 = performance.now()
    await sdk.resolve({ agent: "coder", sessionID })
    const duration1 = performance.now() - start1

    // Second request (cache hit)
    const start2 = performance.now()
    await sdk.resolve({ agent: "coder", sessionID })
    const duration2 = performance.now() - start2

    if (duration2 < duration1) {
      console.log(`${PASS} Cache working (${duration1.toFixed(2)}ms → ${duration2.toFixed(2)}ms)`)
      passed++
    } else {
      console.log(`${WARN} Cache not improving performance`)
      warnings++
    }

    // Wait for TTL expiration
    await new Promise(resolve => setTimeout(resolve, 1100))

    // Third request (should be cache miss due to TTL)
    const start3 = performance.now()
    await sdk.resolve({ agent: "coder", sessionID })
    const duration3 = performance.now() - start3

    if (duration3 > duration2 * 2) {
      console.log(`${PASS} TTL expiration working (${duration3.toFixed(2)}ms after expiry)`)
      passed++
    } else {
      console.log(`${WARN} TTL expiration may not be working correctly`)
      warnings++
    }

    await sdk.dispose()
  } catch (error) {
    console.log(`${FAIL} Session state test failed:`, error)
    failed++
  }

  // Test 3: Parameter Precedence
  console.log("\n3️⃣  Testing Parameter Precedence...")
  try {
    const sdk = new TmuxCoderPrompts({
      mode: "local",
      local: {
        templatesDir: join(__dirname, "../fixtures/templates"),
        parametersPath: join(__dirname, "../fixtures/parameters.json"),
      },
    })
    await sdk.initialize()

    // Test reviewer agent (has specific temperature)
    const result = await sdk.resolve({
      agent: "reviewer",
      sessionID: "param-test",
    })

    if (result.parameters.temperature === 0.3) {
      console.log(`${PASS} Agent-specific parameters applied correctly (temp: 0.3)`)
      passed++
    } else {
      console.log(`${FAIL} Expected temperature 0.3, got ${result.parameters.temperature}`)
      failed++
    }

    await sdk.dispose()
  } catch (error) {
    console.log(`${FAIL} Parameter test failed:`, error)
    failed++
  }

  // Test 4: Performance (Latency)
  console.log("\n4️⃣  Testing Performance...")
  try {
    const sdk = new TmuxCoderPrompts({
      mode: "local",
      local: {
        templatesDir: join(__dirname, "../fixtures/templates"),
        parametersPath: join(__dirname, "../fixtures/parameters.json"),
      },
      cache: { enabled: true },
      debug: false,
    })
    await sdk.initialize()

    // Uncached request
    const start1 = performance.now()
    await sdk.resolve({ agent: "coder", sessionID: "perf-test-1" })
    const uncachedLatency = performance.now() - start1

    // Cached request
    const start2 = performance.now()
    await sdk.resolve({ agent: "coder", sessionID: "perf-test-1" })
    const cachedLatency = performance.now() - start2

    console.log(`   Uncached: ${uncachedLatency.toFixed(2)}ms`)
    console.log(`   Cached:   ${cachedLatency.toFixed(2)}ms`)

    if (uncachedLatency < 50) {
      console.log(`${PASS} Uncached latency meets target (< 50ms)`)
      passed++
    } else if (uncachedLatency < 100) {
      console.log(`${WARN} Uncached latency acceptable (< 100ms)`)
      warnings++
    } else {
      console.log(`${FAIL} Uncached latency too high (>= 100ms)`)
      failed++
    }

    if (cachedLatency < 5) {
      console.log(`${PASS} Cached latency excellent (< 5ms)`)
      passed++
    } else if (cachedLatency < 10) {
      console.log(`${WARN} Cached latency acceptable (< 10ms)`)
      warnings++
    } else {
      console.log(`${FAIL} Cached latency too high (>= 10ms)`)
      failed++
    }

    await sdk.dispose()
  } catch (error) {
    console.log(`${FAIL} Performance test failed:`, error)
    failed++
  }

  // Test 5: Error Handling
  console.log("\n5️⃣  Testing Error Handling...")
  try {
    const sdk = new TmuxCoderPrompts({
      mode: "local",
      local: {
        templatesDir: join(__dirname, "../fixtures/templates"),
      },
    })
    await sdk.initialize()

    // Request nonexistent agent (should fallback)
    const result = await sdk.resolve({
      agent: "nonexistent-agent",
      sessionID: "error-test",
    })

    if (result.system && result.system.length > 0) {
      console.log(`${PASS} Graceful fallback for missing template`)
      passed++
    } else {
      console.log(`${FAIL} No fallback for missing template`)
      failed++
    }

    await sdk.dispose()
  } catch (error) {
    console.log(`${FAIL} Error handling test failed:`, error)
    failed++
  }

  // Summary
  console.log("\n" + "=".repeat(60))
  console.log("📊 Summary:")
  console.log(`   ${PASS} Passed:   ${passed}`)
  console.log(`   ${WARN} Warnings: ${warnings}`)
  console.log(`   ${FAIL} Failed:   ${failed}`)

  const total = passed + warnings + failed
  const score = ((passed + warnings * 0.5) / total) * 100

  console.log(`\n   Overall Score: ${score.toFixed(0)}%`)

  if (score >= 90) {
    console.log(`\n${PASS} Excellent! Implementation meets architecture requirements.`)
  } else if (score >= 70) {
    console.log(`\n${WARN} Good, but some improvements needed.`)
  } else {
    console.log(`\n${FAIL} Significant issues detected. Review implementation.`)
  }

  console.log("\n" + "=".repeat(60))
  console.log("\nFor detailed testing, see: docs/TESTING_GUIDE.md")
  console.log("For architecture analysis, see: docs/IMPLEMENTATION_ANALYSIS.md")

  process.exit(failed > 0 ? 1 : 0)
}

main().catch((error) => {
  console.error("💥 Validation script crashed:", error)
  process.exit(1)
})
