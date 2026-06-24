package main

import "testing"

const (
	gib = int64(1) << 30
)

// computeUsableCapacity: capacidad usable estable por geometría de discos.
// El caso estrella es el de Andrés: RAID1 con discos asimétricos (120+320).

func TestUsableCapacity_Raid1_Asymmetric(t *testing.T) {
	// 120GB + 320GB en raid1 → usable = min(suma/2, suma-mayor)
	// suma=440, mayor=320 → min(220, 120) = 120 (limitado por el disco pequeño)
	sizes := []int64{120 * gib, 320 * gib}
	got := computeUsableCapacity(ProfileRaid1, sizes)
	want := 120 * gib
	if got != want {
		t.Errorf("raid1 asimétrico 120+320: got %d GiB, want %d GiB",
			got/gib, want/gib)
	}
}

func TestUsableCapacity_Raid1_Symmetric(t *testing.T) {
	// 2x500 en raid1 → min(1000/2, 1000-500) = min(500,500) = 500
	sizes := []int64{500 * gib, 500 * gib}
	got := computeUsableCapacity(ProfileRaid1, sizes)
	want := 500 * gib
	if got != want {
		t.Errorf("raid1 simétrico 2x500: got %d, want %d", got/gib, want/gib)
	}
}

func TestUsableCapacity_Single(t *testing.T) {
	// single → suma total (sin redundancia)
	sizes := []int64{120 * gib, 320 * gib}
	got := computeUsableCapacity(ProfileSingle, sizes)
	want := 440 * gib
	if got != want {
		t.Errorf("single: got %d, want %d", got/gib, want/gib)
	}
}

func TestUsableCapacity_Raid10_Balanced(t *testing.T) {
	// 4x500 en raid10 → suma/2 = 1000 (4*500=2000, /2)
	sizes := []int64{500 * gib, 500 * gib, 500 * gib, 500 * gib}
	got := computeUsableCapacity(ProfileRaid10, sizes)
	want := 1000 * gib
	if got != want {
		t.Errorf("raid10 4x500: got %d, want %d", got/gib, want/gib)
	}
}

func TestUsableCapacity_Raid1c3(t *testing.T) {
	// 3x300 en raid1c3 (3 copias) → suma/3 = 300
	sizes := []int64{300 * gib, 300 * gib, 300 * gib}
	got := computeUsableCapacity(ProfileRaid1c3, sizes)
	want := 300 * gib
	if got != want {
		t.Errorf("raid1c3 3x300: got %d, want %d", got/gib, want/gib)
	}
}

func TestUsableCapacity_Empty(t *testing.T) {
	if got := computeUsableCapacity(ProfileRaid1, nil); got != 0 {
		t.Errorf("sin discos: got %d, want 0", got)
	}
}
