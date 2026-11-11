package dteinbox

import (
	"context"
	"errors"
	"time"
)

// ExecuteSyncRequest vincula el resultado real del worker con una solicitud idempotente.
func ExecuteSyncRequest(ctx context.Context, store Store, id string, now func() time.Time, run func(context.Context) error) error {
	job, err := store.SyncRequest(ctx, id, now())
	if err != nil {
		return err
	}
	if job == nil {
		return errors.New("solicitud manual inexistente")
	}
	switch job.Status {
	case "COMPLETED", "RUNNING":
		return nil
	case "FAILED":
		return errors.New("solicitud manual fallida; revisar estado y cola de errores")
	}
	job, err = store.TransitionSyncRequest(ctx, *job, "RUNNING", "Leyendo el correo y procesando los documentos.", now())
	if errors.Is(err, ErrSyncRequestConflict) {
		return nil
	}
	if err != nil {
		return err
	}
	runErr := run(ctx)
	status, detail := "COMPLETED", "Lectura del correo completada. Documentos actualizados."
	if runErr != nil {
		status, detail = "FAILED", "No fue posible completar la lectura; revisa las alertas antes de volver a sincronizar."
		if errors.Is(runErr, ErrSyncBusy) {
			detail = "Ya hay una lectura en curso. Espera a que termine antes de volver a sincronizar."
		}
		if errors.Is(runErr, ErrSyncIncomplete) {
			status, detail = "PENDING", "La lectura continúa desde la siguiente página; esperando reintento."
		}
	}
	_, saveErr := store.TransitionSyncRequest(ctx, *job, status, detail, now())
	if errors.Is(runErr, ErrSyncBusy) {
		return saveErr
	}
	return errors.Join(runErr, saveErr)
}
