import { appendFileSync, mkdirSync } from "fs"
import { dirname } from "path"

export type PromptLoggerOptions = {
  filePath?: string
  debug?: boolean
}

type LogLevel = "DEBUG" | "INFO" | "WARN" | "ERROR"

class PromptLogger {
  private filePath?: string
  private debugEnabled = false
  private initialized = false

  configure(options?: PromptLoggerOptions): void {
    this.filePath = options?.filePath
    this.debugEnabled = Boolean(options?.debug)

    if (this.filePath) {
      try {
        mkdirSync(dirname(this.filePath), { recursive: true })
        appendFileSync(this.filePath, "", { encoding: "utf-8", flag: "a" })
        this.initialized = true
      } catch (error) {
        this.initialized = false
        this.consoleFallback(
          "ERROR",
          `[PromptLogger] Failed to prepare log file '${this.filePath}': ${String(error)}`
        )
      }
    }
  }

  debug(message: string, meta?: unknown): void {
    if (!this.debugEnabled) return
    this.write("DEBUG", message, meta)
  }

  info(message: string, meta?: unknown): void {
    this.write("INFO", message, meta)
  }

  warn(message: string, meta?: unknown): void {
    this.write("WARN", message, meta)
  }

  error(message: string, meta?: unknown): void {
    this.write("ERROR", message, meta)
  }

  private write(level: LogLevel, message: string, meta?: unknown): void {
    const serializedMeta = this.formatMeta(meta)
    const line = `[${new Date().toISOString()}] [${level}] ${message}${serializedMeta}\n`

    if (this.filePath && this.initialized) {
      try {
        appendFileSync(this.filePath, line, { encoding: "utf-8" })
        return
      } catch (error) {
        this.consoleFallback(
          "ERROR",
          `[PromptLogger] Failed to write to '${this.filePath}': ${String(error)}`
        )
      }
    }

    this.consoleFallback(level, line)
  }

  private formatMeta(meta?: unknown): string {
    if (meta === undefined) {
      return ""
    }

    if (typeof meta === "string") {
      return ` ${meta}`
    }

    if (meta instanceof Error) {
      return ` ${JSON.stringify(
        {
          name: meta.name,
          message: meta.message,
          stack: meta.stack,
        },
        null,
        2
      )}`
    }

    return ` ${JSON.stringify(meta, null, 2)}`
  }

  private consoleFallback(level: LogLevel, message: string): void {
    switch (level) {
      case "WARN":
        console.warn(message)
        break
      case "ERROR":
        console.error(message)
        break
      default:
        console.log(message)
    }
  }
}

export const promptLogger = new PromptLogger()

export function configurePromptLogger(options?: PromptLoggerOptions): void {
  promptLogger.configure(options)
}
