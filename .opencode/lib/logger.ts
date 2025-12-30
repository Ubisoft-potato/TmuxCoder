/**
 * Unified Logger for Prompt Proxy
 * Supports structured logging, log levels, and performance metrics
 */

export enum LogLevel {
  DEBUG = 0,
  INFO = 1,
  WARN = 2,
  ERROR = 3,
}

interface LogContext {
  component?: string
  sessionID?: string
  action?: string
  [key: string]: any
}

class Logger {
  private level: LogLevel
  private component: string

  constructor(component: string, level: LogLevel = LogLevel.INFO) {
    this.component = component
    this.level = level
  }

  setLevel(level: LogLevel) {
    this.level = level
  }

  private shouldLog(level: LogLevel): boolean {
    return level >= this.level
  }

  private formatMessage(level: string, message: string, context?: LogContext): string {
    const timestamp = new Date().toISOString()
    const ctx = context ? ` ${JSON.stringify(context)}` : ''
    return `[${timestamp}] [${level}] [${this.component}] ${message}${ctx}`
  }

  debug(message: string, context?: LogContext) {
    if (this.shouldLog(LogLevel.DEBUG)) {
      console.debug(this.formatMessage('DEBUG', message, context))
    }
  }

  info(message: string, context?: LogContext) {
    if (this.shouldLog(LogLevel.INFO)) {
      console.log(this.formatMessage('INFO', message, context))
    }
  }

  warn(message: string, context?: LogContext) {
    if (this.shouldLog(LogLevel.WARN)) {
      console.warn(this.formatMessage('WARN', message, context))
    }
  }

  error(message: string, error?: Error, context?: LogContext) {
    if (this.shouldLog(LogLevel.ERROR)) {
      const errorContext = error ? { ...context, error: error.message, stack: error.stack } : context
      console.error(this.formatMessage('ERROR', message, errorContext))
    }
  }

  // Metric logging for performance monitoring
  metric(metricName: string, value: number, unit?: string, context?: LogContext) {
    if (this.shouldLog(LogLevel.INFO)) {
      const metricContext = { ...context, metric: metricName, value, unit: unit || 'count' }
      this.info(`[METRIC] ${metricName}=${value}${unit ? unit : ''}`, metricContext)
    }
  }

  // Trace logging for detailed debugging
  trace(action: string, data?: any) {
    this.debug(`[TRACE] ${action}`, data)
  }
}

// Singleton instances for different components
export const promptProxyLogger = new Logger('PromptProxy')
export const cacheLogger = new Logger('PromptCache')
export const resolverLogger = new Logger('PromptResolver')

// Export Logger class for custom instances
export { Logger }

// Helper to parse log level from string
export function parseLogLevel(level: string): LogLevel {
  const upperLevel = level.toUpperCase()
  switch (upperLevel) {
    case 'DEBUG':
      return LogLevel.DEBUG
    case 'INFO':
      return LogLevel.INFO
    case 'WARN':
      return LogLevel.WARN
    case 'ERROR':
      return LogLevel.ERROR
    default:
      return LogLevel.INFO
  }
}
