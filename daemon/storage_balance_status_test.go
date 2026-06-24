package main

import "testing"

// parseBalanceStatus — casos reales de salida de `btrfs balance status`.

func TestParseBalanceStatus_NoBalance(t *testing.T) {
	st := parseBalanceStatus("No balance found on '/nimos/pools/data8'\n")
	if st.Active || st.Paused {
		t.Errorf("sin balance: got %+v, want inactivo", st)
	}
}

func TestParseBalanceStatus_Running(t *testing.T) {
	out := "Balance on '/nimos/pools/data8' is running\n" +
		"3 out of about 10 chunks balanced (5 considered), 70% left\n"
	st := parseBalanceStatus(out)
	if !st.Active {
		t.Fatal("balance running: Active debería ser true")
	}
	if st.Paused {
		t.Error("running no es paused")
	}
	if st.PercentDone != 30 {
		t.Errorf("PercentDone: got %.1f, want 30 (100-70 left)", st.PercentDone)
	}
}

func TestParseBalanceStatus_Paused(t *testing.T) {
	out := "Balance on '/nimos/pools/data8' is paused\n" +
		"7 out of about 10 chunks balanced (9 considered), 30% left\n"
	st := parseBalanceStatus(out)
	if !st.Active || !st.Paused {
		t.Errorf("paused: got Active=%v Paused=%v, want true/true", st.Active, st.Paused)
	}
	if st.PercentDone != 70 {
		t.Errorf("PercentDone: got %.1f, want 70", st.PercentDone)
	}
}

func TestParseBalanceStatus_GarbageSafe(t *testing.T) {
	// Output corrupto/desconocido → inactivo, sin panic ni estado inventado.
	for _, out := range []string{"", "???", "ERROR: cannot access", "Balance on '/x' is doing something weird"} {
		st := parseBalanceStatus(out)
		if st.Active {
			t.Errorf("output no reconocido %q: no debe inventar Active=true", out)
		}
	}
}
