import { REGION_CODES, REGIONS_ALLOWED, SELECTED_REGION } from './aws-regions-allowed';
import { CORS_WHITELIST } from './cors-whitelist';
import { type Environment, SELECTED_ENVIRONMENT } from './environment';
import { RATE_LIMITS } from './rate-limit';

export const REGION = SELECTED_REGION;
export const STAGE = SELECTED_ENVIRONMENT as Environment;
export const REGION_CODE = REGION_CODES[REGION] || REGION_CODES[REGIONS_ALLOWED.EEUU_NORTHERN_VIRGINIA];
export const CORS_ORIGINS = CORS_WHITELIST[STAGE] || CORS_WHITELIST.dev;
export const RATE_LIMIT = RATE_LIMITS[STAGE] || RATE_LIMITS.dev;
