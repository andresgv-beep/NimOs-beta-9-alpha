// network_ddns_reconciler.go — Reconciler que mantiene los DDNS actualizados.
//
// Cómo funciona una pasada (Reconcile):
//
//   1. Listar todas las filas network_ddns con enabled=true.
//   2. Para cada una, decidir si toca actualizar:
//      a) Si applied < desired: la config cambió, hay que aplicar.
//      b) Si auto_update=true y han pasado >= update_interval desde
//         last_run_at: toca refresh periódico.
//      c) Si auto_update=true y last_run_at IS NULL: nunca se ha
//         ejecutado, toca primera ejecución.
//      Si ninguna se cumple → skip.
//   3. Para cada DDNS que toca:
//      a) Resolver el provider concreto por nombre (map registrado).
//      b) Descifrar el token de nimos_secrets.
//      c) Crear network_operations con triggered_by=reconciler:ddns_updater.
//      d) Llamar provider.Update.
//      e) Persistir resultado: RecordDdnsRun + MarkDdnsApplied si aplica.
//      f) Emitir evento (niveles según DISCIPLINE §4).
//      g) Cerrar la network_operations con status apropiado.
//
// Tier=Medium, interval configurable (default 60s — chequea con frecuencia
// si toca actualizar, pero cada DDNS solo se llama según SU update_interval).
//
// Decisiones explícitas:
//
//   - NO detectamos IP pública previa. DuckDNS hace su update con la IP
//     del cliente cada vez. Si quieres detectar drift de IP entre runs,
//     se añade como capability futura.
//   - NO bloqueamos boot si el provider está open. Si DuckDNS está caído
//     al arrancar, el breaker se queda open y el reconciler emite warns
//     hasta que cierre.
//   - NO usamos breaker del propio reconciler — el breaker vive en el
//     provider. Si el breaker abre, Update devuelve ErrDDNSTransient.

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Tipos
// ─────────────────────────────────────────────────────────────────────────────

// DDNSReconcilerConfig agrupa parámetros del constructor.
type DDNSReconcilerConfig struct {
	// Interval del Reconciler (cuán a menudo se ejecuta su Reconcile).
	// NOTA: este NO es el intervalo de update de cada DDNS — cada fila
	// network_ddns tiene su propio update_interval. Este es la cadencia
	// con la que MIRAMOS si toca actualizar. Default 60s.
	Interval time.Duration
}

// DefaultDDNSReconcilerConfig devuelve los valores por defecto.
func DefaultDDNSReconcilerConfig() DDNSReconcilerConfig {
	return DDNSReconcilerConfig{
		Interval: 60 * time.Second,
	}
}

// DDNSReconciler implementa Reconciler.
type DDNSReconciler struct {
	repo    *NetworkRepo
	secrets *SecretsStore
	emitter *EventEmitter
	clock   Clock
	config  DDNSReconcilerConfig

	mu        sync.RWMutex
	providers map[string]DDNSProvider
}

// NewDDNSReconciler construye el reconciler. Todos los args excepto
// config son obligatorios. Si config.Interval=0, se aplica default.
//
// El reconciler arranca sin providers registrados — el caller debe
// llamar RegisterProvider tantas veces como providers concretos haya.
func NewDDNSReconciler(repo *NetworkRepo, secrets *SecretsStore, emitter *EventEmitter, clock Clock, config DDNSReconcilerConfig) (*DDNSReconciler, error) {
	if repo == nil {
		return nil, errors.New("NewDDNSReconciler: repo is nil")
	}
	if secrets == nil {
		return nil, errors.New("NewDDNSReconciler: secrets is nil")
	}
	if emitter == nil {
		return nil, errors.New("NewDDNSReconciler: emitter is nil")
	}
	if clock == nil {
		clock = NewRealClock()
	}
	if config.Interval == 0 {
		config.Interval = DefaultDDNSReconcilerConfig().Interval
	}
	return &DDNSReconciler{
		repo:      repo,
		secrets:   secrets,
		emitter:   emitter,
		clock:     clock,
		config:    config,
		providers: make(map[string]DDNSProvider),
	}, nil
}

// RegisterProvider añade un implementador concreto. Sobrescribe si el
// nombre ya existe (útil para tests).
func (r *DDNSReconciler) RegisterProvider(p DDNSProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
}

// ─────────────────────────────────────────────────────────────────────────────
// Reconciler interface impl
// ─────────────────────────────────────────────────────────────────────────────

func (r *DDNSReconciler) Name() string             { return "ddns_updater" }
func (r *DDNSReconciler) Tier() ReconcilerTier     { return TierMedium }
func (r *DDNSReconciler) Interval() time.Duration  { return r.config.Interval }

// ForceUpdate dispara una actualización inmediata de un DDNS concreto por ID,
// fuera del ciclo periódico. Lo usa el endpoint "Actualizar ahora".
// Reutiliza processOne (misma lógica de operation auditable, token, provider).
func (r *DDNSReconciler) ForceUpdate(ctx context.Context, id string) error {
	if r.repo == nil {
		return fmt.Errorf("ddns reconciler: no repo")
	}
	d, err := r.repo.GetDdns(ctx, id)
	if err != nil {
		return err
	}
	if !d.Enabled {
		return fmt.Errorf("ddns is disabled")
	}
	r.processOne(ctx, d)
	return nil
}

// Reconcile ejecuta una pasada completa.
func (r *DDNSReconciler) Reconcile(ctx context.Context) error {
	all, err := r.repo.ListDdns(ctx)
	if err != nil {
		return fmt.Errorf("list ddns: %w", err)
	}

	for _, d := range all {
		if !d.Enabled {
			continue
		}
		if !r.needsUpdate(d) {
			continue
		}
		// Contexto cancelado entre iteraciones — salir limpiamente.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.processOne(ctx, d)
	}
	return nil
}

// needsUpdate decide si una fila DDNS necesita procesarse en esta pasada.
func (r *DDNSReconciler) needsUpdate(d *NetworkDdns) bool {
	// Pending (cambio de config sin aplicar).
	if d.Convergence.IsPending() {
		return true
	}
	// Sin auto_update y ya aplicado: no tocar (one-shot).
	if !d.AutoUpdate {
		return false
	}
	// Auto-update sin run previo: ejecutar.
	if d.LastRunAt == nil {
		return true
	}
	// Auto-update con interval pasado.
	elapsed := r.clock.Now().Sub(*d.LastRunAt)
	return elapsed >= time.Duration(d.UpdateInterval)*time.Second
}

// processOne ejecuta el ciclo completo para un DDNS:
//   - Resolver provider.
//   - Descifrar token.
//   - Abrir network_operations.
//   - Llamar Update.
//   - Persistir resultado.
//   - Emitir eventos.
//   - Cerrar operation.
//
// Errores se loguean y emiten como eventos, NO se propagan — un DDNS
// fallido no impide que el reconciler procese los siguientes.
func (r *DDNSReconciler) processOne(ctx context.Context, d *NetworkDdns) {
	// Resolver provider.
	r.mu.RLock()
	provider, found := r.providers[d.Provider]
	r.mu.RUnlock()
	if !found {
		r.emitEvent(ctx, EventLevelWarn, d.ID, "provider_unknown",
			fmt.Sprintf("DDNS provider %q not registered for %s", d.Provider, d.Domain))
		return
	}

	// Crear operation auditable.
	opID, err := r.openOperation(ctx, d)
	if err != nil {
		logMsg("ddns reconciler: open operation for %s: %v", d.Domain, err)
		return
	}

	// Descifrar token.
	secret, err := r.secrets.GetSecret(SecretID(d.TokenSecretID))
	if err != nil {
		_ = r.closeOperation(ctx, opID, "failed", "SECRET_FETCH", err.Error())
		r.emitEventOp(ctx, opID, EventLevelError, d.ID, "secret_fetch_failed",
			fmt.Sprintf("Could not load token for %s: %v", d.Domain, err))
		return
	}
	tokenBytes := secret.Plaintext
	token := string(tokenBytes)

	// Llamar provider.
	result, callErr := provider.Update(ctx, d.Domain, token)
	// Wipe el token de memoria (best-effort; Go no garantiza zeroing
	// pero al menos no lo dejamos en una variable viva más).
	token = ""
	for i := range tokenBytes {
		tokenBytes[i] = 0
	}

	// Clasificar resultado.
	switch {
	case callErr == nil:
		r.handleSuccess(ctx, d, opID, result)
	case errors.Is(callErr, ErrDDNSAuthFailed):
		r.handleAuthFailed(ctx, d, opID, callErr)
	case errors.Is(callErr, ErrDDNSTransient):
		r.handleTransient(ctx, d, opID, callErr)
	default:
		r.handleOtherError(ctx, d, opID, callErr)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Persistencia y eventos
// ─────────────────────────────────────────────────────────────────────────────

func (r *DDNSReconciler) handleSuccess(ctx context.Context, d *NetworkDdns, opID string, result *DDNSUpdateResult) {
	// Determinar si la IP cambió en este run comparando con last_ip
	// almacenado. DuckDNS responde "OK" siempre, no nos dice si cambió,
	// así que la mejor señal es: "tenemos last_ip almacenada y es != ''
	// y NewIP está vacío" → no podemos saber. Para DuckDNS dejamos
	// last_ip como vacío (no la sobrescribimos). Otros providers que
	// devuelvan IP la guardarán.
	var newIPPtr *string
	if result != nil && result.NewIP != "" {
		ip := result.NewIP
		newIPPtr = &ip
	}

	// Persistir Run + MarkApplied.
	persistErr := r.withTx(ctx, func(tx *sql.Tx) error {
		if err := r.repo.RecordDdnsRun(ctx, tx, d.ID, "success", newIPPtr); err != nil {
			return err
		}
		if err := r.repo.MarkDdnsApplied(ctx, tx, d.ID); err != nil {
			return err
		}
		return nil
	})
	if persistErr != nil {
		_ = r.closeOperation(ctx, opID, "failed", "PERSIST", persistErr.Error())
		r.emitEventOp(ctx, opID, EventLevelError, d.ID, "persist_failed",
			fmt.Sprintf("Update for %s succeeded but persisting failed: %v", d.Domain, persistErr))
		return
	}

	// Evento de éxito.
	// DISCIPLINE §4: "update succeeded" rutinario → debug, no info.
	// Pero si esta es la primera vez que se ejecuta o si IP cambió → info.
	level := EventLevelDebug
	event := "update_succeeded"
	msg := fmt.Sprintf("DDNS update for %s succeeded", d.Domain)
	if d.LastRunAt == nil {
		level = EventLevelInfo
		event = "first_update_succeeded"
		msg = fmt.Sprintf("DDNS first update for %s succeeded", d.Domain)
	} else if newIPPtr != nil && d.LastIP != nil && *newIPPtr != *d.LastIP {
		level = EventLevelInfo
		event = "ip_changed"
		msg = fmt.Sprintf("DDNS IP changed for %s: %s → %s", d.Domain, *d.LastIP, *newIPPtr)
	}
	_ = r.closeOperation(ctx, opID, "completed", "", "")
	r.emitEventOp(ctx, opID, level, d.ID, event, msg)
}

func (r *DDNSReconciler) handleAuthFailed(ctx context.Context, d *NetworkDdns, opID string, err error) {
	_ = r.withTx(ctx, func(tx *sql.Tx) error {
		return r.repo.RecordDdnsRun(ctx, tx, d.ID, "failed", nil)
	})
	_ = r.closeOperation(ctx, opID, "failed", "AUTH", err.Error())
	r.emitEventOp(ctx, opID, EventLevelError, d.ID, "auth_failed",
		fmt.Sprintf("DDNS provider rejected credentials for %s", d.Domain))
}

func (r *DDNSReconciler) handleTransient(ctx context.Context, d *NetworkDdns, opID string, err error) {
	_ = r.withTx(ctx, func(tx *sql.Tx) error {
		return r.repo.RecordDdnsRun(ctx, tx, d.ID, "failed", nil)
	})
	_ = r.closeOperation(ctx, opID, "failed", "TRANSIENT", err.Error())
	// Transient → warn (el provider puede recuperarse).
	r.emitEventOp(ctx, opID, EventLevelWarn, d.ID, "transient_failure",
		fmt.Sprintf("DDNS update for %s failed (transient)", d.Domain))
}

func (r *DDNSReconciler) handleOtherError(ctx context.Context, d *NetworkDdns, opID string, err error) {
	_ = r.withTx(ctx, func(tx *sql.Tx) error {
		return r.repo.RecordDdnsRun(ctx, tx, d.ID, "failed", nil)
	})
	_ = r.closeOperation(ctx, opID, "failed", "UNKNOWN", err.Error())
	r.emitEventOp(ctx, opID, EventLevelError, d.ID, "update_failed",
		fmt.Sprintf("DDNS update for %s failed: %v", d.Domain, err))
}

// ─────────────────────────────────────────────────────────────────────────────
// Operations auditables
// ─────────────────────────────────────────────────────────────────────────────

func (r *DDNSReconciler) openOperation(ctx context.Context, d *NetworkDdns) (string, error) {
	op := &NetworkOperation{
		Type:        "ddns_update",
		TargetID:    &d.ID,
		Status:      "in_progress",
		TriggeredBy: "reconciler:ddns_updater",
	}
	err := r.withTx(ctx, func(tx *sql.Tx) error {
		return r.repo.CreateOperation(ctx, tx, op)
	})
	if err != nil {
		return "", err
	}
	return op.ID, nil
}

func (r *DDNSReconciler) closeOperation(ctx context.Context, opID, status, errCode, errMsg string) error {
	var codePtr, msgPtr *string
	if errCode != "" {
		codePtr = &errCode
	}
	if errMsg != "" {
		msgPtr = &errMsg
	}
	return r.withTx(ctx, func(tx *sql.Tx) error {
		return r.repo.UpdateOperationStatus(ctx, tx, opID, status, codePtr, msgPtr)
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Events helper
// ─────────────────────────────────────────────────────────────────────────────

func (r *DDNSReconciler) emitEvent(ctx context.Context, level EventLevel, targetID, event, message string) {
	r.emitEventOp(ctx, "", level, targetID, event, message)
}

func (r *DDNSReconciler) emitEventOp(ctx context.Context, opID string, level EventLevel, targetID, event, message string) {
	in := EventInput{
		Category: CategoryDdns,
		Event:    event,
		TargetID: &targetID,
		Level:    level,
		Message:  message,
	}
	if opID != "" {
		in.OperationID = &opID
	}
	if _, err := r.emitter.Emit(ctx, in); err != nil {
		logMsg("ddns reconciler: emit event %s: %v", event, err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tx helper
// ─────────────────────────────────────────────────────────────────────────────

// withTx ejecuta fn dentro de una transacción sobre la DB del repo.
// Hace commit si fn devuelve nil; rollback si error.
func (r *DDNSReconciler) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := r.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
