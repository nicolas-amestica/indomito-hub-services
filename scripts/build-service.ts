import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, rmSync } from 'node:fs';
import { join, resolve } from 'node:path';

type ServiceConfig = {
  defaultArtifactDir?: string;
  defaultEntrypoint?: string;
  name: string;
  functions: Array<{
    name: string;
    entrypoint?: string;
    artifact?: string;
  }>;
};

function getArg(name: string): string {
  const index = process.argv.indexOf(`--${name}`);

  if (index === -1 || !process.argv[index + 1]) {
    throw new Error(`Missing required argument --${name}`);
  }

  return process.argv[index + 1];
}

const servicePath = resolve(getArg('service'));
const configPath = join(servicePath, 'service.config.json');

const serviceConfig = JSON.parse(readFileSync(configPath, 'utf8')) as ServiceConfig;
const artifactsDir = join(servicePath, '.serverless-artifacts');
const defaultArtifactDir = serviceConfig.defaultArtifactDir ?? '.serverless-artifacts';
const builtEntrypoints = new Map<string, string>();

if (existsSync(artifactsDir)) {
  rmSync(artifactsDir, { recursive: true, force: true });
}

mkdirSync(artifactsDir, { recursive: true });

for (const fn of serviceConfig.functions) {
  const entrypoint = fn.entrypoint ?? serviceConfig.defaultEntrypoint;
  const artifact = fn.artifact ?? join(defaultArtifactDir, `${fn.name}.zip`);

  if (!entrypoint) {
    throw new Error(`Missing entrypoint for function ${fn.name}`);
  }

  const outputDir = join(artifactsDir, fn.name);
  const compiledBootstrapPath = join(artifactsDir, '.compiled', fn.name, 'bootstrap');
  const zipPath = join(servicePath, artifact);

  mkdirSync(outputDir, { recursive: true });

  let bootstrapPath = builtEntrypoints.get(entrypoint);

  if (!bootstrapPath) {
    bootstrapPath = compiledBootstrapPath;
    mkdirSync(join(artifactsDir, '.compiled', fn.name), { recursive: true });

    console.log(`Compilando ${serviceConfig.name}/${entrypoint}`);

    execFileSync(
      'go',
      [
        'build',
        '-tags',
        'lambda.norpc',
        '-ldflags',
        '-s -w',
        '-o',
        bootstrapPath,
        entrypoint,
      ],
      {
        cwd: servicePath,
        stdio: 'inherit',
        env: {
          ...process.env,
          GOOS: 'linux',
          GOARCH: 'arm64',
          CGO_ENABLED: '0',
        },
      },
    );

    builtEntrypoints.set(entrypoint, bootstrapPath);
  }

  console.log(`Creando ZIP ${zipPath}`);

  execFileSync('zip', ['-j', zipPath, bootstrapPath], {
    cwd: servicePath,
    stdio: 'inherit',
  });
}

console.log(`Build finalizado para ${serviceConfig.name}`);
