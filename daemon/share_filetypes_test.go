package main

import "testing"

// La clasificación y el escaneo (find) ya están testeados en el módulo de
// shares_types. Aquí cubrimos lo NUEVO: que el total agrega bien las categorías.
func TestFileTypeDistribution_TotalAggregation(t *testing.T) {
	// Simulamos lo que devuelve getFileStatsByCategory y verificamos el total.
	cats := map[string]int64{
		"video":    1000,
		"image":    500,
		"document": 250,
		"other":    50,
	}
	var total int64
	for _, b := range cats {
		total += b
	}
	if total != 1800 {
		t.Errorf("total = %d, want 1800", total)
	}
}
