import { execFileSync } from 'node:child_process';
import { writeFileSync } from 'node:fs';
import { basename, dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

type Stage = 'dev' | 'prd';

interface HttpRoute {
  service: string;
  functionName: string;
  description: string;
  method: string;
  path: string;
  authenticated: boolean;
  stages: Stage[];
}

interface ServerlessFunction {
  description?: string;
  events?: Array<{
    httpApi?: {
      method: string;
      path: string;
      authorizer?: unknown;
    };
  }>;
}

interface ServerlessConfig {
  service?: string;
  functions?: Record<string, ServerlessFunction>;
}

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const output = resolve(root, 'postman/collections/indomito-services.postman_collection.json');
const serviceConfigs = [
  'services/api-catalog/serverless.ts',
  'services/api-contract/serverless.ts',
  'services/api-identity/serverless.ts',
  'services/api-inbox/serverless.ts',
  'services/api-meet/serverless.ts',
  'services/api-payment/serverless.ts',
  'services/api-program/serverless.ts',
];

function stageArgument(): Stage | undefined {
  const index = process.argv.indexOf('--extract-stage');
  const value = index >= 0 ? process.argv[index + 1] : undefined;
  return value === 'dev' || value === 'prd' ? value : undefined;
}

async function extract(stage: Stage): Promise<HttpRoute[]> {
  const routes: HttpRoute[] = [];

  for (const relativePath of serviceConfigs) {
    const moduleUrl = pathToFileURL(resolve(root, relativePath)).href;
    const imported = await import(moduleUrl);
    const config = imported.default as ServerlessConfig;
    const service = config.service ?? basename(dirname(relativePath));

    for (const [functionName, fn] of Object.entries(config.functions ?? {})) {
      for (const event of fn.events ?? []) {
        if (!event.httpApi) continue;
        routes.push({
          service,
          functionName,
          description: fn.description ?? functionName,
          method: event.httpApi.method.toUpperCase(),
          path: event.httpApi.path,
          authenticated: Boolean(event.httpApi.authorizer),
          stages: [stage],
        });
      }
    }
  }

  return routes;
}

function extractInChild(stage: Stage): HttpRoute[] {
  const stdout = execFileSync(
    'npx',
    ['tsx', fileURLToPath(import.meta.url), '--extract-stage', stage],
    {
      cwd: root,
      encoding: 'utf8',
      env: { ...process.env, APP_STAGE: stage, STAGE: stage, SLS_STAGE: stage, REGION: 'us-east-1' },
    },
  );
  return JSON.parse(stdout) as HttpRoute[];
}

function mergeRoutes(...sets: HttpRoute[][]): HttpRoute[] {
  const merged = new Map<string, HttpRoute>();
  for (const routes of sets) {
    for (const route of routes) {
      const key = `${route.service}|${route.method}|${route.path}|${route.functionName}`;
      const current = merged.get(key);
      if (current) {
        current.stages = [...new Set([...current.stages, ...route.stages])].sort() as Stage[];
      } else {
        merged.set(key, { ...route });
      }
    }
  }
  return [...merged.values()].sort((a, b) =>
    a.service.localeCompare(b.service) || a.path.localeCompare(b.path) || a.method.localeCompare(b.method),
  );
}

function postmanPath(path: string): string {
  return path.replaceAll(/\{([^}]+)\}/g, '{{$1}}');
}

function requestName(route: HttpRoute): string {
  return `${route.method} ${route.path}`;
}

function requestItem(route: HttpRoute): Record<string, unknown> {
  const hasBody = ['POST', 'PUT', 'PATCH'].includes(route.method);
  const headers = hasBody ? [{ key: 'Content-Type', value: 'application/json', type: 'text' }] : [];
  const stages = route.stages.map((stage) => stage.toUpperCase()).join(', ');
  const request: Record<string, unknown> = {
    method: route.method,
    header: headers,
    url: { raw: `{{baseUrl}}${postmanPath(route.path)}`, host: ['{{baseUrl}}'], path: [] },
    description: `${route.description}\n\nFunción: ${route.functionName}\nStages: ${stages}\nAutenticación: ${route.authenticated ? 'Bearer JWT' : 'Pública o autenticación propia del proveedor'}.`,
  };

  if (!route.authenticated) request.auth = { type: 'noauth' };
  if (hasBody) {
    request.body = {
      mode: 'raw',
      raw: '{}',
      options: { raw: { language: 'json' } },
    };
  }

  return { name: requestName(route), request, response: [] };
}

async function main(): Promise<void> {
  const requestedStage = stageArgument();
  if (requestedStage) {
    process.stdout.write(JSON.stringify(await extract(requestedStage)));
    return;
  }

  const routes = mergeRoutes(extractInChild('dev'), extractInChild('prd'));
  const services = [...new Set(routes.map((route) => route.service))];
  const collection = {
    info: {
      _postman_id: '035edfd7-b929-4d61-ae4c-f329096118db',
      name: 'Indómito Hub — Services API',
      description: [
        'Colección generada desde los serverless.ts de los servicios HTTP de Indómito Hub.',
        'No contiene credenciales ni ejecuta solicitudes automáticamente.',
        'Los procesadores payment-monitoring y payment-receipts son asíncronos y no exponen rutas HTTP.',
        'Selecciona baseUrlDev o baseUrlPrd en baseUrl y completa accessToken después de iniciar sesión.',
      ].join('\n\n'),
      schema: 'https://schema.getpostman.com/json/collection/v2.1.0/collection.json',
    },
    auth: {
      type: 'bearer',
      bearer: [{ key: 'token', value: '{{accessToken}}', type: 'string' }],
    },
    variable: [
      { key: 'baseUrl', value: '{{baseUrlDev}}', type: 'string' },
      { key: 'baseUrlDev', value: 'https://api.dev.girasindomito.cl', type: 'string' },
      { key: 'baseUrlPrd', value: 'https://api.girasindomito.cl', type: 'string' },
      { key: 'accessToken', value: '', type: 'string' },
    ],
    item: services.map((service) => ({
      name: service,
      description: `Endpoints HTTP del servicio ${service}.`,
      item: routes.filter((route) => route.service === service).map(requestItem),
    })),
  };

  writeFileSync(output, `${JSON.stringify(collection, null, 2)}\n`, 'utf8');
  console.log(`Colección generada: ${output}`);
  console.log(`Servicios HTTP: ${services.length}`);
  console.log(`Requests: ${routes.length}`);
}

await main();
