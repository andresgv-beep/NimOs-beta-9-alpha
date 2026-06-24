#!/usr/bin/env python3
"""
Validador del schema NimOS Beta 8.
Prueba aplicación, CHECK constraints, UNIQUE, FK, ON DELETE behaviors,
invariantes de exclusión mutua (UNIQUE parcial) e idempotencia.
"""

import sqlite3
import sys
import uuid
from datetime import datetime, timezone

SCHEMA_FILE = "/home/claude/schema.sql"
DB_FILE = "/tmp/nimos_test.db"


def now_iso():
    return datetime.now(timezone.utc).isoformat()


def setup_db():
    import os
    if os.path.exists(DB_FILE):
        os.remove(DB_FILE)
    conn = sqlite3.connect(DB_FILE)
    conn.execute("PRAGMA foreign_keys = ON")
    with open(SCHEMA_FILE) as f:
        conn.executescript(f.read())
    conn.commit()
    return conn


def assert_passes(label, fn):
    try:
        fn()
        print(f"  ✓ {label}")
        return True
    except Exception as e:
        print(f"  ✗ {label}\n    Error: {e}")
        return False


def assert_fails(label, fn, expected_keyword=None):
    try:
        fn()
        print(f"  ✗ {label} — debería haber fallado pero pasó")
        return False
    except sqlite3.IntegrityError as e:
        if expected_keyword and expected_keyword.lower() not in str(e).lower():
            print(f"  ⚠ {label} — falló pero con error inesperado: {e}")
            return False
        print(f"  ✓ {label}")
        return True
    except Exception as e:
        print(f"  ⚠ {label} — falló con tipo inesperado {type(e).__name__}: {e}")
        return False


def main():
    print("=" * 70)
    print("NimOS Beta 8 — Schema Validation")
    print("=" * 70)

    print("\n[1] Aplicar schema desde cero")
    try:
        conn = setup_db()
        print("  ✓ Schema aplicado sin errores")
    except Exception as e:
        print(f"  ✗ FALLO al aplicar schema: {e}")
        sys.exit(1)

    fk_status = conn.execute("PRAGMA foreign_keys").fetchone()[0]
    print(f"  ✓ PRAGMA foreign_keys = {fk_status}")

    print("\n[2] Metadata inicial")
    for k, v in conn.execute("SELECT key, value FROM storage_metadata ORDER BY key").fetchall():
        print(f"  ✓ {k} = {v}")

    print("\n[3] Tablas creadas")
    for t in [r[0] for r in conn.execute(
        "SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'storage_%' ORDER BY name"
    ).fetchall()]:
        print(f"  ✓ {t}")

    print("\n[4] Índices creados")
    for idx in [r[0] for r in conn.execute(
        "SELECT name FROM sqlite_master WHERE type='index' AND name LIKE 'idx_%' ORDER BY name"
    ).fetchall()]:
        print(f"  ✓ {idx}")

    # =========================================================================
    print("\n[5] CHECK constraints en storage_pools")
    # =========================================================================

    pool_id = str(uuid.uuid4())
    assert_passes(
        "Pool válido con profile=raid1",
        lambda: conn.execute("""
            INSERT INTO storage_pools (id, name, btrfs_uuid, profile, mount_point, created_at)
            VALUES (?, 'multimedia', ?, 'raid1', '/nimbus/pools/multimedia', ?)
        """, (pool_id, str(uuid.uuid4()), now_iso()))
    )
    conn.commit()

    assert_fails(
        "Profile inválido (raid5) — rechazado",
        lambda: conn.execute("""
            INSERT INTO storage_pools (id, name, btrfs_uuid, profile, mount_point, created_at)
            VALUES (?, 'malo1', ?, 'raid5', '/nimbus/pools/malo1', ?)
        """, (str(uuid.uuid4()), str(uuid.uuid4()), now_iso())),
        "CHECK"
    )

    assert_fails(
        "Role inválido — rechazado",
        lambda: conn.execute("""
            INSERT INTO storage_pools (id, name, btrfs_uuid, profile, mount_point, role, created_at)
            VALUES (?, 'malo2', ?, 'raid1', '/nimbus/pools/malo2', 'wrong', ?)
        """, (str(uuid.uuid4()), str(uuid.uuid4()), now_iso())),
        "CHECK"
    )

    assert_fails(
        "Compression inválida (gzip) — rechazado",
        lambda: conn.execute("""
            INSERT INTO storage_pools (id, name, btrfs_uuid, profile, mount_point, compression, created_at)
            VALUES (?, 'malo3', ?, 'raid1', '/nimbus/pools/malo3', 'gzip', ?)
        """, (str(uuid.uuid4()), str(uuid.uuid4()), now_iso())),
        "CHECK"
    )

    assert_fails(
        "Control state inválido — rechazado",
        lambda: conn.execute("""
            INSERT INTO storage_pools (id, name, btrfs_uuid, profile, mount_point, control_state, created_at)
            VALUES (?, 'malo4', ?, 'raid1', '/nimbus/pools/malo4', 'broken', ?)
        """, (str(uuid.uuid4()), str(uuid.uuid4()), now_iso())),
        "CHECK"
    )

    assert_fails(
        "generation negativa en pools — rechazado (NUEVO)",
        lambda: conn.execute("""
            INSERT INTO storage_pools (id, name, btrfs_uuid, profile, mount_point, created_at, generation)
            VALUES (?, 'malo5', ?, 'raid1', '/nimbus/pools/malo5', ?, -1)
        """, (str(uuid.uuid4()), str(uuid.uuid4()), now_iso())),
        "CHECK"
    )

    assert_fails(
        "Nombre duplicado — rechazado",
        lambda: conn.execute("""
            INSERT INTO storage_pools (id, name, btrfs_uuid, profile, mount_point, created_at)
            VALUES (?, 'multimedia', ?, 'raid1', '/nimbus/pools/multimedia2', ?)
        """, (str(uuid.uuid4()), str(uuid.uuid4()), now_iso())),
        "UNIQUE"
    )

    # =========================================================================
    print("\n[6] storage_devices")
    # =========================================================================

    dev1_id = str(uuid.uuid4())
    assert_passes(
        "Device válido",
        lambda: conn.execute("""
            INSERT INTO storage_devices (id, serial, by_id_path, current_path, size_bytes, last_seen_at)
            VALUES (?, 'WD-WCC4N1234567', '/dev/disk/by-id/ata-WDC_WD40EFRX', '/dev/sdb', 4000000000000, ?)
        """, (dev1_id, now_iso()))
    )
    conn.commit()

    assert_fails(
        "Serial duplicado — rechazado",
        lambda: conn.execute("""
            INSERT INTO storage_devices (id, serial, by_id_path, current_path, last_seen_at)
            VALUES (?, 'WD-WCC4N1234567', '/dev/disk/by-id/otro', '/dev/sdc', ?)
        """, (str(uuid.uuid4()), now_iso())),
        "UNIQUE"
    )

    assert_fails(
        "by_id_path duplicado — rechazado",
        lambda: conn.execute("""
            INSERT INTO storage_devices (id, serial, by_id_path, current_path, last_seen_at)
            VALUES (?, 'otro-serial', '/dev/disk/by-id/ata-WDC_WD40EFRX', '/dev/sdd', ?)
        """, (str(uuid.uuid4()), now_iso())),
        "UNIQUE"
    )

    assert_fails(
        "generation negativa en devices — rechazado (NUEVO)",
        lambda: conn.execute("""
            INSERT INTO storage_devices (id, serial, by_id_path, current_path, last_seen_at, generation)
            VALUES (?, 'serial-neg', '/dev/disk/by-id/test-neg', '/dev/sdz', ?, -5)
        """, (str(uuid.uuid4()), now_iso())),
        "CHECK"
    )

    # =========================================================================
    print("\n[7] FOREIGN KEY enforcement")
    # =========================================================================

    assert_fails(
        "Pool-device con pool_id inexistente — rechazado",
        lambda: conn.execute("""
            INSERT INTO storage_pool_devices (pool_id, device_id, added_at)
            VALUES ('pool-no-existe', ?, ?)
        """, (dev1_id, now_iso())),
        "FOREIGN"
    )

    assert_fails(
        "Pool-device con device_id inexistente — rechazado",
        lambda: conn.execute("""
            INSERT INTO storage_pool_devices (pool_id, device_id, added_at)
            VALUES (?, 'dev-no-existe', ?)
        """, (pool_id, now_iso())),
        "FOREIGN"
    )

    assert_passes(
        "Pool-device con FKs válidas",
        lambda: conn.execute("""
            INSERT INTO storage_pool_devices (pool_id, device_id, added_at)
            VALUES (?, ?, ?)
        """, (pool_id, dev1_id, now_iso()))
    )
    conn.commit()

    # =========================================================================
    print("\n[8] INV-1: una sola layout-op activa por pool (NUEVO)")
    # =========================================================================

    op1_id = str(uuid.uuid4())
    assert_passes(
        "Primera layout-op (add_device) in_progress",
        lambda: conn.execute("""
            INSERT INTO storage_operations (id, type, pool_id, status, started_at)
            VALUES (?, 'add_device', ?, 'in_progress', ?)
        """, (op1_id, pool_id, now_iso()))
    )
    conn.commit()

    assert_fails(
        "Segunda layout-op (replace_device) mismo pool — rechazada",
        lambda: conn.execute("""
            INSERT INTO storage_operations (id, type, pool_id, status, started_at)
            VALUES (?, 'replace_device', ?, 'pending', ?)
        """, (str(uuid.uuid4()), pool_id, now_iso())),
        "UNIQUE"
    )

    assert_fails(
        "Tercera layout-op (convert_profile) mismo pool — rechazada",
        lambda: conn.execute("""
            INSERT INTO storage_operations (id, type, pool_id, status, started_at)
            VALUES (?, 'convert_profile', ?, 'in_progress', ?)
        """, (str(uuid.uuid4()), pool_id, now_iso())),
        "UNIQUE"
    )

    assert_passes(
        "create_snapshot durante layout-op — permitido (no es layout)",
        lambda: conn.execute("""
            INSERT INTO storage_operations (id, type, pool_id, status, started_at)
            VALUES (?, 'create_snapshot', ?, 'in_progress', ?)
        """, (str(uuid.uuid4()), pool_id, now_iso()))
    )
    conn.commit()

    # Completar la primera, otra puede empezar
    conn.execute("UPDATE storage_operations SET status = 'completed', completed_at = ? WHERE id = ?",
                 (now_iso(), op1_id))
    conn.commit()

    assert_passes(
        "Tras completar la primera, nueva layout-op — permitida",
        lambda: conn.execute("""
            INSERT INTO storage_operations (id, type, pool_id, status, started_at)
            VALUES (?, 'add_device', ?, 'in_progress', ?)
        """, (str(uuid.uuid4()), pool_id, now_iso()))
    )
    conn.commit()

    # =========================================================================
    print("\n[9] INV-2: un solo scrub activo por pool (NUEVO)")
    # =========================================================================

    scrub1 = str(uuid.uuid4())
    assert_passes(
        "Primer scrub in_progress",
        lambda: conn.execute("""
            INSERT INTO storage_operations (id, type, pool_id, status, started_at)
            VALUES (?, 'start_scrub', ?, 'in_progress', ?)
        """, (scrub1, pool_id, now_iso()))
    )
    conn.commit()

    assert_fails(
        "Segundo scrub mismo pool — rechazado",
        lambda: conn.execute("""
            INSERT INTO storage_operations (id, type, pool_id, status, started_at)
            VALUES (?, 'start_scrub', ?, 'pending', ?)
        """, (str(uuid.uuid4()), pool_id, now_iso())),
        "UNIQUE"
    )

    # Scrub en otro pool sí permitido
    pool2_id = str(uuid.uuid4())
    conn.execute("""
        INSERT INTO storage_pools (id, name, btrfs_uuid, profile, mount_point, created_at)
        VALUES (?, 'backups', ?, 'raid1', '/nimbus/pools/backups', ?)
    """, (pool2_id, str(uuid.uuid4()), now_iso()))
    conn.commit()

    assert_passes(
        "Scrub en OTRO pool simultáneo — permitido",
        lambda: conn.execute("""
            INSERT INTO storage_operations (id, type, pool_id, status, started_at)
            VALUES (?, 'start_scrub', ?, 'in_progress', ?)
        """, (str(uuid.uuid4()), pool2_id, now_iso()))
    )
    conn.commit()

    # =========================================================================
    print("\n[10] ON DELETE behaviors")
    # =========================================================================

    assert_fails(
        "Borrar device en uso — RESTRICT",
        lambda: conn.execute("DELETE FROM storage_devices WHERE id = ?", (dev1_id,)),
        "FOREIGN"
    )

    # Evento de la op
    conn.execute("""
        INSERT INTO storage_events (id, operation_id, timestamp, level, message)
        VALUES (?, ?, ?, 'info', 'Operation started')
    """, (str(uuid.uuid4()), op1_id, now_iso()))

    # Capability
    conn.execute("INSERT INTO storage_pool_capabilities (pool_id, capability) VALUES (?, 'snapshots')",
                 (pool_id,))
    conn.commit()

    print("\n[11] CASCADE / SET NULL al borrar pool")

    conn.execute("DELETE FROM storage_pools WHERE id = ?", (pool_id,))
    conn.commit()

    cnt = conn.execute("SELECT COUNT(*) FROM storage_pool_devices WHERE pool_id = ?", (pool_id,)).fetchone()[0]
    print(f"  {'✓' if cnt == 0 else '✗'} pool_devices borrados (CASCADE), count={cnt}")

    cnt = conn.execute("SELECT COUNT(*) FROM storage_pool_capabilities WHERE pool_id = ?", (pool_id,)).fetchone()[0]
    print(f"  {'✓' if cnt == 0 else '✗'} pool_capabilities borrados (CASCADE), count={cnt}")

    row = conn.execute("SELECT pool_id FROM storage_operations WHERE id = ?", (op1_id,)).fetchone()
    print(f"  {'✓' if row and row[0] is None else '✗'} operación con pool_id=NULL (SET NULL)")

    cnt = conn.execute("SELECT COUNT(*) FROM storage_devices WHERE id = ?", (dev1_id,)).fetchone()[0]
    print(f"  {'✓' if cnt == 1 else '✗'} device conservado tras destruir pool, count={cnt}")

    print("\n[12] CASCADE al borrar operation")

    cnt_before = conn.execute("SELECT COUNT(*) FROM storage_events WHERE operation_id = ?", (op1_id,)).fetchone()[0]
    conn.execute("DELETE FROM storage_operations WHERE id = ?", (op1_id,))
    conn.commit()
    cnt_after = conn.execute("SELECT COUNT(*) FROM storage_events WHERE operation_id = ?", (op1_id,)).fetchone()[0]
    print(f"  {'✓' if cnt_before > 0 and cnt_after == 0 else '✗'} events borrados por CASCADE (antes={cnt_before}, después={cnt_after})")

    # =========================================================================
    print("\n[13] Idempotencia")
    # =========================================================================
    try:
        with open(SCHEMA_FILE) as f:
            conn.executescript(f.read())
        conn.commit()
        print("  ✓ Schema re-aplicado sin errores")
    except Exception as e:
        print(f"  ✗ FALLO en re-aplicación: {e}")

    print("\n" + "=" * 70)
    print("Schema validation COMPLETED")
    print("=" * 70)
    conn.close()


if __name__ == "__main__":
    main()
