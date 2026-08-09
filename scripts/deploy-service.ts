import { execFileSync, execSync } from 'node:child_process';
import { resolve } from 'node:path';

const AWS_PROFILES: Record<string, string> = {
  dev: 'pa-dev',
  qa: 'pa-qa',
  prd: 'pa-prd',
};

const COLORS = {
  green: '\x1b[32m',
  yellow: '\x1b[33m',
  red: '\x1b[31m',
  cyan: '\x1b[36m',
  reset: '\x1b[0m',
};

function log(color: keyof typeof COLORS, symbol: string, msg: string): void {
  console.log(`${COLORS[color]}${symbol}${COLORS.reset} ${msg}`);
}

function getArg(name: string, fallback?: string): string {
  const index = process.argv.indexOf(`--${name}`);

  if (index === -1 || !process.argv[index + 1]) {
    if (fallback !== undefined) return fallback;
    throw new Error(`Argumento requerido: --${name}`);
  }

  return process.argv[index + 1];
}

function hasFlag(name: string): boolean {
  return process.argv.includes(`--${name}`);
}

function checkAwsCli(): void {
  try {
    execFileSync('aws', ['--version'], { stdio: 'pipe' });
  } catch {
    log('red', '✗', 'AWS CLI no está instalado.');
    process.exit(1);
  }
}

function isSsoSessionActive(profile: string): boolean {
  try {
    execSync(`aws sts get-caller-identity --profile ${profile}`, {
      stdio: 'pipe',
      encoding: 'utf8',
    });
    return true;
  } catch {
    return false;
  }
}

function loginSso(profile: string): void {
  log('yellow', '⟳', `Sesión SSO expirada. Iniciando login para perfil "${profile}"...`);

  try {
    execFileSync('aws', ['sso', 'login', '--profile', profile], {
      stdio: 'inherit',
    });
  } catch {
    log('red', '✗', 'No se pudo completar el login SSO.');
    process.exit(1);
  }

  if (!isSsoSessionActive(profile)) {
    log('red', '✗', 'Login SSO completado pero las credenciales siguen sin funcionar.');
    process.exit(1);
  }
}

function exportCredentials(profile: string): Record<string, string> {
  try {
    const output = execSync(
      `aws configure export-credentials --profile ${profile} --format env`,
      { encoding: 'utf8', stdio: ['pipe', 'pipe', 'pipe'] },
    );

    const env: Record<string, string> = {};

    for (const line of output.split('\n')) {
      const match = line.match(/^export\s+(\w+)=(.*)$/);
      if (match) {
        env[match[1]] = match[2];
      }
    }

    if (!env.AWS_ACCESS_KEY_ID || !env.AWS_SECRET_ACCESS_KEY) {
      throw new Error('Credenciales incompletas');
    }

    return env;
  } catch {
    log('red', '✗', 'No se pudieron exportar las credenciales del perfil SSO.');
    log('yellow', '💡', 'Intenta: aws sso login --profile ' + profile);
    process.exit(1);
  }
}

function getCallerIdentity(env: Record<string, string>): { account: string; arn: string } {
  const output = execSync('aws sts get-caller-identity', {
    encoding: 'utf8',
    stdio: ['pipe', 'pipe', 'pipe'],
    env: { ...process.env, ...env },
  });

  const identity = JSON.parse(output);
  return { account: identity.Account, arn: identity.Arn };
}

// --- Main ---

const service = getArg('service');
const stage = getArg('stage', 'dev');
const region = getArg('region', 'us-east-1');
const skipBuild = hasFlag('skip-build');
const skipValidate = hasFlag('skip-validate');

const servicePath = resolve(service);
const profile = AWS_PROFILES[stage];

if (!profile) {
  log('red', '✗', `Stage "${stage}" no tiene perfil AWS configurado. Stages válidos: ${Object.keys(AWS_PROFILES).join(', ')}`);
  process.exit(1);
}

console.log('');
log('cyan', '🚀', `Deploy: ${service} → stage=${stage}, region=${region}, profile=${profile}`);
console.log('');

// 1. Verificar AWS CLI
checkAwsCli();
log('green', '✓', 'AWS CLI disponible');

// 2. Verificar sesión SSO activa
if (!isSsoSessionActive(profile)) {
  loginSso(profile);
}

log('green', '✓', `Sesión SSO activa (${profile})`);

// 3. Exportar credenciales para que Serverless las use
const awsCredentials = exportCredentials(profile);
const identity = getCallerIdentity(awsCredentials);
log('green', '✓', `Credenciales válidas — Account: ${identity.account}`);
log('green', ' ', `ARN: ${identity.arn}`);
console.log('');

// 4. Build
if (!skipBuild) {
  log('cyan', '⟳', 'Compilando servicio...');
  execFileSync('npx', ['tsx', 'scripts/build-service.ts', '--service', service], {
    cwd: resolve('.'),
    stdio: 'inherit',
    env: { ...process.env, ...awsCredentials },
  });
  log('green', '✓', 'Build completado');
  console.log('');
} else {
  log('yellow', '⊘', 'Build omitido (--skip-build)');
}

// 5. Validate
if (!skipValidate) {
  log('cyan', '⟳', 'Validando servicio...');
  execFileSync('npx', ['tsx', 'scripts/validate-service.ts', '--service', service, '--stage', stage, '--region', region], {
    cwd: resolve('.'),
    stdio: 'inherit',
    env: { ...process.env, ...awsCredentials, AWS_PROFILE: '' },
  });
  console.log('');
} else {
  log('yellow', '⊘', 'Validación omitida (--skip-validate)');
}

// 6. Deploy con Serverless Framework
log('cyan', '⟳', 'Desplegando con Serverless Framework...');

execFileSync('npx', ['serverless', 'deploy', '--stage', stage, '--region', region], {
  cwd: servicePath,
  stdio: 'inherit',
  env: {
    ...process.env,
    ...awsCredentials,
    STAGE: stage,
    NODE_OPTIONS: '--disable-warning=DEP0169',
    AWS_PROFILE: '',
  },
});

console.log('');
log('green', '✓', `Deploy exitoso: ${service} → ${stage} (${region})`);
console.log('');
