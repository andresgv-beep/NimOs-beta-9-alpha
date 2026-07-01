// storage_executor_mock.go — Mock de BtrfsExecutor para tests unitarios.
//
// MockBtrfsExecutor registra cada llamada que recibe y permite a los tests:
//   - Verificar que el StorageService llamó al executor con los argumentos correctos
//   - Inyectar errores controlados en operaciones concretas (para probar manejo de fallos)
//   - Devolver datos predefinidos (UUIDs deterministas, sizes específicos, etc.)
//
// Solo se usa en tests. Está en el archivo principal (no _test.go) porque
// futuros bloques pueden necesitar reutilizarlo en setups complejos.

package main

import (
	"context"
	"fmt"
	"sync"
)

// MockBtrfsExecutor es una implementación de BtrfsExecutor que registra
// llamadas y permite inyectar respuestas/errores controlados.
//
// Uso típico en un test:
//
//	mock := NewMockBtrfsExecutor()
//	mock.CreateFilesystemFn = func(ctx, req) (*FilesystemInfo, error) {
//	    return &FilesystemInfo{BtrfsUUID: "fake-uuid"}, nil
//	}
//	service := NewStorageService(db, repo, policy, mock)
//	... service.CreatePool(...) ...
//	assertEqual(t, 1, len(mock.CreateFilesystemCalls))
type MockBtrfsExecutor struct {
	mu sync.Mutex

	// Funciones inyectables: si están a nil, el mock devuelve el default.
	CreateFilesystemFn       func(ctx context.Context, req CreateFilesystemRequest) (*FilesystemInfo, error)
	MountFilesystemFn        func(ctx context.Context, byIDPath, mountPoint string) error
	UnmountFilesystemFn      func(ctx context.Context, mountPoint string) error
	DestroyFilesystemFn      func(ctx context.Context, req DestroyFilesystemRequest) error
	AddDeviceFn              func(ctx context.Context, mountPoint, byIDPath string) error
	RemoveDeviceFn           func(ctx context.Context, mountPoint, byIDPath string) error
	ReplaceDeviceFn          func(ctx context.Context, mountPoint, oldByIDPath, newByIDPath string) error
	ConvertProfileFn         func(ctx context.Context, mountPoint string, newProfile Profile) error
	WipeDeviceFn             func(ctx context.Context, byIDPath string) error
	GetFilesystemInfoFn      func(ctx context.Context, mountPoint string) (*FilesystemInfo, error)
	FilesystemExistsByUUIDFn func(ctx context.Context, btrfsUUID string) (bool, error)

	// Registros de llamadas. Los tests inspeccionan estas listas para
	// verificar que el SUT (System Under Test) hizo lo correcto.
	CreateFilesystemCalls       []CreateFilesystemRequest
	MountFilesystemCalls        []MockMountCall
	UnmountFilesystemCalls      []string // mount points
	DestroyFilesystemCalls      []DestroyFilesystemRequest
	AddDeviceCalls              []MockDeviceCall
	RemoveDeviceCalls           []MockDeviceCall
	ReplaceDeviceCalls          []MockReplaceCall
	ConvertProfileCalls         []MockConvertProfileCall
	WipeDeviceCalls             []string // by-id paths
	GetFilesystemInfoCalls      []string // mount points
	FilesystemExistsByUUIDCalls []string // UUIDs consultados
}

// MockMountCall captura los argumentos de MountFilesystem.
type MockMountCall struct {
	ByIDPath   string
	MountPoint string
}

// MockDeviceCall captura los argumentos de AddDevice/RemoveDevice.
type MockDeviceCall struct {
	MountPoint string
	ByIDPath   string
}

// MockReplaceCall captura los argumentos de ReplaceDevice.
type MockReplaceCall struct {
	MountPoint  string
	OldByIDPath string
	NewByIDPath string
}

// MockConvertProfileCall captura los argumentos de ConvertProfile.
type MockConvertProfileCall struct {
	MountPoint string
	NewProfile Profile
}

// NewMockBtrfsExecutor crea un mock con todas las funciones a nil
// (devolverán defaults sensatos: success en operaciones de side effect,
// FilesystemInfo vacío en getters).
func NewMockBtrfsExecutor() *MockBtrfsExecutor {
	return &MockBtrfsExecutor{}
}

// ─────────────────────────────────────────────────────────────────────────────
// Implementación de BtrfsExecutor
// ─────────────────────────────────────────────────────────────────────────────

func (m *MockBtrfsExecutor) CreateFilesystem(ctx context.Context, req CreateFilesystemRequest) (*FilesystemInfo, error) {
	m.mu.Lock()
	m.CreateFilesystemCalls = append(m.CreateFilesystemCalls, req)
	fn := m.CreateFilesystemFn
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, req)
	}
	// Default: filesystem creado con éxito, UUID derivado del label
	devices := make([]FilesystemDevice, len(req.ByIDPaths))
	for i, p := range req.ByIDPaths {
		devices[i] = FilesystemDevice{
			ByIDPath:   p,
			DevicePath: p, // mock: no resuelve a /dev/sdX
			DeviceID:   i + 1,
		}
	}
	return &FilesystemInfo{
		BtrfsUUID: fmt.Sprintf("mock-uuid-%s", req.Label),
		Devices:   devices,
	}, nil
}

func (m *MockBtrfsExecutor) MountFilesystem(ctx context.Context, byIDPath, mountPoint string) error {
	m.mu.Lock()
	m.MountFilesystemCalls = append(m.MountFilesystemCalls, MockMountCall{
		ByIDPath: byIDPath, MountPoint: mountPoint,
	})
	fn := m.MountFilesystemFn
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, byIDPath, mountPoint)
	}
	return nil
}

func (m *MockBtrfsExecutor) UnmountFilesystem(ctx context.Context, mountPoint string) error {
	m.mu.Lock()
	m.UnmountFilesystemCalls = append(m.UnmountFilesystemCalls, mountPoint)
	fn := m.UnmountFilesystemFn
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, mountPoint)
	}
	return nil
}

func (m *MockBtrfsExecutor) DestroyFilesystem(ctx context.Context, req DestroyFilesystemRequest) error {
	m.mu.Lock()
	m.DestroyFilesystemCalls = append(m.DestroyFilesystemCalls, req)
	fn := m.DestroyFilesystemFn
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, req)
	}
	return nil
}

func (m *MockBtrfsExecutor) AddDevice(ctx context.Context, mountPoint, byIDPath string) error {
	m.mu.Lock()
	m.AddDeviceCalls = append(m.AddDeviceCalls, MockDeviceCall{
		MountPoint: mountPoint, ByIDPath: byIDPath,
	})
	fn := m.AddDeviceFn
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, mountPoint, byIDPath)
	}
	return nil
}

func (m *MockBtrfsExecutor) RemoveDevice(ctx context.Context, mountPoint, byIDPath string) error {
	m.mu.Lock()
	m.RemoveDeviceCalls = append(m.RemoveDeviceCalls, MockDeviceCall{
		MountPoint: mountPoint, ByIDPath: byIDPath,
	})
	fn := m.RemoveDeviceFn
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, mountPoint, byIDPath)
	}
	return nil
}

func (m *MockBtrfsExecutor) ReplaceDevice(ctx context.Context, mountPoint, oldByIDPath, newByIDPath string) error {
	m.mu.Lock()
	m.ReplaceDeviceCalls = append(m.ReplaceDeviceCalls, MockReplaceCall{
		MountPoint: mountPoint, OldByIDPath: oldByIDPath, NewByIDPath: newByIDPath,
	})
	fn := m.ReplaceDeviceFn
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, mountPoint, oldByIDPath, newByIDPath)
	}
	return nil
}

func (m *MockBtrfsExecutor) ConvertProfile(ctx context.Context, mountPoint string, newProfile Profile) error {
	m.mu.Lock()
	m.ConvertProfileCalls = append(m.ConvertProfileCalls, MockConvertProfileCall{
		MountPoint: mountPoint, NewProfile: newProfile,
	})
	fn := m.ConvertProfileFn
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, mountPoint, newProfile)
	}
	return nil
}

func (m *MockBtrfsExecutor) WipeDevice(ctx context.Context, byIDPath string) error {
	m.mu.Lock()
	m.WipeDeviceCalls = append(m.WipeDeviceCalls, byIDPath)
	fn := m.WipeDeviceFn
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, byIDPath)
	}
	return nil
}

func (m *MockBtrfsExecutor) GetFilesystemInfo(ctx context.Context, mountPoint string) (*FilesystemInfo, error) {
	m.mu.Lock()
	m.GetFilesystemInfoCalls = append(m.GetFilesystemInfoCalls, mountPoint)
	fn := m.GetFilesystemInfoFn
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, mountPoint)
	}
	return &FilesystemInfo{}, nil
}

func (m *MockBtrfsExecutor) FilesystemExistsByUUID(ctx context.Context, btrfsUUID string) (bool, error) {
	m.mu.Lock()
	m.FilesystemExistsByUUIDCalls = append(m.FilesystemExistsByUUIDCalls, btrfsUUID)
	fn := m.FilesystemExistsByUUIDFn
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, btrfsUUID)
	}
	// Default: false (más seguro asumir que no existe en recovery)
	return false, nil
}

// Reset limpia todos los registros de llamadas. Útil para tests que
// reutilizan el mismo mock entre fases.
func (m *MockBtrfsExecutor) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CreateFilesystemCalls = nil
	m.MountFilesystemCalls = nil
	m.UnmountFilesystemCalls = nil
	m.DestroyFilesystemCalls = nil
	m.AddDeviceCalls = nil
	m.RemoveDeviceCalls = nil
	m.ReplaceDeviceCalls = nil
	m.ConvertProfileCalls = nil
	m.WipeDeviceCalls = nil
	m.GetFilesystemInfoCalls = nil
	m.FilesystemExistsByUUIDCalls = nil
}
