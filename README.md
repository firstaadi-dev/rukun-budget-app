# Rukun — Keuangan Keluarga

Pencatatan keuangan keluarga yang bisa diakses beberapa anggota ke data yang sama.
Fase 1: Dashboard, Dompet, dan Transaksi (pengeluaran, pemasukan, transfer lintas mata uang).

Satu binary Go: server, template HTML, dan seluruh aset statis ikut ter-embed.
Tidak ada build frontend, tidak ada `node_modules`, tidak ada framework JS.

## Jalankan di lokal

Butuh Go 1.25+ dan sebuah Postgres.

```bash
docker compose up -d db
```

```bash
cp .env.example .env && export $(grep -v '^#' .env | xargs) && go run .
```

Buka http://localhost:8080, lalu daftar memakai `SIGNUP_CODE` dari `.env`.

Jalankan test (tidak butuh database):

```bash
go test ./...
```

## Deploy ke Render

1. Push repo ini ke GitHub.
2. Render Dashboard → **New** → **Blueprint** → pilih repo ini. `render.yaml` membuat
   web service dan Postgres sekaligus, keduanya di region Singapore.
3. Isi `APP_FAMILY` saat diminta, mis. `Keluarga Santoso`.
4. Setelah deploy selesai, buka tab **Environment** pada service `rukun`, salin nilai
   `SIGNUP_CODE` yang dibuat otomatis, lalu bagikan ke anggota keluarga untuk mendaftar.

Skema database dibuat otomatis saat aplikasi start; tidak ada langkah migrasi terpisah.

Dua hal tentang paket gratis Render yang perlu diketahui sejak awal:
Postgres gratis **dihapus setelah 30 hari**, dan web service gratis tidur setelah
15 menit menganggur sehingga request pertama butuh ~50 detik. Untuk dipakai
sungguhan, naikkan keduanya ke paket berbayar termurah di `render.yaml`
(`plan: basic-256mb` untuk database, `plan: starter` untuk web service).

### Variabel lingkungan

| Nama | Wajib | Arti |
|---|---|---|
| `DATABASE_URL` | ya | Koneksi Postgres. Diisi otomatis oleh Render. |
| `SIGNUP_CODE` | ya | Kode undangan untuk mendaftar. Tanpa ini aplikasi menolak start. |
| `APP_FAMILY` | tidak | Nama di sapaan dashboard. Default `Keluarga`. |
| `BASE_CURRENCY` | tidak | Mata uang total di dashboard. Default `IDR`. |
| `APP_TZ` | tidak | Zona waktu untuk "Hari ini". Default `Asia/Jakarta`. |
| `PORT` | tidak | Default `8080`. Diisi otomatis oleh Render. |

## PWA

Aplikasi bisa dipasang ke home screen (Android dan iOS) lewat menu **Bagikan → Tambah
ke Layar Utama**. Distribusinya cukup dengan membagikan satu URL — tanpa app store.

Service worker-nya sengaja tidak menyimpan apa pun. Data keuangan bisa diubah anggota
keluarga lain kapan saja, dan saldo basi lebih menyesatkan daripada layar kosong.
Aplikasi ini butuh koneksi; itu keputusan sadar, bukan kekurangan yang belum digarap.

## Keputusan yang penting dipahami sebelum mengubah kode

**Uang selalu `int64` dalam satuan terkecil.** `Rp1.500,50` disimpan sebagai `150050`.
Tidak pernah `float64`: error pembulatan biner terakumulasi di `SUM` saldo dan tidak
bisa dilacak balik. Jumlah desimal per mata uang mengikuti ISO 4217 (`money.go`).

**Kurs bukan angka tersimpan, melainkan rasio dua nominal.** Menyimpan kurs sebagai
angka ber-skala membuat arah IDR→USD hanya punya 3 digit signifikan, dan konversi
bolak-balik menggeser nominal. Yang disimpan adalah nominal keluar, nominal diterima,
dan biaya admin — ketiganya `int64` eksak. Kurs yang ditampilkan diturunkan dari
ketiganya (`EffectiveRate`), dan kurs default di form transfer diambil dari transfer
terakhir pasangan mata uang yang sama. Tidak ada API kurs eksternal.

**Saldo dompet tidak disimpan.** Selalu dihitung dari `initial_balance_minor` ditambah
transaksinya (`walletSelect` di `store.go`). Tidak ada kolom yang bisa melenceng dari
catatan. Kalau nanti transaksi sudah ratusan ribu baris, barulah pertimbangkan cache.

**Total dashboard jujur soal yang tidak diketahui.** Dompet bermata uang yang kursnya
belum pernah tercatat tidak ikut dijumlahkan, dan mata uangnya disebutkan di layar.
Lebih baik angka kurang lengkap daripada total yang diam-diam salah.

**Tanggal dibandingkan sebagai tanggal kalender, bukan selisih waktu.** Kolom `DATE`
Postgres kembali sebagai tengah malam UTC sedangkan "hari ini" mengikuti `APP_TZ`;
mengurangkannya sebagai durasi membuat transaksi kemarin tampil sebagai hari ini.

**URL aset membawa `?v=<sidik jari isi>`.** Setiap deploy yang mengubah CSS atau JS
otomatis memakai URL baru, jadi tidak ada pengguna yang tersangkut versi lama.

## Susunan berkas

```
main.go        wiring, rute, bootstrap skema, sidik jari aset
handlers.go    handler HTTP dan validasi form
store.go       akses database (pgx)
money.go       nominal int64, kurs sebagai rasio, format Indonesia
view.go        view model dan pelabelan tanggal
auth.go        sesi cookie, bcrypt, kode undangan
schema.sql     skema, dijalankan saat start
templates/     layout + satu berkas per halaman
static/        CSS design system Classical, app.css, app.js, ikon, manifest
```

## Yang sengaja belum ada

Realtime sync, mode offline, kategori custom, laporan dan grafik, ekspor,
API kurs otomatis, peran/izin per anggota, dan tool migrasi. Semuanya ditambahkan
kalau memang terasa kurang setelah dipakai, bukan sebelumnya.

Satu hal yang perlu diganti begitu skema berubah setelah rilis: `schema.sql`
sekarang hanya `CREATE ... IF NOT EXISTS`, jadi ia tidak bisa mengubah tabel yang
sudah berisi data. Saat itu tiba, pindah ke `golang-migrate` dan jadikan berkas ini
migrasi `001`.
