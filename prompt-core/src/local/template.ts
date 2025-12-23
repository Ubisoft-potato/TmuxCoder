import { readFile } from "fs/promises"
import { join } from "path"
import Handlebars from "handlebars"
import { promptLogger } from "../logger"

export interface TemplateEngineConfig {
  templatesDir: string
  debug?: boolean
}

export class TemplateEngine {
  private templates = new Map<string, HandlebarsTemplateDelegate>()
  private defaultTemplate?: HandlebarsTemplateDelegate

  constructor(private config: TemplateEngineConfig) {}

  async initialize(): Promise<void> {
    // Preload default template
    try {
      const defaultPath = join(this.config.templatesDir, "default.txt")
      const defaultContent = await readFile(defaultPath, "utf-8")
      this.defaultTemplate = Handlebars.compile(defaultContent, {
        noEscape: false,
        strict: true,
      })

      if (this.config.debug) {
        promptLogger.debug("[TemplateEngine] Loaded default template")
      }
    } catch (error) {
      promptLogger.warn("[TemplateEngine] No default template found, using hardcoded fallback", error)
      this.defaultTemplate = Handlebars.compile("You are an AI assistant.")
    }

    // Register custom helpers
    this.registerHelpers()
  }

  async render(templateID: string, context: Record<string, any>): Promise<string> {
    const template = await this.loadTemplate(templateID)

    try {
      return template(context)
    } catch (error) {
      promptLogger.error(`[TemplateEngine] Failed to render template '${templateID}'`, error)
      throw error
    }
  }

  private async loadTemplate(templateID: string): Promise<HandlebarsTemplateDelegate> {
    // Check cache
    if (this.templates.has(templateID)) {
      return this.templates.get(templateID)!
    }

    // Try to load template file
    const templatePath = join(this.config.templatesDir, `${templateID}.txt`)

    try {
      const content = await readFile(templatePath, "utf-8")
      const compiled = Handlebars.compile(content, {
        noEscape: false,
        strict: true,
      })

      // Cache compiled result
      this.templates.set(templateID, compiled)

      if (this.config.debug) {
        promptLogger.debug("[TemplateEngine] Loaded template", { templateID })
      }

      return compiled

    } catch (error) {
      promptLogger.warn(`[TemplateEngine] Template '${templateID}' not found, using default`, error)
      return this.defaultTemplate!
    }
  }

  /**
   * Register custom Handlebars helpers
   */
  private registerHelpers(): void {
    // Helper: Format date
    Handlebars.registerHelper("formatDate", (date: string, format: string) => {
      const d = new Date(date)
      if (format === "short") {
        return d.toLocaleDateString()
      }
      return d.toISOString()
    })

    // Helper: Conditional checks
    Handlebars.registerHelper("eq", (a: any, b: any) => a === b)
    Handlebars.registerHelper("ne", (a: any, b: any) => a !== b)
    Handlebars.registerHelper("gt", (a: number, b: number) => a > b)
    Handlebars.registerHelper("lt", (a: number, b: number) => a < b)

    // Helper: String operations
    Handlebars.registerHelper("uppercase", (str: string) => str?.toUpperCase() || "")
    Handlebars.registerHelper("lowercase", (str: string) => str?.toLowerCase() || "")

    if (this.config.debug) {
      promptLogger.debug("[TemplateEngine] Registered custom helpers")
    }
  }

  /**
   * Clear cache (for hot reload)
   */
  clearCache(): void {
    this.templates.clear()
    if (this.config.debug) {
      promptLogger.debug("[TemplateEngine] Cache cleared")
    }
  }
}
