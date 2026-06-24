// docker_network_pool_test.go — tests de la lógica pura del merge de daemon.json.

package main

import (
	"encoding/json"
	"testing"
)

func TestMergeDockerAddressPoolJSON_Empty(t *testing.T) {
	out, changed, err := mergeDockerAddressPoolJSON([]byte(""))
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if !changed {
		t.Fatal("fichero vacío debería añadir el pool (changed=true)")
	}
	conf := map[string]interface{}{}
	if e := json.Unmarshal(out, &conf); e != nil {
		t.Fatalf("salida no es JSON válido: %v", e)
	}
	if _, ok := conf["default-address-pools"]; !ok {
		t.Error("falta default-address-pools en la salida")
	}
}

func TestMergeDockerAddressPoolJSON_PreservaDataRoot(t *testing.T) {
	in := []byte(`{"data-root":"/nimos/pools/main/docker/data"}`)
	out, changed, err := mergeDockerAddressPoolJSON(in)
	if err != nil || !changed {
		t.Fatalf("debería añadir el pool · changed=%v err=%v", changed, err)
	}
	conf := map[string]interface{}{}
	json.Unmarshal(out, &conf)
	if conf["data-root"] != "/nimos/pools/main/docker/data" {
		t.Errorf("data-root no preservado: %v", conf["data-root"])
	}
	if _, ok := conf["default-address-pools"]; !ok {
		t.Error("no se añadió default-address-pools")
	}
}

func TestMergeDockerAddressPoolJSON_Idempotente(t *testing.T) {
	// Ya tiene un pool (del usuario o nuestro) → no se toca.
	in := []byte(`{"data-root":"/x","default-address-pools":[{"base":"10.0.0.0/8","size":24}]}`)
	out, changed, err := mergeDockerAddressPoolJSON(in)
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if changed {
		t.Error("no debería tocar un daemon.json que ya tiene default-address-pools")
	}
	if string(out) != string(in) {
		t.Error("la salida debería ser idéntica a la entrada cuando no cambia")
	}
}

func TestMergeDockerAddressPoolJSON_JSONInvalido(t *testing.T) {
	// LÍNEA ROJA: JSON inválido → error, y el llamador NO debe escribir.
	in := []byte(`{ esto no es json `)
	_, changed, err := mergeDockerAddressPoolJSON(in)
	if err == nil {
		t.Fatal("JSON inválido debería devolver error (para no sobrescribir)")
	}
	if changed {
		t.Error("no debería marcar changed con JSON inválido")
	}
}
