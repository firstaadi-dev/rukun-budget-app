package main

import (
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	db *pgxpool.Pool
	// Zona waktu aplikasi. Saldo berhenti di hari ini menurut zona ini, bukan
	// menurut zona server database: di UTC hari berganti tujuh jam lebih lambat
	// daripada di Jakarta, dan belanja yang dicatat lewat tengah malam akan
	// hilang dari saldo sampai pagi.
	loc *time.Location
}

// today: awal hari ini di zona aplikasi, batas atas seluruh perhitungan saldo.
func (s *Store) today() time.Time {
	n := time.Now().In(s.loc)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, s.loc)
}

var ErrNotFound = errors.New("data tidak ditemukan")
var ErrAlreadyLinked = errors.New("akun sudah terhubung ke keluarga")
var ErrTradeDate = errors.New("tanggal transaksi investasi tidak boleh mendahului penjualan atau pembelian terakhir")
var ErrInsufficientQty = errors.New("kuantitas jual melebihi yang dimiliki")
var ErrTradeLocked = errors.New("hapus penjualan yang lebih baru lebih dulu agar harga pokok tetap benar")

// Aplikasi ini multi-tenant. Setiap metode Store yang menyentuh data
// keluarga mengambil familyID sebagai argumen pertama, dan tidak ada varian
// tanpa batas yang bisa dipanggil sembarangan — satu query yang lupa menyaring
// berarti keluarga lain melihat isi rekening orang. Sebagai lapisan kedua,
// migrasi 002 memasang foreign key gabungan sehingga database sendiri menolak
// transaksi yang menunjuk dompet milik keluarga lain.
