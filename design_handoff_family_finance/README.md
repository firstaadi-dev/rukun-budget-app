# Handoff: Aplikasi Kolaboratif Keuangan Keluarga (Fase 1)

## Overview
Mockup fase 1 untuk aplikasi kolaboratif pencatatan keuangan keluarga, mencakup 3 fitur: **Dashboard**, **Dompet**, dan **Transaksi**. Tujuannya menunjukkan alur, layout, dan gaya visual sebelum implementasi produksi.

## About the Design Files
File di bundle ini (`design-reference.dc.html`, `design-reference-standalone.html`) adalah **referensi desain berbasis HTML** — prototipe statis yang menunjukkan tampilan dan alur yang dituju, bukan kode produksi untuk disalin langsung. Tugasnya adalah **membangun ulang desain ini di stack aplikasi sungguhan** (React Native/Flutter untuk mobile, React/Vue untuk web, dsb. — pilih yang paling sesuai jika belum ada codebase) memakai pola dan komponen yang sudah ada di codebase tersebut.

`design-reference-standalone.html` adalah versi yang bisa dibuka langsung di browser apa pun (tanpa dependency project ini) — paling praktis untuk dilihat manusia atau di-screenshot. `design-reference.dc.html` adalah sumber aslinya.

## Fidelity
**High-fidelity secara visual** (warna, tipografi, spacing sudah final mengikuti design system "Classical"), tapi **logic-nya adalah mockup/dummy**: semua data adalah contoh statis, form tidak melakukan validasi atau perhitungan nyata, dan navigasi antar layar disederhanakan (tombol switcher di atas, bukan flow app sungguhan). Semua interaksi nyata (hitung kurs otomatis, validasi saldo, penyimpanan data) perlu diimplementasikan dari nol mengikuti aturan bisnis di bawah.

## Screens / Views

Ada 9 layar; 8 dalam frame mobile (iPhone), 1 varian desktop web untuk Dashboard.

### 1. Dashboard (Mobile)
- **Purpose**: ringkasan kondisi keuangan keluarga saat dibuka.
- **Layout**: single column, padding 20px, gap ~22px antar section.
- **Components**:
  - Greeting header: "Halo, {nama keluarga}" (heading font) + tanggal (muted, 13px)
  - Card besar "Total Dimiliki" (kicker uppercase kecil + accent color, angka besar 36px warna accent-700) = total semua dompet debet dikurangi total kartu kredit
  - 2 card kecil berdampingan: "Akun Debet" (jumlah semua dompet non-kartu-kredit) dan "Akun Kredit" (jumlah terpakai kartu kredit, warna merah/`--money-out`)
  - 3 tombol quick action (grid 3 kolom, icon+label): Pengeluaran, Pemasukan, Transfer — masing-masing navigasi ke form terkait
  - Section "Transaksi Terbaru": list 4 transaksi terakhir (icon bulat berwarna sesuai jenis, deskripsi, dompet+tanggal muted, nominal berwarna hijau/merah/netral) + link "Lihat semua" ke Daftar Transaksi
  - Section "Dompet Saya": strip horizontal scroll berisi mini-card tiap dompet (tipe, nama, saldo) + link "Lihat semua" ke Daftar Dompet

### 2. Dashboard (Web)
- Layout desktop: sidebar kiri 216px (brand + nav Dashboard/Dompet/Transaksi), konten kanan padding 32px.
- Header halaman + tombol "Tambah Transaksi"
- 3 card ringkasan sejajar (Total Dimiliki, Akun Debet, Akun Kredit) — sama seperti mobile
- Grid 2 kolom: tabel "Transaksi Terbaru" (kolom: Tanggal, Deskripsi, Dompet, Jumlah) di kiri; list card "Dompet Saya" di kanan

### 3. Daftar Dompet
- Tombol full-width "Tambah Dompet" di atas
- List card per dompet: icon bulat sesuai tipe (Tunai/Bank/Kartu Kredit/E-Wallet), nama, subjudul (penyedia + tipe), saldo (kanan, merah jika negatif), tag kecil kode mata uang

### 4. Tambah/Edit Dompet
- Field "Jenis Dompet": segmented control 4 opsi (Tunai/Bank/Kredit/E-Wallet)
- Field "Penyedia": dropdown, daftar berbeda tergantung jenis (Bank/Kartu Kredit → BCA, Mandiri, BNI, BRI, CIMB Niaga, Permata; E-Wallet → GoPay, OVO, DANA, ShopeePay, LinkAja; Tunai → tidak ada field ini)
- Field "Nama Dompet": text input custom (contoh: "Tabungan Pendidikan Anak")
- Field "Mata Uang": dropdown (IDR, USD, SGD, EUR, JPY, dst — harus mendukung multi-currency penuh)
- Field "Saldo Awal": input angka dengan prefix simbol mata uang
- Tombol primary full-width "Simpan Dompet"

### 5. Daftar Transaksi
- Filter segmented: Semua / Keluar / Masuk / Transfer
- List dikelompokkan per tanggal (header "Hari ini", "Kemarin", tanggal lengkap), tiap baris: icon bulat, deskripsi/kategori, dompet + catatan singkat (muted), nominal berwarna
- Baris transfer bisa ditekan → buka Detail Transaksi

### 6. Tambah Pengeluaran
- Field Dompet (dropdown, menampilkan saldo saat ini)
- Field Kategori: chip pilihan (Belanja, Tagihan, Transportasi, Makanan, Kesehatan, Lainnya)
- Field Nominal: prefix otomatis mengikuti **mata uang dompet yang dipilih** (tidak bisa diganti manual)
- Field Tanggal (date picker)
- Field Catatan (textarea, opsional tapi disarankan)
- Tombol primary "Simpan Pengeluaran"

### 7. Tambah Pemasukan
- Struktur identik dengan Pengeluaran; Kategori berbeda: Gaji, Bonus, Hadiah, Investasi, Lainnya
- Nominal juga mengikuti mata uang dompet

### 8. Tambah Transfer (lintas mata uang)
- Field "Dari Dompet" dan "Ke Dompet" (dropdown terpisah, bisa beda mata uang)
- Banner info muncul jika mata uang beda: "Transfer lintas mata uang: {A} → {B}"
- Field "Nominal Keluar" — nominal yang didebit dari dompet sumber (mata uang sumber)
- Field "Kurs" — default diambil dari kurs penyedia (ditampilkan sebagai helper text), user bisa isi kurs kustom (tag "Kustom" muncul jika diubah)
- Field "Nominal Diterima" — nominal yang masuk ke dompet tujuan (mata uang tujuan), bisa diisi manual
- Field "Biaya Admin" — **dihitung otomatis** dari selisih: `biaya admin = nominal keluar − (nominal diterima × kurs)`. Jika user mengisi nominal keluar DAN nominal diterima secara manual, selisihnya otomatis masuk ke sini (field tetap bisa diedit manual jika user mau override)
- Field Catatan (textarea)
- Tombol primary "Simpan Transfer"

### 9. Detail Transaksi
- Contoh menampilkan transaksi Transfer lintas mata uang
- Icon besar + judul jenis transaksi + timestamp
- Card breakdown: Dari (dompet+mata uang), Nominal keluar, Kurs digunakan (+ catatan jika kustom), Biaya admin, garis pemisah, Ke (dompet+mata uang), Nominal diterima, garis pemisah, Catatan, "Dicatat oleh" (nama anggota keluarga — jejak audit kolaboratif)
- Tombol Edit & Hapus

## Interactions & Behavior
- Navigasi utama: tap card/tombol → pindah layar (di app nyata: stack navigation per fitur, bukan switcher tab seperti di mockup ini)
- Baris transaksi bertipe Transfer dapat ditekan untuk membuka detail; Pengeluaran/Pemasukan idealnya juga bisa dibuka ke detail versi sederhana (belum ada di mockup — perlu ditambahkan)
- Kurs pada form transfer: toggle antara "kurs default penyedia" dan "kurs kustom" — saat kustom, tampilkan kurs default sebagai referensi kecil
- Perhitungan biaya admin transfer harus reaktif: berubah otomatis saat nominal keluar/diterima/kurs berubah, tapi tetap bisa di-override manual oleh user
- Belum ada di mockup, perlu ditentukan saat implementasi: validasi saldo cukup, konfirmasi hapus, state loading/error saat simpan, multi-user sync/realtime (aspek "kolaboratif")

## State Management
Kebutuhan data minimum (lihat juga bagian Data Model):
- Daftar dompet milik keluarga (CRUD)
- Daftar transaksi (create; jenis Pengeluaran/Pemasukan/Transfer)
- Saldo dompet harus ter-update setiap transaksi baru (derived atau disimpan & disinkronkan)
- Sesi user aktif per anggota keluarga (untuk field "Dicatat oleh")
- Kurs mata uang (sumber: penyedia/API kurs eksternal, dengan opsi override manual per transaksi)

## Data Model (tersirat dari desain)

**Wallet (Dompet)**
```
id, name (custom), type: 'Tunai' | 'Bank' | 'Kartu Kredit' | 'E-Wallet',
provider: string | null (mis. 'BCA', 'GoPay'; null untuk Tunai),
currency: string (kode ISO, mis. 'IDR', 'USD'),
initialBalance: number,
currentBalance: number (derived dari transaksi)
```

**Transaction (Transaksi)**
```
id, type: 'Pengeluaran' | 'Pemasukan' | 'Transfer',
date, note,
// Pengeluaran/Pemasukan:
walletId, category, amount (dalam mata uang walletId),
// Transfer:
fromWalletId, toWalletId,
amountOut (mata uang fromWallet),
amountIn (mata uang toWallet),
exchangeRate, isCustomRate: boolean,
adminFee (mata uang fromWallet, = amountOut - amountIn*exchangeRate secara default),
createdBy (anggota keluarga)
```

## Design Tokens
Mengikuti design system **Classical** (bundle terpisah, tidak disertakan di sini — minta akses ke `_ds/classical-*` di project sumber jika perlu file token/CSS aslinya):
- Warna: bg `#f3f2f2`, teks `#201f1d`, accent `#b68235` (ramp 100–900), neutral ramp 100–900
- Warna semantik tambahan (di luar token DS, ditambahkan khusus untuk finance): hijau `oklch(52% 0.09 149)` untuk pemasukan, merah `oklch(53% 0.11 27)` untuk pengeluaran/kredit
- Font: heading "Cormorant Garamond" (weight 600), body "Lora"
- Spacing scale: 4.6 / 9.2 / 13.8 / 18.4 / 27.6 / 36.8 px
- Radius: 2 / 4 / 7 px
- Shadow: sm/md/lg (lihat file CSS design system)
- Komponen: `.btn` (outline, bukan solid fill), `.card`, `.tag`, `.field`/`.input`/`.seg`, `.table`, `.nav` — detail lihat file `styles.css` design system di project sumber

## Assets
Tidak ada file gambar/icon eksternal — semua icon adalah inline SVG bergaya Lucide (garis/stroke, bukan fill), digambar langsung di markup. Frame perangkat (bezel iPhone, jendela browser) di prototipe ini hanya alat bantu visual desain, bukan bagian dari UI aplikasi sungguhan — abaikan saat implementasi.

## Files
- `design-reference.dc.html` — sumber desain asli (Design Component, mengandalkan runtime khusus)
- `design-reference-standalone.html` — versi yang sama, bisa dibuka langsung di browser mana pun untuk referensi visual
