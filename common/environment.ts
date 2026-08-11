export type Environment = 'dev' | 'prd';

export const ENVIRONMENTS = {
  prd: 'prd',
  dev: 'dev',
};

function getEnvironmentFromArgv(): string {
  if (process.env.STAGE) return process.env.STAGE;

  const args = process.argv;
  const stageIndex = args.findIndex((arg) => arg === '--stage' || arg === '-s');

  if (stageIndex !== -1 && args[stageIndex + 1]) {
    return args[stageIndex + 1];
  }

  return process.env.STAGE || 'dev';
}

export const SELECTED_ENVIRONMENT = getEnvironmentFromArgv();
