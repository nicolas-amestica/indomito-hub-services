type AwsRegionCode = 'use1';
export type AwsRegion = 'us-east-1';
type AwsRegionName = 'EEUU_NORTHERN_VIRGINIA';

export const REGION_CODES: Record<AwsRegion, AwsRegionCode> = {
  'us-east-1': 'use1',
};

export const REGIONS_ALLOWED: Record<AwsRegionName, AwsRegion> = {
  EEUU_NORTHERN_VIRGINIA: 'us-east-1',
};

const GET_REGION_FROM_ARGV = (): AwsRegion => {
  if (process.env.REGION && REGION_CODES[process.env.REGION as AwsRegion]) {
    return process.env.REGION as AwsRegion;
  }

  const args = process.argv;
  const regionIndex = args.findIndex((arg) => arg === '--region' || arg === '-r');

  if (regionIndex !== -1 && args[regionIndex + 1] && REGION_CODES[args[regionIndex + 1] as AwsRegion]) {
    return args[regionIndex + 1] as AwsRegion;
  }

  return REGIONS_ALLOWED.EEUU_NORTHERN_VIRGINIA;
};

export const SELECTED_REGION = GET_REGION_FROM_ARGV();
