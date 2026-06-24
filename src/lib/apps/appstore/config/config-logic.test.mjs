import { resolveAuto, slugifyAppName, portSuffix, extractSubdomain } from './autoProviders.js';
import { needsConfigModal, collectFields, initialValues, validateValue, validateAll, splitValuesByDestination, escapeDollarForEnv, escapeUserEnv, collectSecretKeys, needsDomainContext } from './configSchema.js';

let pass = 0, fail = 0;
function eq(got, want, name) {
  const g = JSON.stringify(got), w = JSON.stringify(want);
  if (g === w) { pass++; console.log(`✅ ${name}`); }
  else { fail++; console.log(`❌ ${name}\n   got:  ${g}\n   want: ${w}`); }
}

// ── slugifyAppName ──
eq(slugifyAppName("Matrix Server (Synapse)"), "matrix-server-synapse", "slug matrix");
eq(slugifyAppName("VS Code"), "vs-code", "slug vscode");

// ── portSuffix (regla NimOS: 443 se omite) ──
eq(portSuffix(443), "", "port 443 omitido");
eq(portSuffix(444), ":444", "port 444 mostrado");
eq(portSuffix(undefined), "", "port undefined");

// ── resolveAuto domain ──
const ctx = { appName: "Matrix Server (Synapse)", baseDomain: "midominio.duckdns.org", httpsPort: 444, localIp: "192.168.1.131", appPort: 8008, hostname: "raspberrypi" };
eq(resolveAuto({provider:"domain"}, ctx), "matrix-server-synapse.midominio.duckdns.org:444", "auto domain");
eq(resolveAuto({provider:"domain_root"}, ctx), "midominio.duckdns.org:444", "auto domain_root");
eq(resolveAuto({provider:"local_ip"}, ctx), "192.168.1.131:8008", "auto local_ip");
eq(resolveAuto({provider:"hostname"}, ctx), "raspberrypi", "auto hostname");
eq(resolveAuto({provider:"domain"}, {appName:"X", baseDomain:""}), "", "auto domain sin baseDomain");
eq(resolveAuto({provider:"random_password"}, ctx), "", "auto random no resuelve en frontend");

// ── needsConfigModal ──
eq(needsConfigModal({configFields:[{key:"X"}]}), true, "needsModal con configFields");
eq(needsConfigModal({postInstall:[{id:"a"}]}), true, "needsModal con postInstall");
eq(needsConfigModal({name:"Jellyfin"}), false, "needsModal jellyfin no");
eq(needsConfigModal(null), false, "needsModal null");

// ── collectFields (marca origen) ──
const cat = { configFields:[{key:"SERVER_NAME"}], postInstall:[{id:"create_admin", fields:[{key:"ADMIN_USER"},{key:"ADMIN_PASS"}]}] };
const fields = collectFields(cat);
eq(fields.length, 3, "collect 3 campos");
eq(fields[0]._kind, "env", "SERVER_NAME es env");
eq(fields[1]._kind, "postInstall", "ADMIN_USER es postInstall");
eq(fields[1]._actionId, "create_admin", "ADMIN_USER actionId");

// ── initialValues (defaults + auto) ──
const f2 = collectFields({configFields:[{key:"SERVER_NAME", auto:{provider:"domain"}},{key:"PORT", default:"8008"}]});
const iv = initialValues(f2, ctx);
eq(iv.SERVER_NAME, "matrix-server-synapse.midominio.duckdns.org:444", "initial auto domain");
eq(iv.PORT, "8008", "initial default");

// ── validateValue ──
eq(validateValue({type:"hostname"}, "matrix.midominio.org"), true, "hostname OK");
eq(validateValue({type:"hostname"}, "matrix.midominio.org:444"), true, "hostname con puerto OK");
eq(validateValue({type:"hostname"}, "MAYUS.com"), "Debe ser un nombre de host válido (ej. matrix.midominio.org)", "hostname mayus falla");
eq(validateValue({type:"port"}, "8008"), true, "port OK");
eq(validateValue({type:"port"}, "99999"), "Puerto entre 1 y 65535", "port fuera rango");
eq(validateValue({regex:"^[a-z]+$"}, "abc"), true, "regex OK");
eq(validateValue({regex:"^[a-z]+$"}, "ABC"), "Formato no válido", "regex falla");
eq(validateValue(null, "loquesea"), true, "sin validation OK");

// ── validateAll ──
const fr = [{key:"USER", required:true},{key:"PASS", required:true, secret:true},{key:"DOM", validation:{type:"hostname"}}];
eq(validateAll(fr, {USER:"andres", PASS:"x", DOM:"matrix.org"}).ok, true, "validateAll OK");
eq(validateAll(fr, {USER:"", PASS:"x", DOM:"matrix.org"}).ok, false, "validateAll required falla");
eq(validateAll(fr, {USER:"a", PASS:"x", DOM:"MAYUS"}).ok, false, "validateAll hostname falla");

// ── splitValuesByDestination ──
const sp = splitValuesByDestination(fields, {SERVER_NAME:"matrix.org", ADMIN_USER:"andres", ADMIN_PASS:"secreto"});
eq(sp.env, {SERVER_NAME:"matrix.org"}, "split env");
eq(sp.postInstall, {ADMIN_USER:"andres", ADMIN_PASS:"secreto"}, "split postInstall");


// ── escapeDollarForEnv / escapeUserEnv (Pieza 2) ──
eq(escapeDollarForEnv("my$ecret"), "my$$ecret", "escape un dolar");
eq(escapeDollarForEnv("2266"), "2266", "escape sin dolar");
eq(escapeDollarForEnv("Casa#2024"), "Casa#2024", "escape hash intacto");
eq(escapeDollarForEnv(42), 42, "escape number intacto");
eq(escapeUserEnv({PASSWORD:"a$b", USER:"andres"}), {PASSWORD:"a$$b", USER:"andres"}, "escapeUserEnv objeto");


// ── collectSecretKeys (Fase 4) ──
const acts = [{id:"create_admin", fields:[{key:"ADMIN_USER"},{key:"ADMIN_PASS", secret:true}]}];
eq(collectSecretKeys(acts), ["ADMIN_PASS"], "collectSecretKeys solo secretos");
eq(collectSecretKeys([]), [], "collectSecretKeys vacio");
eq(collectSecretKeys(null), [], "collectSecretKeys null");


// ── needsDomainContext (Paso 1 · integración Network) ──
eq(needsDomainContext({configFields:[{key:"SERVER_NAME", auto:{provider:"domain"}}]}), true, "needsDomain con auto:domain");
eq(needsDomainContext({configFields:[{key:"X", auto:{provider:"domain_root"}}]}), true, "needsDomain con domain_root");
eq(needsDomainContext({configFields:[{key:"PASSWORD", type:"password"}]}), false, "needsDomain sin auto:domain (vscode)");
eq(needsDomainContext({configFields:[{key:"X", auto:{provider:"local_ip"}}]}), false, "needsDomain local_ip no cuenta");
eq(needsDomainContext(null), false, "needsDomain null");


// ── extractSubdomain (Paso 3 · auto-exposición) ──
eq(extractSubdomain("matrix.nimosbarraca1.duckdns.org:444", "nimosbarraca1.duckdns.org"), "matrix", "extractSub con puerto");
eq(extractSubdomain("nimosbarraca1.duckdns.org", "nimosbarraca1.duckdns.org"), "", "extractSub dominio raiz");
eq(extractSubdomain("otra.com", "nimosbarraca1.duckdns.org"), "", "extractSub no encaja");

console.log(`\n═══ ${pass} pass, ${fail} fail ═══`);
process.exit(fail > 0 ? 1 : 0);
