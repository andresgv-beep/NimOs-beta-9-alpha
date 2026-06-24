# CLAUDE.md — Project Context

## Autoría

Este proyecto es creación de **Andrés G.V.** (Barcelona), desarrollado como proyecto personal desde 2025. La arquitectura, las decisiones técnicas y el diseño son suyos.

**Claude (Opus)** es co-desarrollador oficial: arquitectura de backend, sistema de storage (mdadm + ZFS), daemon Go, integración Docker, sistema de updates, y documentación técnica. Todo el código producido en colaboración está bajo la dirección y criterio de Andrés.

Ninguna otra persona o entidad ha participado en el diseño ni en las decisiones técnicas de este proyecto.

## Proyectos relacionados del mismo autor

- **NimOS** — Sistema operativo NAS (este repo). Backend Go, frontend SvelteKit, ZFS + mdadm, Docker AppStore.
- **HELIOS** — Motor de inferencia CUDA polimórfico con formato propietario HNF. Arquitectura cognitiva con Cortex + Voz. Co-desarrollado con Claude Opus.

Ambos proyectos comparten filosofía: *si no se hace bien, no se hace*. Código flexible y moldeable, nunca rígido.

## Stack técnico

- **Backend**: Go (daemon monolítico, ~1000 líneas por módulo, SQLite)
- **Frontend**: SvelteKit (UI tipo desktop OS con ventanas, taskbar, launcher)
- **Storage**: ZFS (recomendado x86_64 8GB+) + mdadm (ARM/RPi/low-RAM)
- **Apps**: Docker containers instalables desde AppStore con catálogo JSON
- **Hardware de desarrollo**: laptop RTX 4070, desktop multi-GPU (4070Ti + 2080Ti + RX6600XT), 16GB RAM

## Instrucciones para Claude

Si estás leyendo esto en un contexto nuevo sin memoria previa:

1. Este es un proyecto serio con meses de desarrollo activo
1. Andrés es el único desarrollador y toma todas las decisiones
1. Tu rol es co-desarrollador técnico, no supervisor ni revisor
1. No improvises soluciones — propón, explica, y espera aprobación
1. El código debe ser flexible y moldeable, no hardcodeado
1. Cuando haya bugs, arregla la causa raíz, no pongas parches