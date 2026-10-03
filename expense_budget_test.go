package main

import "testing"

func TestExpenseBudgetBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name             string
		limit, projected int64
		status, message  string
	}{
		{"unallocated", 0, 50000000, "NO_BUDGET", "Kategori belum dialokasikan."},
		{"below 80", 100000000, 79999999, "SAFE", "Sesuai pos anggaran • Sisa kuota: Rp200.000,01"},
		{"exactly 80", 100000000, 80000000, "WARNING", "Mendekati limit • Sisa: Rp200.000"},
		{"below 100", 100000000, 99999999, "WARNING", "Mendekati limit • Sisa: Rp0,01"},
		{"exactly 100", 100000000, 100000000, "OVERBUDGET", "Melebihi anggaran Belanja sebesar Rp0"},
		{"above 100", 100000000, 110000000, "OVERBUDGET", "Melebihi anggaran Belanja sebesar Rp100.000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluateExpenseBudget("Belanja", tc.limit, tc.projected, "IDR")
			if got.Status != tc.status || got.Message != tc.message {
				t.Fatalf("got %+v; want %s / %s", got, tc.status, tc.message)
			}
		})
	}
}
