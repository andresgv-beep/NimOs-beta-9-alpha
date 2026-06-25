package main

import "testing"

// La Norma 1 de Docker: su data-root manda. Si el pool no está montado, Docker
// se detiene (no escribe en el disco de sistema). Cuando vuelve, se rearranca.

func withDockerGuardStubs(conf map[string]interface{}, mounted func(string) bool, fn func()) {
	origConf := getDockerConfigGo
	origMounted := isPoolMounted
	getDockerConfigGo = func() map[string]interface{} { return conf }
	isPoolMounted = mounted
	defer func() {
		getDockerConfigGo = origConf
		isPoolMounted = origMounted
	}()
	fn()
}

func TestCheckDockerDataRoot_PoolNotMounted(t *testing.T) {
	// EL CASO REAL: data-root en data8, pero data8 NO montado → NO seguro.
	conf := map[string]interface{}{"data-root": "/nimos/pools/data8/docker/data"}
	withDockerGuardStubs(conf, func(string) bool { return false }, func() {
		st := checkDockerDataRoot()
		if st.Safe {
			t.Error("pool no montado debe ser NO seguro")
		}
		if st.Code != "pool_not_mounted" {
			t.Errorf("código esperado pool_not_mounted, got %s", st.Code)
		}
		if st.PoolMount != "/nimos/pools/data8" {
			t.Errorf("mountpoint mal derivado: %s", st.PoolMount)
		}
	})
}

func TestCheckDockerDataRoot_PoolMounted(t *testing.T) {
	conf := map[string]interface{}{"data-root": "/nimos/pools/data8/docker/data"}
	withDockerGuardStubs(conf, func(string) bool { return true }, func() {
		st := checkDockerDataRoot()
		if !st.Safe {
			t.Error("pool montado debe ser seguro")
		}
		if st.Code != "ok" {
			t.Errorf("código esperado ok, got %s", st.Code)
		}
	})
}

func TestCheckDockerDataRoot_NoConfig(t *testing.T) {
	// Sin data-root → Docker usa su default; no es asunto de NimOS.
	withDockerGuardStubs(map[string]interface{}{}, func(string) bool { return false }, func() {
		st := checkDockerDataRoot()
		if !st.Safe {
			t.Error("sin data-root en pool → no bloqueamos (Safe=true)")
		}
		if st.Code != "no_config" {
			t.Errorf("código esperado no_config, got %s", st.Code)
		}
	})
}

func TestCheckDockerDataRoot_NotInNimosPool(t *testing.T) {
	// data-root fuera de /nimos/pools → no opinamos.
	conf := map[string]interface{}{"data-root": "/var/lib/docker"}
	withDockerGuardStubs(conf, func(string) bool { return false }, func() {
		st := checkDockerDataRoot()
		if !st.Safe {
			t.Error("data-root fuera de pools → no bloqueamos")
		}
		if st.Code != "not_in_pool" {
			t.Errorf("código esperado not_in_pool, got %s", st.Code)
		}
	})
}

func TestDockerDataRootToPoolMount(t *testing.T) {
	cases := map[string]string{
		"/nimos/pools/data8/docker/data": "/nimos/pools/data8",
		"/nimos/pools/data10/docker":     "/nimos/pools/data10",
		"/var/lib/docker":                "",
		"":                               "",
	}
	for in, want := range cases {
		if got := dockerDataRootToPoolMount(in); got != want {
			t.Errorf("dockerDataRootToPoolMount(%q) = %q, want %q", in, got, want)
		}
	}
}
