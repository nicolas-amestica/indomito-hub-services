import { type Environment } from './environment';

/**
 * Configuración de rate limiting por ambiente para HTTP API Gateway v2.
 * - maxRequestsPerSecond: máximo de requests por segundo (throttling).
 * - burstLimit: ráfaga máxima permitida antes de aplicar throttling.
 */
export type RateLimitConfig = {
  maxRequestsPerSecond: number;
  burstLimit: number;
};

export const RATE_LIMITS: Record<Environment, RateLimitConfig> = {
  dev: {
    maxRequestsPerSecond: 10,
    burstLimit: 20,
  },
  qa: {
    maxRequestsPerSecond: 10,
    burstLimit: 20,
  },
  prd: {
    maxRequestsPerSecond: 100,
    burstLimit: 400,
  },
};
