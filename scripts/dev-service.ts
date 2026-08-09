import { execSync, spawn } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

type ServiceConfig = {
  name: string;
  localPort: number;
  localEntrypoint: string;
};

function getArg(name: string, fallback?: string): string {
  const index = process.argv.indexOf(`--${name}`);

  if (index === -1 || !process.argv[index + 1]) {
    if (fallback !== undefined) {
      return fallback;
    }

    throw new Error(`Missing required argument --${name}`);
  }

  return process.argv[index + 1];
}

/**
 * Parsea un archivo .env y retorna un objeto con las variables.
 */
function parseDotenv(filePath: string): Record<string, string> {
  if (!existsSync(filePath)) return {};

  const content = readFileSync(filePath, 'utf8');
  const env: Record<string, string> = {};

  for (const line of content.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;

    const eqIndex = trimmed.indexOf('=');
    if (eqIndex === -1) continue;

    const key = trimmed.slice(0, eqIndex).trim();
    let value = trimmed.slice(eqIndex + 1).trim();

    if ((value.startsWith('"') && value.endsWith('"')) ||
      (value.startsWith("'") && value.endsWith("'"))) {
      value = value.slice(1, -1);
    }

    env[key] = value;
  }

  return env;
}

const servicePath = resolve(getArg('service'));
const stage = getArg('stage', 'local');
const region = getArg('region', 'us-east-1');

const serviceConfig = JSON.parse(
  readFileSync(join(servicePath, 'service.config.json'), 'utf8'),
) as ServiceConfig;

// Cargar variables centralizadas desde configs/.env.local
const rootDir = resolve(import.meta.dirname, '..');
const centralEnvPath = join(rootDir, 'configs', '.env.local');
const centralEnv = parseDotenv(centralEnvPath);

if (!existsSync(centralEnvPath)) {
  console.warn(`⚠️  No se encontró ${centralEnvPath}. Copia configs/.env.example como configs/.env.local`);
}

// En stage=local se omite `serverless print` para no depender de credenciales AWS.
let providerEnvironment: Record<string, string> = {};

if (stage === 'local') {
  console.log('🏠 Stage local: usando variables de configs/.env.local (sin resolver SSM)');
} else {
  const rawOutput = execSync(`npx serverless print --stage ${stage} --region ${region}`, {
    cwd: servicePath,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'inherit'],
  });

  const lines = rawOutput.split('\n');
  const yamlStartIndex = lines.findIndex(line => /^\w+:/.test(line));
  const output = yamlStartIndex >= 0 ? lines.slice(yamlStartIndex).join('\n') : rawOutput;

  const YAML = await import('yaml');
  const serverlessConfig = YAML.parse(output);
  providerEnvironment = serverlessConfig.provider?.environment ?? {};
}

const child = spawn('go', ['run', serviceConfig.localEntrypoint], {
  cwd: servicePath,
  stdio: 'inherit',
  env: {
    ...process.env,
    ...centralEnv,
    ...providerEnvironment,
    APP_STAGE: stage,
    APP_REGION: region,
    APP_MODE: 'local',
    PORT: String(serviceConfig.localPort),
  },
});

child.on('exit', code => {
  process.exit(code ?? 0);
});
