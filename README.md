# Rukun — Keuangan Keluarga

Pencatatan keuangan keluarga yang bisa diakses beberapa anggota ke data yang sama.

Fase 1: Dashboard, Dompet, dan Transaksi (pengeluaran, pemasukan, transfer lintas mata uang).
Fase 2: kategori kustom, ringkasan per kategori, filter transaksi per kategori, dan
kurs otomatis dari API.
Fase 3: multi-tenant — satu deployment melayani banyak keluarga, dengan data yang
terpisah penuh.
Fase 4: siklus tagihan kartu kredit, dan pencatatan hutang piutang per pihak.

Satu binary Go: server, template HTML, dan seluruh aset statis ikut ter-embed.
Tidak ada build frontend, tidak ada `node_modules`, tidak ada framework JS.

## Jalankan di lokal

Butuh Go 1.25+ dan sebuah Postgres.

```bash
docker compose up -d db
```

```bash
cp .env.example .env && set -a && . ./.env && set +a && go run .
```

Pakai `set -a` + `source`, bukan `export $(... | xargs)`: nilai yang mengandung spasi
akan terpotong oleh cara yang kedua.

Migrasi berjalan otomatis saat start. Database yang masih kosong akan berisi satu
keluarga bernama "Keluarga" dengan kode daftar acak; lihat kodenya lewat API admin di
bawah, lalu daftar di http://localhost:8080/daftar dengan kode itu.

Jalankan test (tidak butuh database):

```bash
go test ./...
```

## Deploy ke Render

Databasenya di **Neon**, bukan Postgres bawaan Render.

1. Push repo ini ke GitHub.
2. Di Neon, salin connection string proyek Anda. Ambil yang **tanpa `-pooler`** di nama
   host: aplikasi ini sudah punya pool sendiri lewat pgxpool, dan pooler Neon berjalan
   dalam mode transaksi yang bisa berbenturan dengan prepared statement pgx.
3. Render Dashboard → **New** → **Blueprint** → pilih repo ini. Render akan menanyakan
   `DATABASE_URL`; tempel connection string tadi. Nilainya disimpan di dashboard, tidak
   pernah masuk git.
4. Setelah deploy selesai, buka tab **Environment** dan salin `ADMIN_TOKEN` yang dibuat
   otomatis. Token itu dipakai untuk membuat keluarga lewat API admin — lihat
   "Membuat keluarga baru" di bawah.

Pilih region **Singapore** untuk web service-nya, sama dengan region proyek Neon
(`ap-southeast`). Query yang menyeberang region menambah puluhan milidetik pada setiap
permintaan halaman.

Migrasi dijalankan otomatis saat aplikasi start, berurutan dan sekali saja. Tidak ada
langkah manual, tapi juga bukan berarti tanpa migrasi — lihat bagian akhir README.

Soal paket gratis: web service Render tidur setelah 15 menit menganggur, dan compute
Neon juga menyusut ke nol saat tidak dipakai. Efeknya request pertama setelah lama
menganggur bisa memakan hampir satu menit. Untuk dipakai sungguhan, naikkan web
service ke `plan: starter` di `render.yaml`.

### Variabel lingkungan

| Nama | Wajib | Arti |
|---|---|---|
| `DATABASE_URL` | ya | Koneksi Postgres. Diisi otomatis oleh Render. |
| `ADMIN_TOKEN` | tidak | Token API admin. Kosong berarti API admin mati dan keluarga baru tidak bisa dibuat. |
| `BASE_CURRENCY` | tidak | Mata uang total di dashboard. Default `IDR`. |
| `APP_TZ` | tidak | Zona waktu untuk "Hari ini". Default `Asia/Jakarta`. |
| `PORT` | tidak | Default `8080`. Diisi otomatis oleh Render. |
| `RATES_URL` | tidak | Sumber kurs. Default open.er-api.com; isi `off` untuk mematikan. |

## PWA

Aplikasi bisa dipasang ke home screen (Android dan iOS) lewat menu **Bagikan → Tambah
ke Layar Utama**. Distribusinya cukup dengan membagikan satu URL — tanpa app store.

Service worker-nya sengaja tidak menyimpan apa pun. Data keuangan bisa diubah anggota
keluarga lain kapan saja, dan saldo basi lebih menyesatkan daripada layar kosong.
Aplikasi ini butuh koneksi; itu keputusan sadar, bukan kekurangan yang belum digarap.

## Multi-tenant: satu deployment, banyak keluarga

Setiap keluarga punya data sendiri yang tidak terlihat oleh keluarga lain. Pemisahannya
dijaga di dua lapis, bukan satu:

**Lapis aplikasi.** Setiap metode `Store` yang menyentuh data keluarga menerima
`familyID` sebagai argumen, dan tidak ada varian tanpa batas yang bisa dipanggil. Nilai
itu selalu berasal dari sesi login, tidak pernah dari parameter URL atau isian form.
Penyaring `family_id` juga sudah menempel di dalam potongan query `walletSelect` dan
`txSelect`, jadi query baru mewarisinya tanpa perlu diingat.

**Lapis database.** `transactions` punya kolom `family_id` sendiri — sengaja
didenormalisasi — sehingga foreign key gabungan `(family_id, wallet_id)` bisa merujuk
`wallets (family_id, id)`. Akibatnya tidak ada nilai `wallet_id` yang bisa disimpan
kalau dompetnya milik keluarga lain, berapa pun cerobohnya kode di atasnya.

`tenant_test.go` menjaga lapis pertama secara mekanis: ia membaca `store.go` dan gagal
kalau ada query yang menyentuh tabel data tanpa menyebut `family_id`, atau ada metode
`Store` baru yang tidak menerima `familyID`. Kesalahan semacam itu tidak menimbulkan
error dan tidak membuat test lain merah — jadi harus dicari dengan sengaja.

### Membuat keluarga baru

Tidak ada UI untuk ini. Keluarga baru berarti ruang data terpisah untuk orang lain, dan
itu dijalankan developer atas permintaan manual — bukan sesuatu yang bisa dipicu siapa
saja yang menemukan alamat aplikasinya.

Isi `ADMIN_TOKEN` di environment, lalu:

```bash
curl -X POST https://rukun.example.com/admin/keluarga \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"nama":"Keluarga Santoso","kepala":{"nama":"Ayah","sandi":"minimal-8-karakter"}}'
```

Balasannya memuat `kode_daftar`. Bagikan kode itu ke keluarga tersebut: kepala keluarga
memakainya untuk login, anggota lain memakainya untuk mendaftar di `/daftar`.

Melihat semua keluarga beserta kodenya:

```bash
curl -H "Authorization: Bearer $ADMIN_TOKEN" https://rukun.example.com/admin/keluarga
```

Mengganti nama atau memutar kode undangan:

```bash
curl -X PATCH https://rukun.example.com/admin/keluarga/2 \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"nama":"Keluarga Santoso","putar_kode":true}'
```

Tanpa `ADMIN_TOKEN`, seluruh endpoint di atas membalas 404 — bukan 401 — supaya
keberadaannya pun tidak ketahuan.

### Kode keluarga dipakai saat login

Nama anggota hanya unik di dalam keluarganya, karena hampir setiap keluarga punya
"Ayah". Karena itu form login meminta kode keluarga untuk menentukan yang mana. Kodenya
disimpan di cookie setahun, jadi cukup diketik sekali per perangkat.

Kode itu bukan kredensial: ia menentukan keluarga, bukan memberi akses. Yang menjaga
akses tetap kata sandi. Tapi kode itu juga yang dipakai mendaftar, jadi tetap
diperlakukan sebagai rahasia keluarga:

- **Bagikan lewat jalur pribadi**, jangan ditulis di grup.
- **Putar kodenya setelah semua anggota terdaftar**, lewat `putar_kode` di atas. Bagikan
  ulang kode barunya karena login juga memakainya.

Setiap pendaftaran baru dicatat di log (`anggota baru terdaftar: ... di keluarga ...`).
Kalau muncul nama yang tidak dikenal, kode keluarga itu sudah bocor.

## Kartu kredit

Dompet bertipe kartu kredit bisa diberi **tanggal cetak** dan **tanggal bayar**. Dari
keduanya aplikasi membedakan dua angka yang sering tertukar:

| | Artinya |
|---|---|
| **Tagihan** | Yang sudah tercetak di lembar tagihan terakhir dan harus dibayar sebelum jatuh tempo, dikurangi pembayaran yang masuk sesudahnya. |
| **Sisa pemakaian** | Seluruh yang terpakai sampai hari ini, termasuk belanja yang belum masuk tagihan mana pun. |

Membayar sebesar sisa pemakaian tidak salah, tapi membayar sebesar tagihan sudah cukup
untuk menghindari bunga — karena itu keduanya ditampilkan berdampingan.

Tanggal di atas jumlah hari suatu bulan dijepit ke hari terakhir bulan itu, jadi tanggal
cetak 31 tetap masuk akal di Februari. Kartu tanpa siklus tetap berfungsi sebagai dompet
biasa; tanpa tanggal cetak, "tagihan" tidak punya arti dan menebaknya lebih menyesatkan
daripada diam.

**Pembayaran kartu dicatat sebagai transfer, bukan pengeluaran.** Belanjanya sudah
tercatat sebagai pengeluaran waktu kartu dipakai; mencatat pembayarannya sebagai
pengeluaran lagi akan menghitungnya dua kali. Tombol "Bayar Tagihan" membuka form
transfer biasa dengan dompet tujuan dan nominal tagihan sudah terisi.

## Hutang dan piutang

Dicatat terhadap **pihak** — orang atau lembaga — dan dijumlahkan per pihak, bukan per
transaksi. Pembayaran juga dilakukan per pihak: yang ditagih adalah orangnya, bukan
selembar catatan.

Empat jenis transaksi: `debt_in` menerima pinjaman, `debt_pay` membayar hutang,
`loan_out` memberi pinjaman, `loan_in` menerima pelunasan.

**Dompetnya boleh kosong.** Meminjam uang yang langsung dipakai tanpa pernah masuk
rekening tetap menambah kewajiban, dan memaksa memilih dompet akan membuat saldo dompet
itu salah. Catatan tanpa dompet menyimpan mata uangnya sendiri di kolom `currency`;
yang punya dompet menurunkannya dari dompet itu, supaya tidak ada dua sumber kebenaran.

Pembayaran yang melebihi sisa saldo ditolak. Tanpa itu, salah ketik satu nol membuat
saldo hutang berbalik jadi piutang dan tidak ada yang menyadarinya.

## Keputusan yang penting dipahami sebelum mengubah kode

**Uang selalu `int64` dalam satuan terkecil.** `Rp1.500,50` disimpan sebagai `150050`.
Tidak pernah `float64`: error pembulatan biner terakumulasi di `SUM` saldo dan tidak
bisa dilacak balik. Jumlah desimal per mata uang mengikuti ISO 4217 (`money.go`).

**Kurs bukan angka tersimpan, melainkan rasio dua nominal.** Menyimpan kurs sebagai
angka ber-skala membuat arah IDR→USD hanya punya 3 digit signifikan, dan konversi
bolak-balik menggeser nominal. Yang disimpan adalah nominal keluar, nominal diterima,
dan biaya admin — ketiganya `int64` eksak. Kurs yang ditampilkan diturunkan dari
ketiganya (`EffectiveRate`).

**Kurs pasar diminta dengan base USD, tidak pernah base IDR.** Dengan base IDR, API
membulatkan ke enam desimal sehingga 1 IDR = 0,000056 USD — dua digit signifikan, dan
kursnya meleset belasan persen. Dari base USD (1 USD = 17.936,304774 IDR) semua
pasangan lain diturunkan lewat USD. Kalau API tidak bisa dihubungi, aplikasi tetap
jalan memakai kurs dari transfer yang sudah tercatat.

**Nominal diterima dibulatkan ke bawah, dan selisih sesen dimaafkan.** Rp15.500.000
dibagi kursnya jatuh di $864,1697, sementara yang bisa benar-benar diterima hanya
kelipatan sen. Membulatkan ke atas menghasilkan nominal yang nilainya melebihi uang
yang keluar, jadi skrip form selalu membulatkan ke bawah. Di sisi server, biaya admin
negatif yang besarnya di bawah nilai satu satuan terkecil mata uang tujuan dianggap
sisa pembulatan dan dinolkan; di atas itu tetap ditolak.

**Kategori disimpan sebagai teks di transaksi, bukan foreign key.** Tabel `categories`
hanya sumber daftar pilihan. Dengan begitu menghapus kategori tidak bisa membuat
transaksi lama menggantung, dan tidak perlu join tambahan di setiap query. Harganya:
mengganti nama kategori harus ikut memperbarui transaksinya — itu dilakukan dalam satu
transaksi database di `RenameCategory`, dan menghapus kategori yang masih dipakai
ditolak.

**Hutang piutang menumpang tabel `transactions`, bukan tabel sendiri.** Saldo dompet
dihitung dari satu query di `walletSelect`; kalau ada tabel kedua yang juga menggerakkan
saldo, query itu harus menggabungkan dua sumber dan keduanya bisa menyimpang tanpa
ketahuan. Konsekuensinya `wallet_id` jadi nullable, dan CHECK `transactions_bentuk`
yang menentukan kolom mana wajib untuk tiap jenis. Menambah jenis transaksi berarti
menyentuh CHECK itu **dan** daftar `kindMenambahSaldo` di `store.go` — jenis yang
terlewat di salah satunya membuat saldo bohong tanpa error apa pun.

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
main.go        wiring, rute, sidik jari aset
migrate.go     penerap migrasi berurutan dengan advisory lock
migrations/    skema, satu berkas per perubahan
admin.go       API admin tanpa UI untuk membuat dan mengubah keluarga
handlers.go    handler HTTP dan validasi form
store.go       akses database (pgx)
money.go       nominal int64, kurs sebagai rasio, format Indonesia
rates.go       kurs pasar dari API, cache di memori, gabung dengan kurs transfer
view.go        view model dan pelabelan tanggal
auth.go        sesi cookie, bcrypt, login dan pendaftaran per keluarga
kartu.go       siklus tagihan kartu kredit: tanggal cetak, jatuh tempo, tagihan
hutang.go      handler hutang piutang dan pembayaran per pihak
templates/     layout + satu berkas per halaman
static/        CSS design system Classical, app.css, app.js, ikon, manifest
```

## Yang sengaja belum ada

Realtime sync, mode offline, laporan dan grafik lintas bulan, ekspor, anggaran per
kategori, cicilan berjadwal, pengingat jatuh tempo, bunga kartu kredit, dan peran/izin
per anggota. Semuanya ditambahkan kalau memang terasa kurang setelah dipakai, bukan
sebelumnya.

Migrasi sekarang punya penerapnya sendiri di `migrate.go`: berkas `migrations/*.sql`
dijalankan berurutan, sekali saja, satu transaksi per berkas, dengan advisory lock
supaya dua instance tidak berebut saat Render menjalankan versi baru sebelum yang lama
berhenti. Yang belum ada di sana: migrasi turun, dan migrasi yang tidak boleh dibungkus
transaksi seperti `CREATE INDEX CONCURRENTLY`. Begitu salah satunya dibutuhkan, pindah
ke `golang-migrate` — tabel `schema_migrations` yang sekarang sudah berformat sama.

Berkas migrasi yang sudah pernah diterapkan tidak boleh diubah lagi. Perubahan
berikutnya ditulis sebagai berkas baru bernomor lebih besar.
