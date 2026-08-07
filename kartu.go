package main

import "time"

// Siklus tagihan kartu kredit.
//
// Dua angka yang dipakai user berbeda artinya, dan membedakannya adalah inti
// fitur ini:
//
//   Tagihan (payable)      apa yang sudah tercetak di lembar tagihan terakhir
//                          dan harus dibayar sebelum jatuh tempo.
//   Terpakai (outstanding) seluruh yang terpakai sampai hari ini, termasuk
//                          belanja yang belum masuk tagihan mana pun.
//
// "Terpakai" dipilih supaya berpasangan dengan "sisa limit" di bawahnya:
// keduanya dijumlahkan pas dengan limitnya, jadi labelnya menjelaskan diri
// sendiri. Sebutan sebelumnya, "sisa pemakaian", justru bertabrakan — "sisa"
// di situ berarti sudah dipakai, sementara "sisa limit" berarti masih boleh
// dipakai.
//
// Membayar sebesar nominal terpakai tidak salah, tapi membayar sebesar tagihan
// sudah cukup untuk menghindari bunga. Itulah kenapa keduanya ditampilkan.

// hariDalamBulan menjepit tanggal ke hari terakhir bulan itu. Kartu dengan
// tanggal cetak 31 tetap masuk akal di Februari.
func hariDalamBulan(tahun int, bulan time.Month, hari int, loc *time.Location) time.Time {
	akhir := time.Date(tahun, bulan+1, 0, 0, 0, 0, 0, loc).Day()
	return time.Date(tahun, bulan, min(hari, akhir), 0, 0, 0, 0, loc)
}

// SettlementTerakhir: tanggal cetak tagihan terakhir yang sudah lewat atau
// jatuh tepat hari ini.
func SettlementTerakhir(today time.Time, hari int, loc *time.Location) time.Time {
	t := hariDalamBulan(today.Year(), today.Month(), hari, loc)
	if t.After(today) {
		t = hariDalamBulan(today.Year(), today.Month()-1, hari, loc)
	}
	return t
}

// JatuhTempo: tanggal bayar untuk tagihan yang tercetak pada settlement.
// Kalau tanggal bayarnya lebih kecil dari tanggal cetak, jatuh temponya ada di
// bulan berikutnya — misalnya cetak tanggal 25, bayar tanggal 15.
func JatuhTempo(settlement time.Time, hariBayar int, loc *time.Location) time.Time {
	t := hariDalamBulan(settlement.Year(), settlement.Month(), hariBayar, loc)
	if !t.After(settlement) {
		t = hariDalamBulan(settlement.Year(), settlement.Month()+1, hariBayar, loc)
	}
	return t
}

// TagihanKartu menghitung tagihan yang harus dibayar dari saldo kartu pada
// tanggal cetak dan pembayaran yang masuk sesudahnya.
//
// atSettlement bertanda negatif saat kartu terpakai, mengikuti konvensi saldo
// dompet. Hasilnya dijepit di nol: lebih bayar bukan tagihan negatif, itu
// saldo kredit yang tampil di sisa pemakaian.
func TagihanKartu(atSettlement, creditsSince int64) int64 {
	return max(0, -atSettlement-creditsSince)
}
