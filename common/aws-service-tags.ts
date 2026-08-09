import { STAGE } from './custom-parameters';

/**
 * Tags estándar para todos los recursos AWS de Indómito Hub.
 *
 * Convención: inglés kebab-case. Consistente entre api-gox-gh e ifr-sls-gh.
 */

/** Tags que aplican a nivel de provider (todos los recursos del stack). */
export function buildResourceTags(service: string): Record<string, string> {
  return {
    cc: 'pending',
    entorno: STAGE,
    seguridad: 'pri',
    servicio: service,
    repositorio: 'api-gox-gh',
    proyecto: 'indomito-hub',
    iac: 'serverless-framework-v4',
    equipo: 'equipo-indomito',
    contacto: 'contacto@indomitohub.cl',
  };
}
