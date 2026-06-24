/**
 * autoProviders.js — Resolución de valores `auto` de los configFields.
 *
 * Lógica PURA (sin Svelte, sin fetch) · testeable de forma aislada.
 *
 * Cuando un configField del catálogo declara `auto: { provider: "..." }`,
 * NimOS pre-rellena ese campo automáticamente. Este módulo decide QUÉ valor
 * poner según el provider y un contexto (dominio DDNS, IP local, etc.).
 *
 * El contexto (ctx) lo aporta quien llama (el modal), que lo obtiene de la
 * configuración real de NimOS (Network/DDNS, IP del NAS...). Así este módulo
 * no hace I/O · solo transforma datos → testeable.
 *
 * Ver contrato: APP-CATALOG-SCHEMA.md (sección `auto · providers`).
 */

/**
 * Construye el sufijo de puerto siguiendo la MISMA regla que ExposeAppModal:
 * el puerto estándar HTTPS (443) se omite; cualquier otro se muestra.
 *   port 443  → ""           (https://dominio)
 *   port 444  → ":444"       (https://dominio:444)
 * @param {number|undefined} port
 * @returns {string}
 */
export function portSuffix(port) {
  if (!port || port === 443) return '';
  return `:${port}`;
}

/**
 * Resuelve el valor de un campo con `auto`.
 *
 * @param {object} auto    El objeto auto del configField, ej. { provider: "domain" }.
 * @param {object} ctx     Contexto con los datos que NimOS conoce:
 *   {
 *     appName:    string,  // nombre de la app (para el subdominio)
 *     baseDomain: string,  // dominio DDNS, ej. "midominio.duckdns.org" ('' si no hay)
 *     httpsPort:  number,  // puerto HTTPS de Caddy (443, 444...)
 *     localIp:    string,  // IP local del NAS, ej. "192.168.1.131"
 *     appPort:    number,  // puerto del contenedor de la app, ej. 8008
 *     hostname:   string,  // hostname del NAS, ej. "raspberrypi"
 *   }
 * @returns {string} El valor resuelto, o '' si no se puede (sin romper).
 */
export function resolveAuto(auto, ctx) {
  if (!auto || typeof auto !== 'object' || !auto.provider) return '';
  const c = ctx || {};
  switch (auto.provider) {
    case 'domain': {
      // subdominio (nombre app) + dominio base + puerto.
      // Si auto.port === false, se OMITE el puerto · necesario para valores que
      // son identidad y no URL de acceso (ej. SERVER_NAME de Matrix · el puerto
      // lo gestiona Caddy, no Matrix · meter ":444" rompe la config de Synapse).
      if (!c.baseDomain) return '';
      const sub = slugifyAppName(c.appName);
      const port = auto.port === false ? '' : portSuffix(c.httpsPort);
      return `${sub}.${c.baseDomain}${port}`;
    }
    case 'domain_root': {
      // solo el dominio base (sin subdominio) + puerto (omitible con port:false)
      if (!c.baseDomain) return '';
      const port = auto.port === false ? '' : portSuffix(c.httpsPort);
      return `${c.baseDomain}${port}`;
    }
    case 'local_ip': {
      // IP local + puerto del contenedor · para acceso en red local
      if (!c.localIp) return '';
      const p = c.appPort ? `:${c.appPort}` : '';
      return `${c.localIp}${p}`;
    }
    case 'hostname':
      return c.hostname || '';
    default:
      // Provider desconocido (ej. random_password · se resuelve en backend,
      // no aquí). Devolver '' · el campo queda vacío para que el usuario decida
      // o el backend lo resuelva.
      return '';
  }
}

/**
 * Convierte el nombre de una app en un slug válido para subdominio.
 *   "Matrix Server (Synapse)" → "matrix-server-synapse"
 *   "VS Code"                 → "vs-code"
 * Minúsculas, solo [a-z0-9-], sin guiones dobles ni en extremos.
 * @param {string} name
 * @returns {string}
 */
export function slugifyAppName(name) {
  if (!name) return 'app';
  const slug = String(name)
    .toLowerCase()
    .normalize('NFD')
    .replace(/[\u0300-\u036f]/g, '') // quitar acentos
    .replace(/[^a-z0-9]+/g, '-')     // no alfanumérico → guion
    .replace(/-+/g, '-')             // guiones dobles → uno
    .replace(/^-|-$/g, '');          // sin guiones en extremos
  return slug || 'app';
}

/**
 * Extrae el subdominio de un dominio completo, dado el dominio base.
 *
 * Para la auto-exposición: el usuario confirmó SERVER_NAME (ej.
 * "matrix.nimosbarraca1.duckdns.org:444") y necesitamos el subdominio ("matrix")
 * para registrarlo en Network. El puerto (:444) se ignora · es de Caddy, no
 * parte del subdominio.
 *
 * @param {string} fullDomain  ej. "matrix.nimosbarraca1.duckdns.org:444"
 * @param {string} baseDomain  ej. "nimosbarraca1.duckdns.org"
 * @returns {string}  el subdominio ("matrix"), o '' si no se puede extraer
 *   · si fullDomain == baseDomain (sin subdominio) → ''
 *   · si no encaja con baseDomain → '' (se asume manual/raro)
 */
export function extractSubdomain(fullDomain, baseDomain) {
  if (!fullDomain || !baseDomain) return '';
  // Quitar el puerto (:444) si lo lleva.
  const host = String(fullDomain).split(':')[0].trim();
  const base = String(baseDomain).split(':')[0].trim();
  if (host === base) return ''; // es el dominio raíz, sin subdominio
  // Debe terminar en ".baseDomain" para ser un subdominio válido.
  const suffix = '.' + base;
  if (!host.endsWith(suffix)) return ''; // no encaja · raro/manual
  return host.slice(0, host.length - suffix.length);
}
