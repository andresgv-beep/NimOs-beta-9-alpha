# Daemon en menor privilegio (no-root) — plan de endurecimiento

> **Estado:** deuda técnica de arquitectura · **Prioridad:** media (no urgente) · **Origen:** hallazgo #5.4 de la auditoría de seguridad (2026-07-01).
>
> Este documento describe la **opción D**: dejar de correr el daemon como root. Es el
> endurecimiento "de verdad" del privilegio; el resto de opciones (dejar el sudo con
> contraseña, sudo opt-in) son parches. No es un cambio para hacer con prisa: toca
> montajes, docker, systemctl, storage y red. Se documenta aquí para tenerlo visible.

## 1. Contexto y motivación

Hoy `nimos-daemon.service` corre **como root** (sin `User=` en el unit). Consecuencia:
cualquier **RCE o escape** en el daemon = **root instantáneo** en todo el NAS. El principio
de **menor privilegio** dice que el daemon debería tener solo las capacidades que necesita,
y que las operaciones privilegiadas concretas pasen por una superficie pequeña y auditable.

Dato de la auditoría: el sudo del usuario `nimos` **no** es passwordless (pide contraseña),
así que el riesgo real NO es "sudo sin contraseña" — es que **el daemon en sí es root**.
Este plan ataca eso.

Pista de que hubo intención previa de no-root: el código usa `sudo` en varios sitios
(redundante corriendo como root), como si se hubiera pensado para un daemon sin privilegios.

## 2. Inventario: qué hace el daemon que necesita privilegio

| Operación | Subsistema | Privilegio real | Dónde (código) |
|---|---|---|---|
| Arrancar/parar servicios de apps; `restart nimos` | systemd | root **o** sudoers acotado / polkit | `apps.go`, `hardware_system.go` (`sudo systemctl …`) |
| Montar/desmontar, btrfs, pools | storage | `CAP_SYS_ADMIN` (≈ root) o helper | `storage_*.go` |
| Crear/parar contenedores, imágenes | Docker | socket docker (**grupo docker ≈ root**) | `docker_*.go` |
| Firewall / nftables (exposición; futuro NimShield L3) | red | `CAP_NET_ADMIN` | `network_exposure_firewall.go` |
| Propiedad de ficheros en shares (chown) | files | `CAP_CHOWN` / `CAP_DAC_OVERRIDE` | files/storage |
| Espacio en disco (`df`) | files | **NINGUNO** — `df` no necesita root | `files_helpers.go` (`sudo df` es innecesario) |
| Escribir `dist/`, config | fichero | solo permisos de fichero (no root) | varios |

> **Fase 1 del plan es completar este inventario** con un grep exhaustivo (`sudo`, `exec`,
> `mount`, `nft`, `chown`, socket docker) — la tabla es el punto de partida, no la lista final.

## 3. Arquitectura objetivo

Daemon como usuario dedicado **`nimosd`** (no-root) + los privilegios justos:

1. **Capabilities mínimas** vía systemd (`AmbientCapabilities=`): p. ej. `CAP_NET_ADMIN`
   para nftables. Evitar `CAP_SYS_ADMIN` en el propio daemon (equivale casi a root).
2. **Helper privilegiado** (binario root separado, invocado por socket Unix con protocolo
   acotado — NO setuid a lo loco): ejecuta SOLO operaciones concretas con **validación
   estricta** de entrada. El daemon no-root pide "monta el pool X", "arranca el servicio de
   la app Y (de una **allowlist**)". Toda la superficie root queda en este helper pequeño y auditable.
3. **Docker**: `nimosd` en el grupo `docker` (con la advertencia de que **docker-group ≈ root**);
   alternativa más segura: **docker-socket-proxy** que autoriza solo las llamadas necesarias.
4. **sudoers.d acotado** (si se usa sudo en vez de helper): solo los comandos exactos, p. ej.
   `systemctl start|stop nimos-app-*`, nunca `ALL`.

## 4. Las partes difíciles (honestidad)

- **`CAP_SYS_ADMIN` ≈ root**: montar/btrfs necesita casi-root. De-privilegiar esto **de
  verdad** exige el **helper con validación**, no dar `CAP_SYS_ADMIN` al daemon (sería no
  ganar nada).
- **Socket docker ≈ root**: quien habla con el socket docker escala a root trivialmente. No
  es mejora real salvo con **proxy de autorización**.
- **Amplitud**: el daemon toca muchos subsistemas → migración por fases con riesgo de romper
  features. Hay que probar app-por-app, storage, red y files.

## 5. Mitigación INTERMEDIA (quick win, mientras siga root)

Aunque el daemon siga corriendo como root, **endurecer el unit systemd** reduce el blast
radius de un RCE **sin refactor**. Esto se puede hacer YA, independiente de D:

```ini
[Service]
ProtectSystem=strict
ReadWritePaths=/opt/nimos /var/lib/nimos /var/log/nimos   # solo lo que escribe
ProtectHome=true
PrivateTmp=true
ProtectKernelTunables=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK
RestrictNamespaces=true
MemoryDenyWriteExecute=true
SystemCallFilter=@system-service            # seccomp allowlist
SystemCallErrorNumber=EPERM
# NoNewPrivileges=true  ← ROMPE el 'sudo' interno; activar SOLO tras mover el sudo a helper/caps
```

> Ojo con `NoNewPrivileges=true`: **rompe** los `sudo …` que el daemon hace hoy. Activarlo
> solo cuando esos sudo se hayan movido a un helper o a capabilities. El resto de directivas
> son seguras de probar de una en una.

**Quick win extra**: quitar el `sudo df` de `files_helpers.go` (`df` no necesita root).

## 6. Plan por fases

- **Fase 0** (quick win, independiente de D): hardening del unit systemd (§5, sin `NoNewPrivileges`) + quitar el `sudo df` innecesario.
- **Fase 1**: inventario exhaustivo de toda operación privilegiada (§2 completada).
- **Fase 2**: helper privilegiado + mover `systemctl`/`mount`/`chown` detrás de él con allowlists estrictas.
- **Fase 3**: daemon a usuario `nimosd`, capabilities mínimas, docker-socket-proxy.
- **Fase 4**: pruebas por subsistema (apps, storage, network, files, shield) + plan de rollback.

## 7. Criterios de aceptación

- [ ] El daemon **no** corre como root.
- [ ] Un RCE en el daemon **no** da root directo — solo las operaciones acotadas del helper.
- [ ] Todas las features siguen funcionando: apps, storage, docker, red, files, NimShield.
- [ ] La superficie root (el helper) es pequeña, auditable y con validación de entrada.

## 8. Relación con NimShield

El futuro **NimShield L3 (nftables)** (ver roadmap del motor de comportamiento) también
necesitará `CAP_NET_ADMIN`. Conviene planificarlo **junto a esta migración**: si el daemon
pasa a no-root con `CAP_NET_ADMIN`, el bloqueo a nivel kernel encaja de forma natural sin
volver a root.
