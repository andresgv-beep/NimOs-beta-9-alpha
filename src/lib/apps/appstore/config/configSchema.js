/**
 * configSchema.js — Lógica del contrato de configuración de apps.
 *
 * Lógica PURA (sin Svelte, sin fetch) · testeable de forma aislada.
 *
 * Interpreta los campos del catálogo (configFields, postInstall) según el
 * contrato definido en APP-CATALOG-SCHEMA.md. Decide:
 *   · si una app necesita abrir el modal de config
 *   · qué campos mostrar
 *   · si los valores introducidos son válidos
 *
 * NO hace I/O · solo transforma datos. El modal (Svelte) usa estas funciones.
 */

import { resolveAuto } from './autoProviders.js';

/**
 * ¿La app necesita abrir el modal de configuración?
 * Se INFIERE (no hay flag configApp · ver contrato): si tiene configFields o
 * postInstall, hay que pedir datos antes de instalar.
 * @param {object} catalog  La entrada de catálogo de la app.
 * @returns {boolean}
 */
export function needsConfigModal(catalog) {
  if (!catalog || typeof catalog !== 'object') return false;
  const hasFields = Array.isArray(catalog.configFields) && catalog.configFields.length > 0;
  const hasPost = Array.isArray(catalog.postInstall) && catalog.postInstall.length > 0;
  return hasFields || hasPost;
}

/**
 * Reúne TODOS los campos que el modal debe mostrar:
 *   · configFields (Tipo A · variables → .env, antes de arrancar)
 *   · campos de cada postInstall.fields (Tipo B · admin, etc., tras arrancar)
 * Cada campo se marca con su origen (`_kind`) para que el backend sepa qué
 * hacer: 'env' va al .env; 'postInstall' se usa en el comando posterior.
 * @param {object} catalog
 * @returns {Array<object>} lista de campos normalizados
 */
export function collectFields(catalog) {
  const out = [];
  if (!catalog) return out;

  if (Array.isArray(catalog.configFields)) {
    for (const f of catalog.configFields) {
      out.push({ ...f, _kind: 'env' });
    }
  }
  if (Array.isArray(catalog.postInstall)) {
    for (const action of catalog.postInstall) {
      if (Array.isArray(action.fields)) {
        for (const f of action.fields) {
          out.push({ ...f, _kind: 'postInstall', _actionId: action.id });
        }
      }
    }
  }
  return out;
}

/**
 * Calcula los valores INICIALES de los campos (defaults + auto resueltos).
 * @param {Array<object>} fields  De collectFields.
 * @param {object} ctx            Contexto para resolveAuto (dominio, IP...).
 * @returns {object}  { key: valorInicial }
 */
export function initialValues(fields, ctx) {
  const values = {};
  for (const f of fields) {
    if (!f.key) continue;
    if (f.auto) {
      const resolved = resolveAuto(f.auto, ctx);
      values[f.key] = resolved !== '' ? resolved : (f.default ?? '');
    } else {
      values[f.key] = f.default ?? '';
    }
  }
  return values;
}

/**
 * Valida un valor contra la regla `validation` de su campo.
 * @param {object} validation  ej. { type: "hostname" } o { regex: "^...$" }
 * @param {string} value
 * @returns {true|string}  true si OK; mensaje de error si no.
 */
export function validateValue(validation, value) {
  if (!validation) return true;
  const v = String(value ?? '');

  if (validation.regex) {
    try {
      const re = new RegExp(validation.regex);
      return re.test(v) ? true : 'Formato no válido';
    } catch {
      return true; // regex malformada en catálogo · no bloquear al usuario
    }
  }

  switch (validation.type) {
    case 'hostname':
      // host válido, opcionalmente con :puerto. Minúsculas, dígitos, . y -
      return /^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:\d{1,5})?$/.test(v)
        ? true
        : 'Debe ser un nombre de host válido (ej. matrix.midominio.org)';
    case 'email':
      return /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(v) ? true : 'Email no válido';
    case 'url':
      return /^https?:\/\/.+/.test(v) ? true : 'Debe empezar por http:// o https://';
    case 'port': {
      const n = Number(v);
      return Number.isInteger(n) && n >= 1 && n <= 65535 ? true : 'Puerto entre 1 y 65535';
    }
    case 'none':
    default:
      return true;
  }
}

/**
 * Valida TODOS los campos contra sus valores. Comprueba required y validation.
 * @param {Array<object>} fields
 * @param {object} values  { key: valor }
 * @returns {object}  { ok: boolean, errors: { key: mensaje } }
 */
export function validateAll(fields, values) {
  const errors = {};
  for (const f of fields) {
    if (!f.key) continue;
    const val = values[f.key];

    // required: no vacío
    if (f.required && (val === undefined || val === null || String(val).trim() === '')) {
      errors[f.key] = 'Este campo es obligatorio';
      continue;
    }
    // validation: solo si hay valor (un campo opcional vacío no se valida)
    if (val !== undefined && val !== null && String(val).trim() !== '') {
      const res = validateValue(f.validation, val);
      if (res !== true) errors[f.key] = res;
    }
  }
  return { ok: Object.keys(errors).length === 0, errors };
}

/**
 * Separa los valores por destino para mandarlos al backend:
 *   · env: van al .env (configFields, Tipo A)
 *   · postInstall: se usan en los comandos posteriores (Tipo B)
 * @param {Array<object>} fields  De collectFields (con _kind).
 * @param {object} values
 * @returns {object}  { env: {...}, postInstall: {...} }
 */
export function splitValuesByDestination(fields, values) {
  const env = {};
  const postInstall = {};
  for (const f of fields) {
    if (!f.key) continue;
    const val = values[f.key];
    if (f._kind === 'postInstall') {
      postInstall[f.key] = val;
    } else {
      env[f.key] = val;
    }
  }
  return { env, postInstall };
}

/**
 * Escapa el carácter `$` en un valor para el .env de docker-compose.
 *
 * CONTEXTO (verificado con tests reales en hardware · ENV-ESCAPE-HALLAZGO.md):
 * el ÚNICO carácter que rompe en el .env es el `$` (docker-compose lo
 * interpreta como inicio de variable · "my$ecret" → "my"). Los demás (# espacio
 * = " etc.) funcionan sin tocar. La solución universal (interpolación Y
 * env_file) es duplicarlo: `$` → `$$`.
 *
 * IMPORTANTE · esto se aplica SOLO a valores que vienen del USUARIO (el modal).
 * NO se aplica a las referencias internas de NimOS (${CONFIG_PATH}, etc.) cuyo
 * `$` es sintaxis legítima. Por eso el escape vive aquí (frontend, donde se sabe
 * que el valor es del usuario) y NO en el backend (que ve los valores mezclados).
 *
 * @param {*} value
 * @returns {*}  El valor con `$` duplicado si es string; igual si no lo es.
 */
export function escapeDollarForEnv(value) {
  if (typeof value !== 'string') return value;
  return value.replace(/\$/g, '$$$$'); // en regex de replace, '$$' = un '$' literal → '$$$$' = '$$'
}

/**
 * Aplica escapeDollarForEnv a TODOS los valores de un objeto (los del modal).
 * @param {object} values  { key: valor }
 * @returns {object}  nuevo objeto con los `$` escapados en los strings
 */
export function escapeUserEnv(values) {
  const out = {};
  for (const [k, v] of Object.entries(values || {})) {
    out[k] = escapeDollarForEnv(v);
  }
  return out;
}

/**
 * Recoge las CLAVES de los campos marcados secret:true dentro de las acciones
 * postInstall. El backend las usa para ofuscar esos valores en los logs.
 *
 * @param {Array<object>} actions  postInstall del catálogo (cada uno con fields[])
 * @returns {string[]}  claves secretas (ej. ["ADMIN_PASS"])
 */
export function collectSecretKeys(actions) {
  const keys = [];
  if (!Array.isArray(actions)) return keys;
  for (const action of actions) {
    if (!Array.isArray(action.fields)) continue;
    for (const f of action.fields) {
      if (f.secret && f.key) keys.push(f.key);
    }
  }
  return keys;
}

/**
 * ¿La config tiene algún campo que use auto:domain o auto:domain_root?
 * Sirve para decidir si hay que cargar los datos de Network (baseDomain, puerto)
 * antes de abrir el modal. Las apps sin campo de dominio (Jellyfin) no lo
 * necesitan · no se molesta a Network.
 *
 * @param {object} catalog  la config de la app (configFields)
 * @returns {boolean}
 */
export function needsDomainContext(catalog) {
  if (!catalog || !Array.isArray(catalog.configFields)) return false;
  return catalog.configFields.some(
    (f) => f.auto && (f.auto.provider === 'domain' || f.auto.provider === 'domain_root')
  );
}
