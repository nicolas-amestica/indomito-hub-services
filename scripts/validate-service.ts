import { execFileSync, execSync } from 'node:child_process';
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';

type ServiceConfig = {
  defaultArtifactDir?: string;
  name: string;
  localPort?: number;
  localEntrypoint?: string;
  requiredEnvironment: string[];
  functions: Array<{
    name: string;
    entrypoint: string;
    artifact?: string;
  }>;
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

function assertZipContainsBootstrap(zipPath: string): boolean {
  try {
    const output = execFileSync('zipinfo', ['-1', zipPath], {
      encoding: 'utf8',
    });

    return output
      .split('\n')
      .map(line => line.trim())
      .includes('bootstrap');
  } catch {
    return false;
  }
}

function getCmdFnDirectories(servicePath: string): string[] {
  const cmdPath = join(servicePath, 'cmd');

  if (!existsSync(cmdPath)) {
    return [];
  }

  return readdirSync(cmdPath)
    .filter(entry => entry.startsWith('fn-'))
    .filter(entry => {
      try {
        return statSync(join(cmdPath, entry)).isDirectory();
      } catch {
        return false;
      }
    });
}

async function main() {

const servicePath = resolve(getArg('service'));
const stage = getArg('stage', 'dev');
const region = getArg('region', 'us-east-1');

const serviceConfig = JSON.parse(
  readFileSync(join(servicePath, 'service.config.json'), 'utf8'),
) as ServiceConfig;
const defaultArtifactDir = serviceConfig.defaultArtifactDir ?? '.serverless-artifacts';
const isApiService = serviceConfig.name.startsWith('api-');

const errors: string[] = [];

// --- Validation 1: serverless print succeeds ---
let serverlessConfig: any;

try {
  let output = execSync(`npx serverless print --stage ${stage} --region ${region}`, {
    cwd: servicePath,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'inherit'],
    env: { ...process.env, STAGE: stage, AWS_PROFILE: '', NODE_OPTIONS: '--disable-warning=DEP0169' },
  });

  const yamlStart = output.indexOf('service:');

  if (yamlStart > 0) {
    output = output.substring(yamlStart);
  }

  // Dynamic import para YAML (dependencia del package.json)
  const YAML = await import('yaml');
  serverlessConfig = YAML.parse(output);
} catch {
  console.error(`Error: serverless print falló para ${serviceConfig.name}`);
  process.exit(1);
}

const provider = serverlessConfig.provider ?? {};
const environment = provider.environment ?? {};
const slsFunctions: Record<string, any> = serverlessConfig.functions ?? {};

// --- Validation 2: provider.runtime is provided.al2023 ---
if (provider.runtime !== 'provided.al2023') {
  errors.push('[V2] provider.runtime debe ser provided.al2023');
}

// --- Validation 3: provider.architecture is arm64 ---
if (provider.architecture !== 'arm64') {
  errors.push('[V3] provider.architecture debe ser arm64');
}

// --- Validation 4: Every function has handler: 'bootstrap' ---
for (const [fnName, fnConfig] of Object.entries<any>(slsFunctions)) {
  if (fnConfig.handler !== 'bootstrap') {
    errors.push(`[V4] La función ${fnName} debe tener handler: 'bootstrap'`);
  }
}

// --- Validation 5: Every function has package.artifact ---
for (const [fnName, fnConfig] of Object.entries<any>(slsFunctions)) {
  if (!fnConfig.package?.artifact) {
    errors.push(`[V5] La función ${fnName} no tiene package.artifact`);
  }
}

// --- Validation 6: Every referenced artifact file exists ---
for (const [fnName, fnConfig] of Object.entries<any>(slsFunctions)) {
  const artifact = fnConfig.package?.artifact;

  if (artifact && !existsSync(join(servicePath, artifact))) {
    errors.push(`[V6] La función ${fnName} referencia artifact inexistente: ${artifact}`);
  }
}

// --- Validation 7: Every ZIP artifact contains bootstrap at root ---
for (const [fnName, fnConfig] of Object.entries<any>(slsFunctions)) {
  const artifact = fnConfig.package?.artifact;

  if (artifact) {
    const artifactPath = join(servicePath, artifact);

    if (existsSync(artifactPath) && !assertZipContainsBootstrap(artifactPath)) {
      errors.push(`[V7] El ZIP de ${fnName} no contiene bootstrap en la raíz`);
    }
  }
}

// --- Validation 8: Required env vars from service.config.json exist ---
for (const variable of serviceConfig.requiredEnvironment) {
  const value = environment[variable];

  if (value === undefined || value === null || value === '') {
    errors.push(`[V8] Falta variable requerida en provider.environment: ${variable}`);
  }
}

// --- Validation 9: Every functions[].entrypoint exists as a directory ---
for (const fn of serviceConfig.functions) {
  const entrypointPath = join(servicePath, fn.entrypoint);

  if (!existsSync(entrypointPath) || !statSync(entrypointPath).isDirectory()) {
    errors.push(`[V9] El entrypoint de ${fn.name} no existe como directorio: ${fn.entrypoint}`);
  }
}

// --- Validation 10: Every functions[].entrypoint/main.go exists ---
for (const fn of serviceConfig.functions) {
  const mainGoPath = join(servicePath, fn.entrypoint, 'main.go');

  if (!existsSync(mainGoPath)) {
    errors.push(`[V10] No existe main.go en el entrypoint de ${fn.name}: ${fn.entrypoint}/main.go`);
  }
}

// --- Validation 11: API services have cmd/local-api/main.go ---
if (isApiService) {
  const localApiMainGo = join(servicePath, 'cmd', 'local-api', 'main.go');

  if (!existsSync(localApiMainGo)) {
    errors.push('[V11] Servicio API debe tener cmd/local-api/main.go');
  }
}

// --- Validation 12: API services have localPort in service.config.json ---
if (isApiService) {
  if (serviceConfig.localPort === undefined || serviceConfig.localPort === null) {
    errors.push('[V12] Servicio API debe tener localPort en service.config.json');
  }
}

// --- Validation 13: API services have localEntrypoint in service.config.json ---
if (isApiService) {
  if (!serviceConfig.localEntrypoint) {
    errors.push('[V13] Servicio API debe tener localEntrypoint en service.config.json');
  }
}

// --- Validation 14: No cmd/fn-* directories without main.go ---
const cmdFnDirs = getCmdFnDirectories(servicePath);

for (const fnDir of cmdFnDirs) {
  const mainGoPath = join(servicePath, 'cmd', fnDir, 'main.go');

  if (!existsSync(mainGoPath)) {
    errors.push(`[V14] Directorio cmd/${fnDir} no tiene main.go`);
  }
}

// --- Report results ---
if (errors.length > 0) {
  console.error(`\nValidación fallida para ${serviceConfig.name} (${errors.length} errores):\n`);

  for (const error of errors) {
    console.error(`  ✗ ${error}`);
  }

  console.error('');
  process.exit(1);
}

console.log(`✓ Validación correcta para ${serviceConfig.name} (14 checks), stage=${stage}, region=${region}`);

}

main();
