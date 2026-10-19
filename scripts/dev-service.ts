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

// Cargar variables propias del servicio desde <servicio>/configs/.env.local.
// Ganan sobre las centralizadas: el nombre de la tabla que usa api-catalog no
// tiene por qué existir en la raíz, donde solo viven las variables comunes a
// todos los servicios (perfil y región de AWS).
const serviceEnvPath = join(servicePath, 'configs', '.env.local');
const serviceEnv = parseDotenv(serviceEnvPath);

if (!existsSync(serviceEnvPath)) {
  console.warn(`⚠️  No se encontró ${serviceEnvPath}. Copia ${serviceConfig.name}/configs/.env.example como ${serviceConfig.name}/configs/.env.local`);
}

// En stage=local se omite `serverless print` para no depender de credenciales AWS.
let providerEnvironment: Record<string, string> = {};

if (stage === 'local') {
  console.log(`🏠 Stage local: usando variables de configs/.env.local (raíz) y ${serviceConfig.name}/configs/.env.local (sin resolver SSM)`);
} else {
  // Serverless Framework v4 emite la configuracion por stderr, no por stdout,
  // por lo que se redirige stderr a stdout para capturarla completa. Sin esto el
  // parseo recibe una salida vacia y el servicio arranca sin las variables de
  // provider.environment, fallando mas tarde y por una causa poco evidente.
  const rawOutput = execSync(`npx serverless print --stage ${stage} --region ${region} 2>&1`, {
    cwd: servicePath,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  const yamlStartIndex = rawOutput.search(/^service:/m);

  if (yamlStartIndex === -1) {
    console.error(`Error: la salida de serverless print no contiene la clave 'service:'`);
    console.error(`Salida recibida:\n${rawOutput}`);
    process.exit(1);
  }

  const output = rawOutput.substring(yamlStartIndex);

  const YAML = await import('yaml');
  const serverlessConfig = YAML.parse(output);

  // YAML.parse devuelve un string cuando la entrada no es un mapa. Sin esta
  // comprobacion providerEnvironment queda vacio y el servidor local arranca
  // mal configurado en silencio.
  if (typeof serverlessConfig !== 'object' || serverlessConfig === null) {
    console.error(`Error: serverless print no devolvió una configuración válida`);
    console.error(`Salida recibida:\n${output}`);
    process.exit(1);
  }

  providerEnvironment = serverlessConfig.provider?.environment ?? {};

  if (Object.keys(providerEnvironment).length === 0) {
    console.error(
      `Error: provider.environment llegó vacío para ${serviceConfig.name}. El servicio arrancaría sin sus variables de entorno.`,
    );
    process.exit(1);
  }
}

const child = spawn('go', ['run', serviceConfig.localEntrypoint], {
  cwd: servicePath,
  stdio: 'inherit',
  env: {
    ...process.env,
    ...centralEnv,
    ...serviceEnv,
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
