// @bun
var __defProp = Object.defineProperty;
var __export = (target, all) => {
  for (var name in all)
    __defProp(target, name, {
      get: all[name],
      enumerable: true,
      configurable: true,
      set: (newValue) => all[name] = () => newValue
    });
};
var __esm = (fn, res) => () => (fn && (res = fn(fn = 0)), res);

// src/logger.ts
import { appendFileSync, mkdirSync } from "fs";
import { dirname } from "path";

class PromptLogger {
  filePath;
  debugEnabled = false;
  initialized = false;
  configure(options) {
    this.filePath = options?.filePath;
    this.debugEnabled = Boolean(options?.debug);
    if (this.filePath) {
      try {
        mkdirSync(dirname(this.filePath), { recursive: true });
        appendFileSync(this.filePath, "", { encoding: "utf-8", flag: "a" });
        this.initialized = true;
      } catch (error) {
        this.initialized = false;
        this.consoleFallback("ERROR", `[PromptLogger] Failed to prepare log file '${this.filePath}': ${String(error)}`);
      }
    }
  }
  debug(message, meta) {
    if (!this.debugEnabled)
      return;
    this.write("DEBUG", message, meta);
  }
  info(message, meta) {
    this.write("INFO", message, meta);
  }
  warn(message, meta) {
    this.write("WARN", message, meta);
  }
  error(message, meta) {
    this.write("ERROR", message, meta);
  }
  write(level, message, meta) {
    const serializedMeta = this.formatMeta(meta);
    const line = `[${new Date().toISOString()}] [${level}] ${message}${serializedMeta}
`;
    if (this.filePath && this.initialized) {
      try {
        appendFileSync(this.filePath, line, { encoding: "utf-8" });
        return;
      } catch (error) {
        this.consoleFallback("ERROR", `[PromptLogger] Failed to write to '${this.filePath}': ${String(error)}`);
      }
    }
    this.consoleFallback(level, line);
  }
  formatMeta(meta) {
    if (meta === undefined) {
      return "";
    }
    if (typeof meta === "string") {
      return ` ${meta}`;
    }
    if (meta instanceof Error) {
      return ` ${JSON.stringify({
        name: meta.name,
        message: meta.message,
        stack: meta.stack
      }, null, 2)}`;
    }
    return ` ${JSON.stringify(meta, null, 2)}`;
  }
  consoleFallback(level, message) {
    switch (level) {
      case "WARN":
        console.warn(message);
        break;
      case "ERROR":
        console.error(message);
        break;
      default:
        console.log(message);
    }
  }
}
function configurePromptLogger(options) {
  promptLogger.configure(options);
}
var promptLogger;
var init_logger = __esm(() => {
  promptLogger = new PromptLogger;
});

// src/local/template.ts
import { readFile } from "fs/promises";
import { join } from "path";
import Handlebars from "handlebars";

class TemplateEngine {
  config;
  templates = new Map;
  defaultTemplate;
  constructor(config) {
    this.config = config;
  }
  async initialize() {
    try {
      const defaultPath = join(this.config.templatesDir, "default.txt");
      const defaultContent = await readFile(defaultPath, "utf-8");
      this.defaultTemplate = Handlebars.compile(defaultContent, {
        noEscape: false,
        strict: true
      });
      if (this.config.debug) {
        promptLogger.debug("[TemplateEngine] Loaded default template");
      }
    } catch (error) {
      promptLogger.warn("[TemplateEngine] No default template found, using hardcoded fallback", error);
      this.defaultTemplate = Handlebars.compile("You are an AI assistant.");
    }
    this.registerHelpers();
  }
  async render(templateID, context) {
    const template = await this.loadTemplate(templateID);
    try {
      return template(context);
    } catch (error) {
      promptLogger.error(`[TemplateEngine] Failed to render template '${templateID}'`, error);
      throw error;
    }
  }
  async loadTemplate(templateID) {
    if (this.templates.has(templateID)) {
      return this.templates.get(templateID);
    }
    const templatePath = join(this.config.templatesDir, `${templateID}.txt`);
    try {
      const content = await readFile(templatePath, "utf-8");
      const compiled = Handlebars.compile(content, {
        noEscape: false,
        strict: true
      });
      this.templates.set(templateID, compiled);
      if (this.config.debug) {
        promptLogger.debug("[TemplateEngine] Loaded template", { templateID });
      }
      return compiled;
    } catch (error) {
      promptLogger.warn(`[TemplateEngine] Template '${templateID}' not found, using default`, error);
      return this.defaultTemplate;
    }
  }
  registerHelpers() {
    Handlebars.registerHelper("formatDate", (date, format) => {
      const d = new Date(date);
      if (format === "short") {
        return d.toLocaleDateString();
      }
      return d.toISOString();
    });
    Handlebars.registerHelper("eq", (a, b) => a === b);
    Handlebars.registerHelper("ne", (a, b) => a !== b);
    Handlebars.registerHelper("gt", (a, b) => a > b);
    Handlebars.registerHelper("lt", (a, b) => a < b);
    Handlebars.registerHelper("uppercase", (str) => str?.toUpperCase() || "");
    Handlebars.registerHelper("lowercase", (str) => str?.toLowerCase() || "");
    if (this.config.debug) {
      promptLogger.debug("[TemplateEngine] Registered custom helpers");
    }
  }
  clearCache() {
    this.templates.clear();
    if (this.config.debug) {
      promptLogger.debug("[TemplateEngine] Cache cleared");
    }
  }
}
var init_template = __esm(() => {
  init_logger();
});

// src/local/experiments.ts
import { readFile as readFile2 } from "fs/promises";
import { existsSync } from "fs";

class ExperimentManager {
  opts;
  config;
  constructor(opts) {
    this.opts = opts;
  }
  async initialize() {
    if (!this.opts.configPath || !existsSync(this.opts.configPath)) {
      this.config = { experiments: [] };
      return;
    }
    try {
      const content = await readFile2(this.opts.configPath, "utf-8");
      this.config = JSON.parse(content);
      if (this.opts.debug) {
        promptLogger.debug("[ExperimentManager] Loaded experiments", {
          count: this.config.experiments.length
        });
      }
    } catch (error) {
      promptLogger.error("[ExperimentManager] Failed to load experiments", error);
      this.config = { experiments: [] };
    }
  }
  findActiveExperiment(agent, sessionID) {
    if (!this.config || this.config.experiments.length === 0) {
      return null;
    }
    return this.config.experiments.find((exp) => {
      if (!exp.enabled)
        return false;
      if (exp.targeting?.agents && !exp.targeting.agents.includes(agent)) {
        return false;
      }
      return true;
    }) || null;
  }
  allocateVariant(experiment, sessionID) {
    const hash = this.hashString(sessionID + experiment.id);
    const allocation = experiment.allocation;
    let cumulative = 0;
    for (const [variant, probability] of Object.entries(allocation)) {
      cumulative += probability;
      if (hash < cumulative) {
        return variant;
      }
    }
    return "control";
  }
  hashString(str) {
    let hash = 0;
    for (let i = 0;i < str.length; i++) {
      const char = str.charCodeAt(i);
      hash = (hash << 5) - hash + char;
      hash = hash & hash;
    }
    return Math.abs(hash) / 2147483647;
  }
}
var init_experiments = __esm(() => {
  init_logger();
});

// src/local/parameters.ts
import { readFile as readFile3 } from "fs/promises";
import { existsSync as existsSync2 } from "fs";

class ParameterManager {
  opts;
  config;
  constructor(opts) {
    this.opts = opts;
  }
  async initialize() {
    if (!this.opts.configPath || !existsSync2(this.opts.configPath)) {
      this.config = {
        defaults: {
          temperature: 0.7,
          topP: 0.9
        }
      };
      return;
    }
    try {
      const content = await readFile3(this.opts.configPath, "utf-8");
      this.config = JSON.parse(content);
      if (this.opts.debug) {
        promptLogger.debug("[ParameterManager] Loaded parameters config");
      }
    } catch (error) {
      promptLogger.error("[ParameterManager] Failed to load parameters", error);
      this.config = { defaults: { temperature: 0.7, topP: 0.9 } };
    }
  }
  resolve(opts) {
    const { agent, model, experiment, variantID } = opts;
    let params = { ...this.config?.defaults };
    if (model && this.config?.models?.[model]) {
      params = { ...params, ...this.config.models[model] };
    }
    if (this.config?.agents?.[agent]) {
      params = { ...params, ...this.config.agents[agent] };
    }
    if (experiment && variantID && experiment.variants[variantID]) {
      params = { ...params, ...experiment.variants[variantID] };
    }
    return params;
  }
}
var init_parameters = __esm(() => {
  init_logger();
});

// src/local/manager.ts
var exports_manager = {};
__export(exports_manager, {
  LocalResolver: () => LocalResolver
});
var LocalResolver;
var init_manager = __esm(() => {
  init_template();
  init_experiments();
  init_parameters();
  init_logger();
  LocalResolver = class LocalResolver extends PromptResolver {
    templateEngine;
    experimentManager;
    parameterManager;
    initialized = false;
    constructor(config) {
      super(config);
      if (!config.local) {
        throw new Error("Local config is required for LocalResolver");
      }
    }
    async initialize() {
      if (this.initialized)
        return;
      const { local } = this.config;
      this.templateEngine = new TemplateEngine({
        templatesDir: local.templatesDir,
        debug: this.config.debug
      });
      await this.templateEngine.initialize();
      if (local.experimentsPath) {
        this.experimentManager = new ExperimentManager({
          configPath: local.experimentsPath,
          debug: this.config.debug
        });
        await this.experimentManager.initialize();
      } else {
        this.experimentManager = new ExperimentManager({ configPath: "" });
        await this.experimentManager.initialize();
      }
      if (local.parametersPath) {
        this.parameterManager = new ParameterManager({
          configPath: local.parametersPath,
          debug: this.config.debug
        });
        await this.parameterManager.initialize();
      } else {
        this.parameterManager = new ParameterManager({ configPath: "" });
        await this.parameterManager.initialize();
      }
      this.initialized = true;
      if (this.config.debug) {
        promptLogger.debug("[LocalResolver] Initialized successfully");
      }
    }
    async resolve(context) {
      if (!this.initialized) {
        throw new Error("LocalResolver not initialized. Call initialize() first.");
      }
      const startTime = Date.now();
      try {
        const enrichedContext = await this.enrichContext(context);
        const experiment = this.experimentManager.findActiveExperiment(context.agent, context.sessionID);
        const variantID = experiment ? this.experimentManager.allocateVariant(experiment, context.sessionID) : "default";
        const systemPrompt = await this.templateEngine.render(context.agent, enrichedContext);
        const parameters = this.parameterManager.resolve({
          agent: context.agent,
          model: context.model?.modelID,
          experiment,
          variantID
        });
        const resolved = {
          system: systemPrompt,
          parameters,
          metadata: {
            templateID: context.agent,
            variantID,
            experimentID: experiment?.id,
            resolverType: "local",
            resolvedAt: new Date().toISOString()
          }
        };
        if (this.config.debug) {
          const elapsed = Date.now() - startTime;
          promptLogger.debug("[LocalResolver] Resolved prompt", {
            agent: context.agent,
            variantID,
            temperature: parameters.temperature,
            durationMs: elapsed
          });
        }
        return resolved;
      } catch (error) {
        promptLogger.error("[LocalResolver] Failed to resolve prompt", error);
        return this.getFallbackPrompt(context);
      }
    }
    async enrichContext(context) {
      return {
        agent: context.agent,
        sessionID: context.sessionID,
        project_name: context.project?.name || "unknown",
        project_path: context.project?.path || "",
        git_branch: context.git?.branch || "unknown",
        git_dirty: context.git?.isDirty || false,
        git_commit: context.git?.commitHash?.substring(0, 7) || "",
        model_provider: context.model?.providerID || "unknown",
        model_id: context.model?.modelID || "unknown",
        timestamp: new Date().toISOString(),
        date: new Date().toLocaleDateString(),
        time: new Date().toLocaleTimeString(),
        user_id: context.user?.id,
        user_email: context.user?.email,
        ...context.environment
      };
    }
    getFallbackPrompt(context) {
      return {
        system: `You are an AI assistant for the ${context.project?.name || "project"}.`,
        parameters: {
          temperature: 0.7,
          topP: 0.9
        },
        metadata: {
          templateID: "fallback",
          resolverType: "local",
          resolvedAt: new Date().toISOString()
        }
      };
    }
    async healthCheck() {
      return this.initialized;
    }
  };
});

// src/resolver.ts
class PromptResolver {
  config;
  constructor(config) {
    this.config = config;
  }
  async dispose() {}
  async healthCheck() {
    return true;
  }
}

class ResolverFactory {
  static async create(config) {
    switch (config.mode) {
      case "local": {
        const { LocalResolver: LocalResolver2 } = await Promise.resolve().then(() => (init_manager(), exports_manager));
        return new LocalResolver2(config);
      }
      case "remote":
        throw new Error("Remote mode not implemented yet");
      case "hybrid":
        throw new Error("Hybrid mode not implemented yet");
      default:
        throw new Error(`Unknown mode: ${config.mode}`);
    }
  }
}
// src/cache.ts
init_logger();

class PromptCache {
  config;
  cache = new Map;
  cleanupInterval;
  constructor(config) {
    this.config = config;
    if (config.enabled) {
      this.cleanupInterval = setInterval(() => this.cleanup(), config.cleanupIntervalMs || 60000);
    }
  }
  static generateKey(agent, sessionID, modelID) {
    return `${agent}:${sessionID}:${modelID || "default"}`;
  }
  get(key) {
    if (!this.config.enabled)
      return null;
    const entry = this.cache.get(key);
    if (!entry)
      return null;
    const now = Date.now();
    if (now - entry.timestamp > entry.ttl * 1000) {
      this.cache.delete(key);
      return null;
    }
    return entry.value;
  }
  set(key, value, ttl) {
    if (!this.config.enabled)
      return;
    if (this.config.maxSize && this.cache.size >= this.config.maxSize) {
      const oldestKey = this.findOldestKey();
      if (oldestKey) {
        this.cache.delete(oldestKey);
      }
    }
    this.cache.set(key, {
      value,
      timestamp: Date.now(),
      ttl: ttl || this.config.ttl || 300
    });
  }
  clearSession(sessionID) {
    for (const key of this.cache.keys()) {
      if (key.includes(sessionID)) {
        this.cache.delete(key);
      }
    }
  }
  clear() {
    this.cache.clear();
  }
  cleanup() {
    const now = Date.now();
    let deletedCount = 0;
    for (const [key, entry] of this.cache.entries()) {
      if (now - entry.timestamp > entry.ttl * 1000) {
        this.cache.delete(key);
        deletedCount++;
      }
    }
    if (deletedCount > 0) {
      promptLogger.info("[PromptCache] Cleaned up expired entries", { deletedCount });
    }
  }
  findOldestKey() {
    let oldestKey = null;
    let oldestTime = Date.now();
    for (const [key, entry] of this.cache.entries()) {
      if (entry.timestamp < oldestTime) {
        oldestTime = entry.timestamp;
        oldestKey = key;
      }
    }
    return oldestKey;
  }
  dispose() {
    if (this.cleanupInterval) {
      clearInterval(this.cleanupInterval);
    }
    this.cache.clear();
  }
}

// src/index.ts
init_manager();
init_logger();

class TmuxCoderPrompts {
  config;
  resolver;
  cache;
  initialized = false;
  constructor(config) {
    this.config = config;
    this.cache = new PromptCache(config.cache || { enabled: false });
    configurePromptLogger({
      filePath: config.logging?.filePath,
      debug: config.debug
    });
  }
  async initialize() {
    if (this.initialized)
      return;
    this.resolver = await ResolverFactory.create(this.config);
    await this.resolver.initialize();
    this.initialized = true;
    promptLogger.debug("[TmuxCoderPrompts] SDK initialized", {
      mode: this.config.mode,
      cacheEnabled: this.config.cache?.enabled
    });
  }
  async resolve(context) {
    if (!this.initialized || !this.resolver) {
      throw new Error("SDK not initialized. Call initialize() first.");
    }
    const cacheKey = PromptCache.generateKey(context.agent, context.sessionID, context.model?.modelID);
    const cached = this.cache.get(cacheKey);
    if (cached) {
      promptLogger.debug("[TmuxCoderPrompts] Cache hit", { cacheKey });
      return cached;
    }
    const resolved = await this.resolver.resolve(context);
    this.cache.set(cacheKey, resolved);
    return resolved;
  }
  clearSessionCache(sessionID) {
    this.cache.clearSession(sessionID);
  }
  async healthCheck() {
    if (!this.resolver)
      return false;
    return this.resolver.healthCheck();
  }
  async dispose() {
    if (this.resolver) {
      await this.resolver.dispose();
    }
    this.cache.dispose();
    this.initialized = false;
  }
}
export {
  TmuxCoderPrompts,
  ResolverFactory,
  PromptResolver,
  PromptCache,
  LocalResolver
};
