import { ConsolaOptions, ConsolaInstance } from './core.js';
export { Consola, ConsolaReporter, FormatOptions, InputLogObject, LogLevel, LogLevels, LogObject, LogType, LogTypes } from './core.js';

declare function createConsola(options?: Partial<ConsolaOptions & {
    fancy: boolean;
}>): ConsolaInstance;
declare const consola: ConsolaInstance;

export { ConsolaInstance, ConsolaOptions, consola, createConsola, consola as default };
