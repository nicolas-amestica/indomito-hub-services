import { REGION_CODES, REGIONS_ALLOWED, SELECTED_REGION } from './aws-regions-allowed';
import { CORS_WHITELIST } from './cors-whitelist';
import { type Environment, SELECTED_ENVIRONMENT } from './environment';
import { RATE_LIMITS } from './rate-limit';

export const REGION = SELECTED_REGION;
export const STAGE = SELECTED_ENVIRONMENT as Environment;
export const REGION_CODE = REGION_CODES[REGION] || REGION_CODES[REGIONS_ALLOWED.EEUU_NORTHERN_VIRGINIA];
export const CORS_ORIGINS = CORS_WHITELIST[STAGE] || CORS_WHITELIST.dev;
export const RATE_LIMIT = RATE_LIMITS[STAGE] || RATE_LIMITS.dev;

/** Account IDs por ambiente */
const AWS_ACCOUNT_IDS: Record<Environment, string> = {
  dev: '382670112717',
  prd: '576394965493',
};

/** Sufijo corto del account ID (ultimos 6 digitos) para unicidad global en S3 */
const ACCOUNT_SUFFIX = AWS_ACCOUNT_IDS[STAGE].slice(-6);

/** Nombre del bucket de despliegues de Serverless Framework */
export const DEPLOYMENT_BUCKET = `ind-hub-${STAGE}-deploys-s3-pri-${REGION_CODE}-${ACCOUNT_SUFFIX}`;
