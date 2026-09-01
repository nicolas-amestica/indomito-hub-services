/**
 * Registro de microservicios del backend.
 *
 * Cada servicio en `services/<nombre>/` se declara aqui para tener un unico
 * lugar donde consultar los nombres reales (evita strings sueltos en cada
 * `serverless.ts`). El nombre debe coincidir exactamente con el directorio.
 *
 * Ejemplo al crear `services/api-viajes/`:
 *
 *   export const ApiServices = {
 *     Viajes: 'api-viajes',
 *   } as const;
 *
 * Y en `services/api-viajes/serverless.ts`:
 *
 *   const config = buildGoServiceServerless(ApiServices.Viajes, [...endpoints]);
 */
export const ApiServices = {} as const;
