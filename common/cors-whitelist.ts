import { type Environment } from './environment';

/**
 * Orígenes permitidos por CORS, configurados por ambiente.
 * Solo estos dominios pueden realizar peticiones cross-origin a la API.
 */
export const CORS_WHITELIST: Record<Environment, string[]> = {
  dev: [
    'https://app.dev.indomitohub.cl',
    'https://api.dev.indomitohub.cl',
    'http://localhost:4200',
    'http://localhost:3000',
  ],
  qa: [
    'https://app.qa.indomitohub.cl',
    'https://api.qa.indomitohub.cl',
    'http://localhost:4200',
    'http://localhost:3000',
  ],
  prd: [
    'https://app.indomitohub.cl',
    'https://api.indomitohub.cl',
  ],
};
