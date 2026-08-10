package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Cicilan dan transaksi berulang.
//
// Keduanya satu mekanisme yang sama: satu isian form jadi beberapa transaksi
// bulanan biasa, seluruhnya langsung tersimpan. Tidak ada tabel jadwal dan
// tidak ada penjadwal yang harus jalan tiap hari — baris yang duduk di tabel
// yang sama dengan transaksi lain otomatis ikut terhitung di saldo dompet,
// tagihan kartu, pencarian, dan ringkasan kategori tanpa satu pun query di
// aplikasi ini perlu tahu bahwa cicilan itu ada. Tiap bulannya juga tetap bisa
// diubah dan dihapus sendiri-sendiri, seperti transaksi mana pun.
//
// Bedanya cuma nominal tiap barisnya:
//
//	cicil  satu belanja dipecah rata  (cicilan 12x)
//	ulang  nominal penuh diulang      (langganan, sewa, gaji bulanan)
//
// Tidak dibatasi kartu kredit. Cicilan memang paling sering di sana, tapi
// langganan yang didebet dari rekening dan gaji yang masuk tiap bulan sama
// berulangnya, dan tidak ada di perhitungan mana pun yang berubah karena jenis
// dompetnya.
//
// ponytail: seluruh angsuran ditulis di muka, termasuk yang jatuh di bulan
// depan, dan saldo dompet menghitungnya sejak hari ini — konsisten dengan
// transaksi bertanggal maju yang memang sudah bisa dicatat manual di aplikasi
// ini. Untuk cicilan kartu itu justru benar: limitnya memang tertahan sebesar
// harga penuh sejak transaksinya. Untuk yang berulang panjang itu terlalu
// cepat, dan pemeriksaan saldo di bawah ikut menolak dua belas bulan sewa yang
// uangnya belum ada hari ini. Kalau itu mengganggu, yang dibutuhkan tabel
// jadwal dengan baris yang dibuat saat tanggalnya tiba — bukan tambalan di
// sini.
const cicilanMaks = 60

// Mode pencatatan, dipilih dari menu di kanan atas form. Kosong adalah jalur
// utamanya: satu transaksi, dan tidak ada satu isian tambahan pun yang muncul.
var modeCicilan = []struct{ Value, Label, Hint string }{
	{"", "Sekali jalan", "Satu transaksi, seperti biasa"},
	{"cicil", "Cicilan", "Nominalnya dibagi rata ke tiap bulan"},
	{"ulang", "Berulang", "Nominal penuh diulang tiap bulan"},
}

func modeLabel(v string) string {
	for _, m := range modeCicilan {
		if m.Value == v {
			return m.Label
		}
	}
	return ""
}

// modeHint: kalimat pembuka keterangan di bawah kolom jumlah bulan.
func modeHint(v string) string {
	switch v {
	case "cicil":
		return "Nominal di atas dibagi rata ke sekian bulan."
	case "ulang":
		return "Nominal di atas ditagih penuh tiap bulan."
	}
	return ""
}

// totalSampai: jumlah angsuran yang jatuh pada atau sebelum today. Hanya itu
// yang menyentuh saldo hari ini — sisanya baru terhitung saat tanggalnya tiba,
// sama seperti transaksi bertanggal maju mana pun.
func totalSampai(txs []Tx, today time.Time) int64 {
	var total int64
	for _, t := range txs {
		if !t.Date.After(today) {
			total += t.AmountMinor
		}
	}
	return total
}

// jadwalCicilan memecah satu transaksi jadi n transaksi bulanan berturut-turut.
//
// Sisa pembagian ditaruh di angsuran pertama, bukan disebar atau dibuang:
// dijumlahkan kembali, seluruh angsuran harus pas dengan nominal yang diketik.
//
// Tanggalnya dijepit lewat hariDalamBulan, bukan AddDate: belanja 31 Januari
// yang dicicil akan melompati Februari dan jatuh di 3 Maret kalau memakai
// AddDate, sehingga satu bulan tidak punya angsuran sama sekali.
//
// Nomor urutnya ditaruh di kolom sendiri, bukan ditempel ke catatan: catatan
// milik orang yang menulisnya, dan mengubahnya lewat edit massal berarti harus
// merakit ulang teks "(cicilan 3/12)" yang ikut terhapus. SeriesID sendiri
// diisi belakangan oleh CreateTxs, yang tahu nomor rangkaiannya.
func jadwalCicilan(t Tx, n int, bagi bool) []Tx {
	if n < 2 {
		return []Tx{t}
	}
	kind, per := "ulang", t.AmountMinor
	if bagi {
		kind, per = "cicil", t.AmountMinor/int64(n)
	}

	loc := t.Date.Location()
	out := make([]Tx, n)
	for i := range out {
		c := t
		c.AmountMinor = per
		if bagi && i == 0 {
			c.AmountMinor += t.AmountMinor - per*int64(n)
		}
		c.Date = hariDalamBulan(t.Date.Year(), t.Date.Month()+time.Month(i), t.Date.Day(), loc)
		c.SeriesSeq, c.SeriesN, c.SeriesKind = i+1, n, kind
		out[i] = c
	}
	return out
}

// bacaMode menjepit mode dari URL atau form ke salah satu yang dikenal. Yang
// tidak dikenal jatuh ke sekali jalan, bukan ditolak: mode ngawur paling banter
// datang dari URL yang salah ketik, dan jalur utamanya adalah jawaban yang
// paling tidak mengejutkan.
func bacaMode(v string) string {
	if modeLabel(v) == "" {
		return ""
	}
	return v
}

// readCicilan membaca pilihan mode dan jumlah bulannya. n=1 berarti transaksi
// biasa, dan itu jawabannya selama modenya tidak dipilih.
func readCicilan(r *http.Request, t Tx) (n int, bagi bool, err error) {
	mode := bacaMode(r.FormValue("mode"))
	if mode == "" {
		return 1, false, nil
	}

	// Transfer punya dua sisi nominal, dan memecah sisi keluarnya saja akan
	// membuat kurs tiap angsuran ngawur. Menu modenya memang tidak ada di form
	// transfer, tapi yang menjaganya adalah baris ini, bukan itu.
	if t.IsTransfer() {
		return 1, false, errors.New("Transfer belum bisa dicicil atau diulang.")
	}

	n, convErr := strconv.Atoi(strings.TrimSpace(r.FormValue("cicilan")))
	if convErr != nil || n < 2 || n > cicilanMaks {
		return 1, false, errors.New("Jumlah bulan harus 2 sampai " +
			strconv.Itoa(cicilanMaks) + ".")
	}
	return n, mode == "cicil", nil
}
