/**
 * @fookie/types — Shared type definitions for Fookie client and worker packages
 */
// ──────────────────────────────────────────────────────────────────────────
// Error Types
// ──────────────────────────────────────────────────────────────────────────
export class FookieError extends Error {
    code;
    statusCode;
    constructor(message, code, statusCode = 500) {
        super(message);
        this.code = code;
        this.statusCode = statusCode;
        this.name = 'FookieError';
    }
}
//# sourceMappingURL=index.js.map