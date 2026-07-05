# Release con artefactos pre-compilados — plan

> **Estado:** deuda técnica de release-engineering · **Prioridad:** media · **Origen:** hallazgo #5.3 de la auditoría de seguridad (2026-07-01).
>
> Describe cómo pasar de "el instalador compila TODO en el destino" a "el instalador
> **descarga artefactos ya compilados y verificados**". Elimina del NAS la cadena de
> compilación entera (Go, Node, build-essential) y, con ello, el riesgo de supply-chain de
> instalar toolchains de terceros. No es un parche: cambia el **modelo de release**. Se
> documenta para tenerlo visible y accionable.

## 1. Contexto y motivación

Hoy `install.sh` **construye todo en la máquina destino**:

| Artefacto | Se compila con | Toolchain que exige en el destino |
|---|---|---|
| Daemon (`nimos-daemon`) | `go build` (`install.sh`, ~L343) | **Go** + build-essential |
| NimTorrent | `make` (`install.sh`, ~L236) | **g++/make** (C++) |
| Frontend (`dist/`) | `npm install` + `npm run build` (~L710-719) | **Node.js** (via NodeSource) |

Problemas:
- **Supply-chain**: instalar Go, Node (NodeSource) y libs de build en el NAS = mucha superficie
  de confianza en terceros. El #5.2 endureció NodeSource (repo firmado por keyring), pero la
  dependencia sigue ahí.
- **UX/robustez**: un NAS no debería necesitar un entorno de compilación; los builds fallan por
  versiones, memoria (el Pi Zero!), red, etc.
- **Reproducibilidad**: cada usuario compila su propio binario → no hay un artefacto conocido y verificable.

**Objetivo**: el destino solo **descarga + verifica + coloca** binarios ya hechos. Sin Go, sin Node, sin make.

## 2. Arquitectura objetivo

```
┌────────────── CI (GitHub Actions), en cada tag ──────────────┐
│  build daemon (Go)  +  build NimTorrent (C++)  +  build dist │
│  → tarball(s) por arquitectura  +  checksums (+ FIRMA Ed25519)│
│  → publicar en GitHub Releases                                │
└───────────────────────────────────────────────────────────────┘
                              │  descarga
                              ▼
        install.sh (destino):  fetch release de su arch
                               → sha256sum -c  (y verificar firma)
                               → colocar binario + dist/ + unidades
                               → configurar y arrancar   (SIN compilar)
```

- **CI**: workflow `.github/workflows/release.yml` que en un tag (`v0.9.0-alpha`) compila y
  publica. Multi-arch si aplica (amd64 y arm64/armv7 para Raspberry Pi).
- **GitHub Releases**: aloja los tarballs versionados. **No** commitear `dist/`/binarios al repo
  (ensucia git, conflictos, tamaño).
- **install.sh**: sección de build → sección de descarga. Detecta arch (`uname -m`), baja el
  artefacto correcto, **verifica checksum** (reusar el patrón del #5.1) y opcionalmente **firma**.

## 3. Reutilizar lo que ya tenemos 🔑

- **Checksums** (#5.1): mismo mecanismo `.sha256` + `sha256sum -c`, ahora sobre el tarball de release.
- **Firma Ed25519** (¡como el feed de NimShield Intelligence!): ya tenemos toda la maquinaria de
  firmar/verificar con clave privada aislada. Firmar los artefactos de release con esa misma idea
  daría integridad **contra compromiso del repo/CDN**, no solo transporte. Alto valor, bajo coste incremental.

## 4. Consideraciones / partes a decidir

- **Multi-arch**: ¿solo amd64, o también arm (Pi)? Define la matriz del CI.
- **Fallback "build desde fuente"**: mantener un flag `--from-source` para dev/arquitecturas no
  publicadas, que use el flujo de compilación actual.
- **Versionado/tagging**: disciplina de tags = releases. El install por defecto baja el "latest"
  release; para reproducibilidad, permitir fijar versión.
- **Tamaño**: dist + binarios por release; GitHub Releases lo aguanta de sobra.
- **Validación**: **requiere probar un install limpio en una VM** (no se puede validar de otra forma).

## 5. Plan por fases

- **Fase 1**: workflow de CI que compile los 3 artefactos y los suba como assets de un release (con `.sha256`).
- **Fase 2**: añadir **firma Ed25519** de los artefactos (reusar la maquinaria del feed).
- **Fase 3**: reescribir `install.sh` → detectar arch, descargar release, verificar checksum+firma, colocar, configurar. Mantener `--from-source` como fallback.
- **Fase 4**: probar install limpio en VM(s) por arch (amd64 y, si aplica, arm). Documentar.

## 6. Criterios de aceptación

- [ ] El destino instala **sin** Go, Node ni build-essential.
- [ ] Los artefactos se **verifican** (checksum, idealmente firma) antes de ejecutarse/colocarse.
- [ ] Existe fallback `--from-source` para dev / arquitecturas no publicadas.
- [ ] Un install limpio en VM funciona de punta a punta.

## 7. Alcance / esfuerzo

Medio-alto: es release-engineering (CI + releases + reescritura del install), no una edición
puntual. Con el **#5.2 ya hecho**, el riesgo agudo de NodeSource ya bajó, así que esto es
"mejora correcta para beta pública", no urgente. Encaja hacerlo cuando se monte el pipeline de
releases formal — junto a, o justo después de, la firma de artefactos.
