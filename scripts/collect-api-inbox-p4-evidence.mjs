#!/usr/bin/env node

import { createHash } from 'node:crypto';
import { readFile, writeFile } from 'node:fs/promises';
import process from 'node:process';

const forbiddenKey = /(authorization|token|secret|phone|telefono|payload|body|messageText|accessToken)/i;
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

const fail = message => {
  throw new Error(message);
};

const rejectSensitiveKeys = (value, path = '$') => {
  if (Array.isArray(value)) return value.forEach((item, index) => rejectSensitiveKeys(item, `${path}[${index}]`));
  if (!value || typeof value !== 'object') return;
  for (const [key, child] of Object.entries(value)) {
    if (forbiddenKey.test(key)) fail(`campo sensible no permitido: ${path}.${key}`);
    rejectSensitiveKeys(child, `${path}.${key}`);
  }
};

const fingerprint = value => createHash('sha256').update(value).digest('hex').slice(0, 12);

export const collectEvidence = input => {
  rejectSensitiveKeys(input);
  if (input.stage !== 'dev') fail('stage debe ser dev');
  if (typeof input.runId !== 'string' || !input.runId.trim()) fail('runId es obligatorio');
  if (!Array.isArray(input.records) || input.records.length === 0) fail('records debe contener evidencia');

  const records = input.records.map((record, index) => {
    const label = `records[${index}]`;
    if (!uuid.test(record.clientMessageId || '')) fail(`${label}.clientMessageId invalido`);
    if (typeof record.wamid !== 'string' || !record.wamid.startsWith('wamid.')) fail(`${label}.wamid invalido`);
    if (record.persistence?.clientMessageId !== record.clientMessageId) fail(`${label} no correlaciona persistencia`);
    if (record.realtime?.clientMessageId !== record.clientMessageId) fail(`${label} no correlaciona realtime`);
    if (!['committed', 'sent', 'delivered', 'read'].includes(record.persistence?.state)) fail(`${label}.persistence.state invalido`);
    if (!Array.isArray(record.metaStatuses) || record.metaStatuses.length === 0) fail(`${label}.metaStatuses vacio`);
    return {
      capability: String(record.capability || 'unknown'),
      correlation: fingerprint(`${record.clientMessageId}:${record.wamid}`),
      persistenceState: record.persistence.state,
      realtimeEventType: String(record.realtime.eventType || ''),
      metaStatuses: record.metaStatuses.map(String),
      correlated: true,
    };
  });

  return {
    schemaVersion: 1,
    runId: input.runId,
    stage: 'dev',
    generatedAt: new Date().toISOString(),
    summary: { total: records.length, correlated: records.length, failed: 0 },
    records,
  };
};

const main = async () => {
  const [inputPath, outputPath] = process.argv.slice(2);
  if (!inputPath) fail('uso: collect-api-inbox-p4-evidence.mjs <input.json> [output.json]');
  const input = JSON.parse(await readFile(inputPath, 'utf8'));
  const report = collectEvidence(input);
  const serialized = `${JSON.stringify(report, null, 2)}\n`;
  if (outputPath) await writeFile(outputPath, serialized, { mode: 0o600 });
  else process.stdout.write(serialized);
};

if (process.argv[1] && import.meta.url === new URL(`file://${process.argv[1]}`).href) {
  main().catch(error => {
    process.stderr.write(`Error: ${error.message}\n`);
    process.exitCode = 1;
  });
}
