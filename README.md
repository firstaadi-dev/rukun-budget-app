# Rukun — Keuangan Keluarga

Pencatatan keuangan keluarga yang bisa diakses beberapa anggota ke data yang sama.

Fase 1: Dashboard, Dompet, dan Transaksi (pengeluaran, pemasukan, transfer lintas mata uang).
Fase 2: kategori kustom, ringkasan per kategori, filter transaksi per kategori, dan
kurs otomatis dari API.
Fase 3: multi-tenant — satu deployment melayani banyak keluarga, dengan data yang
terpisah penuh.
Fase 4: siklus tagihan kartu kredit, dan pencatatan hutang piutang per pihak.
Fase 5: investasi — emas batangan, saham dan ETF Amerika, serta reksadana Indonesia,
dengan harga pasar yang diambil sendiri.

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
| `HARGA_URL` | tidak | Sumber harga saham, ETF, dan emas. Default endpoint chart Yahoo Finance; isi `off` untuk mematikan. |
| `HARGA_URL_CADANGAN` | tidak | Host kedua Yahoo, dipakai saat yang pertama gagal. Default query2; isi `off` untuk mematikan. |
| `HARGA_TWELVE_KEY` | tidak | Kunci [Twelve Data](https://twelvedata.com/pricing), sumber harga ketiga. Kosong berarti sumber itu mati. Lihat di bawah. |
| `NAB_URL` | tidak | Sumber NAB reksadana. Default Infovesta; isi `off` untuk mematikan. |

### Kalau harga pasar sering gagal

Yahoo membatasi permintaan per alamat IP, dan Render memakai alamat bersama —
jadi `429 Too Many Requests` bisa datang meski aplikasi ini cuma mengambil
belasan simbol sehari. Kedua host Yahoo biasanya menolak bersamaan, jadi
`HARGA_URL_CADANGAN` saja tidak selalu menolong.

Jalan keluarnya `HARGA_TWELVE_KEY`. Daftar gratis di
[twelvedata.com](https://twelvedata.com/pricing) — tier gratisnya tanpa kartu —
lalu isi kuncinya di environment. Batasnya dihitung per akun, bukan per alamat
IP, jadi tetangga di Render tidak ikut menghabiskannya. Selama kosong, aplikasi
tetap berjalan seperti biasa, hanya tanpa sumber ketiga.

Dua hal yang tetap bekerja walau seluruh sumber sedang menolak:

- Harga yang pernah berhasil diambil tersimpan di database, bukan cuma di
  memori — instance yang tidur lalu bangun tidak kehilangannya.
- Tiap posisi boleh menyimpan harga yang diisi sendiri, lewat tombol di halaman
  detail investasi. Itu tidak bergantung sumber mana pun.

Seluruh harga juga dijemput ulang sekali sehari pada pukul 00:00 waktu `APP_TZ`,
saat bursa sudah tutup dan tidak ada yang sedang membuka halaman.

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

## Akun dan anggota

Halaman **Akun** (`/pengaturan`) mengurus dua hal yang sebelumnya tidak punya jalan sama
sekali dari dalam aplikasi: mengganti kata sandi sendiri, dan mencabut akses anggota yang
sudah tidak lagi bagian dari keluarga. Sebelum ini sandi yang bocor hanya bisa diganti
lewat akses langsung ke database, dan memutar kode undangan cuma menutup pendaftaran baru
— sesi yang sudah berjalan tetap hidup tiga puluh hari penuh.

Halaman ini juga satu-satunya jalan menuju **Keluar** di ponsel. Sidebar yang memuat
tombol itu baru muncul mulai lebar 900px, jadi selama ini pengguna ponsel tidak punya
cara keluar dari aplikasinya sendiri.

**Ganti sandi tetap meminta sandi lama** meski yang meminta jelas-jelas sudah punya sesi.
Tanpa itu, satu perangkat yang tertinggal dalam keadaan login cukup untuk mengunci
pemiliknya keluar dari akunnya sendiri. Setelah sandinya diganti, **seluruh sesi anggota
itu dihapus** dan perangkat yang sedang dipakai langsung diberi sesi pengganti. Sandi
diganti biasanya justru karena yang lama diduga bocor; tanpa langkah itu, sandi barunya
tidak mengusir siapa pun.

**Anggota dinonaktifkan, tidak dihapus.** `transactions.created_by` menunjuk ke barisnya
untuk mengisi "Dicatat oleh", dan menghapus anggota berarti menghapus jejak siapa mencatat
apa — persis hal yang paling dibutuhkan saat ada yang perlu ditelusuri belakangan. Yang
dicabut adalah aksesnya: `disabled_at` terisi, seluruh sesinya dihapus, dan `SessionUser`
menyaringnya sehingga pencabutan berlaku di semua perangkatnya pada permintaan berikutnya.

Penyaring itu ada di satu tempat saja, dan hilangnya tidak menimbulkan error apa pun —
anggota yang sudah dicabut cuma diam-diam tetap bisa masuk. `anggota_test.go` menjaganya
secara mekanis, sama seperti `tenant_test.go` menjaga penyaring `family_id`.

**Yang boleh mencabut hanya kepala keluarga**, yaitu anggota pertama — yang dibuat
bersama keluarganya lewat API admin. Perannya diturunkan dari urutan pendaftaran, bukan
disimpan sebagai kolom sendiri, jadi tidak ada keluarga yang bisa kehilangan kepalanya
karena satu baris data salah ubah. Kepala keluarga sendiri tidak bisa dinonaktifkan oleh
siapa pun, dan itu dijaga di query `SetMemberActive`, bukan cuma di handler: keluarga yang
kepalanya ikut nonaktif tidak menyisakan siapa pun yang bisa memulihkan anggota lain.

## Kartu kredit dan PayLater

Keduanya jenis dompet yang berbeda hanya pada nama dan daftar penyedianya; di seluruh
aplikasi perilakunya sama — saldonya bergerak negatif saat dipakai dan dilunasi
belakangan. `Wallet.IsCredit()` yang menentukan, dan semua yang di bawah berlaku untuk
keduanya.

**Limit** menentukan berapa lagi yang boleh dipakai. Transaksi yang menembusnya ditolak
dengan menyebut sisa limitnya, dan bilah pemakaian berubah merah saat menyentuh 90% —
transaksi yang ditolak di kasir lebih merepotkan daripada peringatan yang muncul
kecepatan. Tanpa limit terisi, tidak ada yang bisa dijaga dan pemakaian dibiarkan;
menebak pagunya lebih berbahaya daripada diam.

**Tanggal cetak** dan **tanggal bayar** membedakan dua angka yang sering tertukar:

| | Artinya |
|---|---|
| **Tagihan** | Yang sudah tercetak di lembar tagihan terakhir dan harus dibayar sebelum jatuh tempo, dikurangi pembayaran yang masuk sesudahnya. |
| **Terpakai** | Seluruh yang terpakai sampai hari ini, termasuk belanja yang belum masuk tagihan mana pun. |

Tagihan adalah **bagian di dalam** nominal terpakai, bukan angka terpisah, jadi selisih
keduanya ikut ditulis sebagai "termasuk sekian belum ditagih". Ketiga angka di kartu bisa
dicek silang tanpa berhitung: tagihan ditambah yang belum ditagih sama dengan terpakai,
dan terpakai ditambah sisa limit sama dengan limitnya.

Nominal terpakai sengaja memakai total, bukan hanya yang belum ditagih: limit dimakan
oleh totalnya, dan bank menolak transaksi berdasarkan angka itu. Menampilkan yang belum
ditagih saja akan membuat kartunya bertentangan sendiri dengan sisa limit di bawahnya.

Membayar sebesar sisa pemakaian tidak salah, tapi membayar sebesar tagihan sudah cukup
untuk menghindari bunga — karena itu keduanya ditampilkan berdampingan.

Tanggal di atas jumlah hari suatu bulan dijepit ke hari terakhir bulan itu, jadi tanggal
cetak 31 tetap masuk akal di Februari. Akun tanpa siklus tetap ditampilkan beserta limit
dan nominal terpakainya — hanya bagian tagihannya yang disembunyikan, karena tanpa tanggal
cetak "tagihan" tidak punya arti.

**Pengingat jatuh tempo diangkat ke puncak dashboard.** Tanda "telat" dan "3 hari lagi"
sudah lama ada di dalam kartunya sendiri, tapi tanda di dalam kartu hanya terbaca oleh
orang yang sedang memandang kartunya — sementara yang telat bayar justru orang yang
sedang tidak memikirkan kartu itu sama sekali. Peringatannya karena itu muncul di bagian
layar yang dilihat lebih dulu, lengkap dengan nominal dan tombol bayarnya, dan **hanya**
saat ada yang perlu dikerjakan: peringatan yang selalu terpampang akan berhenti dibaca
tepat pada hari ia dibutuhkan.

Tiga hal yang memicunya: tagihan yang sudah lewat jatuh tempo, tagihan yang jatuh
temponya tinggal lima hari atau kurang, dan limit yang sudah terpakai 90%. Satu baris per
akun — kalau tagihannya telat sekaligus limitnya menipis, yang ditulis tagihannya, karena
hanya itu yang punya tenggat. Urutan kartu di dashboard mengikuti kegentingan yang sama:
deret kartu itu menggulung ke samping dan yang di ujung praktis tidak terbaca di layar
ponsel, jadi yang di sana harus yang paling tidak butuh tindakan.

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

## Investasi

Tiga jenis yang bisa dicatat: **emas batangan**, **saham dan ETF Amerika**, dan
**reksadana Indonesia**. Ketiganya berbentuk sama — sejumlah unit yang dimiliki, modal
yang sudah dikeluarkan, dan harga pasar yang bergerak sendiri — jadi ketiganya memakai
satu tabel `investments`, bukan tiga. Yang berbeda cuma satuannya (gram, lembar, unit),
cara harganya didapat, dan dari mana uangnya diambil.

**Pembelian menumpang `transactions`, bukan tabel sendiri.** Alasannya sama dengan hutang
piutang: saldo dompet diturunkan dari satu query di `walletSelect`, dan sumber kedua yang
juga menggerakkan saldo akan menyimpang tanpa ketahuan. Jenisnya `invest_buy`, dengan
kolom `investment_id` dan `qty_e8`. Di daftar transaksi ia bernada netral, bukan
"keluar" — uangnya memang meninggalkan dompet, tapi tidak habis; ia berubah bentuk jadi
sesuatu yang masih dimiliki, persis seperti transfer antar dompet.

**Satu posisi per identitas.** Untuk emas, identitasnya merek *dan* pecahan: keping 1
gram dan 10 gram jadi dua posisi terpisah karena preminya berbeda dan harga jualnya
kembali juga berbeda. Menggabungkan keduanya akan menyembunyikan selisih yang nyata.

### Saldo broker, dan mengapa ia dompet

Saham dan reksadana dibeli dari saldo yang sudah lebih dulu ditopup ke brokernya. Saldo
itu dompet sungguhan — uangnya sudah keluar dari bank, sudah kena kurs dan biaya
transfer, tapi belum jadi aset apa pun — jadi ia jenis dompet sendiri (`broker`). Tanpa
dompet itu, uang yang mengendap di broker akan hilang dari catatan sampai ia terpakai
membeli sesuatu.

Topupnya **tidak butuh jenis transaksi baru**: ia transfer antar dompet yang sudah ada,
lengkap dengan kurs dan biaya adminnya. Dana yang masuk ke dompet broker sudah final, dan
selisihnya tercatat sebagai biaya admin — persis alur yang diminta, dan persis yang sudah
dilakukan transfer lintas mata uang sejak fase 1.

Pembelian hanya menerima dompet yang mata uangnya sama dengan posisinya. Membeli posisi
dolar langsung dari dompet rupiah butuh kurs, dan kurs pada pembelian aset bukan hal yang
bisa ditebak belakangan dari dua angka yang tersimpan — itulah gunanya topup lebih dulu.

**Harga satuan mengisi biaya sendiri.** Form pembelian terisi lebih dulu dengan harga
yang berlaku sekarang — penutupan terakhir untuk saham dan ETF, NAB untuk reksadana,
harga emas dunia per gram untuk emas. Mengetik kuantitas mengisi totalnya; membetulkan
total jadi yang benar-benar keluar membuat selisihnya jatuh ke Biaya, dan itulah komisi
broker, spread, atau premi cetak. Biaya juga boleh diketik sendiri, dan harga satuannya
yang lalu menyesuaikan.

Ketiganya terikat satu persamaan — `total = kuantitas × harga satuan + biaya` — dengan
urutan menang yang sama persis dengan biaya admin di form transfer: isian orangnya
dipakai kalau ada, kalau kosong dihitung dari harga satuan. Karena aturan itu ada di
server, form ini tetap utuh tanpa JS; yang hilang cuma angkanya muncul sambil diketik.

**Boleh tanpa dompet.** Emas yang dibeli bertahun-tahun sebelum aplikasi ini dipakai tetap
perlu tercatat, dan memaksanya menunjuk sebuah dompet akan mengurangi saldo hari ini
karena uang yang keluar jauh di masa lalu. Pilihan "Tanpa dompet" mencatat kepemilikannya
tanpa menyentuh saldo mana pun, sama seperti hutang yang tidak pernah masuk rekening.

### Angka yang eksak

Kuantitas disimpan sebagai `int64` berskala 1e8, bukan pecahan biner: 0,1 lembar tidak
punya wakil eksak di `float64`, dan galatnya menumpuk di penjumlahan lot lalu tidak bisa
dilacak balik. Delapan desimal menampung unit reksadana yang lazim empat desimal dan saham
pecahan yang lazim enam.

Harga satuan disimpan empat desimal **lebih halus** dari satuan terkecil mata uangnya —
enam desimal untuk rupiah. NAB reksadana lazim ditulis empat desimal, dan membulatkannya
ke rupiah penuh menghapus persis perbedaan antar hari yang ingin dilihat. Di layar,
desimal tambahan itu hanya ditulis selama harganya masih di bawah 10.000 satuan mayor;
pada harga emas sejuta rupiah per gram ia cuma derau.

### Harga pasar

Sumbernya endpoint chart Yahoo Finance: tanpa API key, tanpa pendaftaran, dan menyebut
mata uang kuotasinya sendiri sehingga tidak perlu ditebak dari simbolnya. Yang dipakai
penutupan terakhir, bukan harga berjalan — bursa Amerika tutup saat sebagian besar
keluarga di sini melihat layarnya, dan angka yang bergerak tiap detik tidak mengubah satu
pun keputusan yang diambil dari halaman ini.

| Jenis | Sumber harga | Catatan |
|---|---|---|
| Saham & ETF | Yahoo Finance, simbol bursa (`VOO`, `QQQ`, `AAPL`) | langsung, mata uang kuotasi biasanya USD |
| Emas | Yahoo Finance, `GC=F` | per troy ounce → per gram, lalu dikonversi ke mata uang posisinya |
| Reksadana | Infovesta, dicocokkan dari nama | 1.293 reksadana, enam kategori, NAB harian |

Keduanya keluar sebagai satu peta yang dikunci simbol, jadi yang membacanya tidak perlu
tahu dari mana angkanya datang. Bentroknya tidak mungkin: kode bursa dan nama reksadana
tidak pernah sama.

Emas butuh dua konversi sekaligus: troy ounce jadi gram (31,1034768 gram, eksak menurut
definisi), lalu dolar jadi rupiah lewat kurs yang sudah ada. Tanpa keduanya tidak ada satu
pun posisi emas rupiah yang bisa mengikuti harga pasar. Kalau kursnya tidak diketahui,
harga pasarnya **dilewati**, bukan dipajang apa adanya: angka dolar yang tampil sebagai
rupiah lebih buruk daripada tidak ada angka sama sekali.

### NAB reksadana

Reksadana Indonesia tidak punya API resmi yang terbuka. Dua API komunitas yang beredar
(`bibit-reksadana`, `ojk-invest-api`) sudah mati saat fitur ini dibuat, dan OJK sendiri
hanya menerbitkan HTML bulanan. Yang dipakai sekarang **Infovesta**, satu-satunya yang
menerbitkan NAB harian seluruh reksadana Indonesia dalam bentuk yang bisa dibaca mesin.

Yang dibaca fragmen HTML di `/index/mutualfund/{kategori}/2` — tabel yang dipakai halaman
datanya sendiri, bukan API dengan kontrak yang dijanjikan siapa pun. Enam kategori (pasar
uang, pendapatan tetap, campuran, saham, ETF, indeks) berisi sekitar 1.293 reksadana.

Perlu diketahui sebelum mengubahnya:

- **Ini menempel pada bentuk tabel orang lain.** Nama di kolom ketiga, NAB/UP di keempat.
  Kalau tabelnya berubah, pengambilannya gagal dan yang tampil NAB isian sendiri.
- **Pengambilannya sengaja hemat**: enam permintaan per 12 jam untuk seluruh deployment,
  bukan satu per halaman yang dibuka. NAB memang cuma berubah sekali sehari sesudah bursa
  tutup. Ini satu-satunya sumber di aplikasi ini yang punya penyegar latar seperti kurs —
  daftarnya sama untuk semua keluarga, jadi tidak perlu menunggu ada yang membuka halaman.
- **Tanggal NAB tidak ikut diterbitkan** di tabelnya. Yang dicatat waktu pengambilan, dan
  labelnya di layar mengatakan "diambil", bukan "per".
- **Mata uangnya ditebak dari nama.** Yang berdenominasi dolar menyebut "USD" di
  belakang namanya, dan itu satu-satunya petunjuk yang ada. Salah tebak tertahan lapis
  berikutnya, yang menolak kuotasi bermata uang beda dari posisinya.
- **Posisi reksadana dicocokkan lewat nama persis**, dinormalkan huruf besar-kecil dan
  spasinya. Karena itu namanya dipilih dari daftar, bukan diketik — satu huruf berbeda
  berarti NAB-nya tidak akan pernah ketemu.

### Mencari simbol, dan nama yang tidak diketik

Posisi saham dan reksadana **tidak punya kolom nama**. Yang diisi cuma kodenya; namanya
diambil di server — dari kuotasi bursanya untuk saham dan ETF, dari daftar NAB untuk
reksadana. Emas tetap mengetik mereknya, karena ia memang tidak punya kode.

Sebelumnya nama adalah kolom sendiri yang diisi skrip di browser begitu hasil pencarian
sampai. Itu balapan yang sering kalah: memilih dari daftar lebih cepat daripada jaringan,
dan yang tersimpan posisi tanpa nama. Memindahkannya ke server menghapus balapannya, bukan
memperkecil peluangnya — di sana kodenya sudah ada di tangan, dan tidak ada yang perlu
ditunggu.

Nama saham datang dari endpoint kuotasi yang sama dengan harganya, bukan permintaan kedua:
`shortName` sudah ikut di balasan yang memang harus diambil. Tiga hasilnya dibedakan —
kode dikenal memberi nama resmi, kode salah ketik **ditolak sebelum tersimpan** (posisinya
tidak akan pernah dapat harga, dan mengetahuinya sekarang lebih baik), dan sumber yang
sedang mati membiarkan kodenya sendiri jadi nama sementara supaya pencatatan tidak
terhenti oleh kegagalan jaringan orang lain.

Mengetik di kolom simbol tetap memunculkan pilihan lengkap dengan namanya. Untuk saham dan
ETF pencariannya ke Yahoo — "voo" maupun "vanguard s&p" sama-sama menemukan VOO, karena
orang mengingat salah satu dari keduanya. Untuk reksadana pencariannya ke daftar NAB yang
sudah ada di memori, tanpa menyentuh jaringan sama sekali. Bedanya sekarang, daftar itu
alat bantu memilih kode — bukan sumber nama yang tersimpan.

`/investasi/cari` adalah satu-satunya endpoint JSON di aplikasi ini; sisanya HTML.
Pengecualiannya dibayar setimpal: tanpa itu, mencatat sebuah ETF berarti mengetik ulang
kode dan namanya dari ingatan, dan kode yang salah satu huruf menghasilkan posisi yang
harganya tidak pernah datang tanpa penjelasan apa pun. Kripto dan indeks disaring dari
hasilnya — fase ini hanya menjanjikan saham dan ETF, dan pilihan yang tidak bisa dipakai
lebih buruk daripada daftar yang lebih pendek.

Tanpa `app.js`, kolomnya tetap kolom teks biasa yang bisa diisi sendiri — dan karena
namanya tidak lagi bergantung pada skrip, posisinya tetap tersimpan lengkap.

### Yang perlu diketahui tentang ketiga sumber

1. **Emas dunia bukan harga gerai Antam.** Gerai menjual di atas harga ini dan membeli
   kembali di bawahnya, jadi angkanya perkiraan kasar. Yang ingin nilai jual sebenarnya
   mengisi harga buyback sendiri.
2. **Tidak satu pun dari ketiganya API resmi.** Yahoo dan Infovesta sama-sama bisa berubah
   sewaktu-waktu tanpa pemberitahuan. Karena itu setiap posisi tetap boleh menyimpan harga
   isian sendiri, dan kegagalan pengambilan tidak pernah mengosongkan layar: yang tampil
   harga terakhir yang diketahui, lengkap dengan kapan ia diambil. Ketiganya juga bisa
   dimatikan lewat `HARGA_URL=off` dan `NAB_URL=off`.
3. **Keduanya dipakai atas nama pemakaian pribadi keluarga**, dengan permintaan sesedikit
   mungkin dan User-Agent yang menyebut aplikasinya. Kalau Rukun dipakai lebih luas dari
   itu, sumbernya perlu ditinjau ulang — data pasar berlisensi, dan tidak ada satu pun dari
   keduanya yang memberi izin tertulis untuk dipakai ulang.

Harga bursa tidak punya penyegar latar, karena yang perlu diambil cuma simbol yang
benar-benar dimiliki seseorang — dan daftar itu hanya diketahui saat halamannya dibuka.
Simbol yang belum pernah diambil dijemput saat itu juga dalam anggaran waktu yang dijepit;
yang sudah punya nilai tapi mulai basi dikembalikan apa adanya dan disegarkan di latar.

## Daftar transaksi: periode dan pencarian

**Bawaannya bulan berjalan.** Sebelum ini daftarnya tak berperiode tapi dipotong di 200
baris terakhir — dan begitu sebuah keluarga melewati angka itu, transaksi lama menghilang
dari layar tanpa tanda apa pun dan tanpa cara apa pun untuk sampai ke sana. Bulan adalah
satuan yang dipakai keluarga saat mengingat pengeluaran, jadi itu yang jadi bawaannya;
pindah bulan dan melepas batasnya sama-sama cukup satu tautan.

Melangkah ke bulan depan sengaja tidak ditutup meski belum tiba. Transaksi boleh
bertanggal di depan, dan menutup jalan ke sana akan menyembunyikannya persis seperti
pemotongan 200 baris dulu.

**Batas halaman tetap ada, tapi sekarang dikatakan.** Satu bulan praktis tidak pernah
menembus 200 transaksi, tapi "Seluruh waktu" akan menembusnya cepat. Query karena itu
meminta satu baris lebih banyak dari batasnya: kelebihan satu baris itulah yang memberi
tahu bahwa daftarnya terpotong — tanpanya, 200 hasil pas tidak bisa dibedakan dari 200
hasil pertama dari seribu. Daftar yang berhenti tanpa keterangan terbaca sebagai "sudah
habis", dan itu persis cara transaksi lama dulu hilang tanpa ada yang menyadarinya.

**Pencarian mencakup empat kolom**: catatan, kategori, nama pihak, dan nama dompet —
keempat tempat sebuah transaksi bisa diingat kembali. Pencarian tetap tunduk pada periode
yang sedang dipilih, dan kalau tidak ada hasilnya, layar kosongnya menawarkan pencarian
yang sama di seluruh waktu. Menyetel pencarian supaya diam-diam melompati periode akan
lebih membingungkan: hasil yang muncul di luar bulan yang tertulis di layar.

**Rentang tanggal sendiri** untuk yang tidak jatuh rapi di batas bulan — sebuah
perjalanan, satu minggu belanja, periode tagihan kartu. Pintunya nama periodenya
sendiri: menekan "Agustus 2026" membuka panel tanggal tepat di bawahnya. Panelnya
`<details>` biasa, jadi buka-tutupnya jalan tanpa JS; skrip di `app.js` cuma menutupnya
saat ditekan di luar atau saat Escape, dan itu kenyamanan, bukan syarat. Panelnya
mengambang di atas daftar, tidak mendorongnya turun — yang sedang dilihat orangnya justru
daftar itu. Dipasang lewat `?dari=` dan
`?sampai=`, keduanya `YYYY-MM-DD` dan keduanya ikut terhitung; `sampai` yang inklusif di
layar digeser sehari jadi `To` yang eksklusif di query, satu kali di `bacaRentang`, bukan
di setiap pemanggil. Salah satunya boleh dikosongkan: yang terisi Dari saja berarti sejak
tanggal itu tanpa batas akhir, dan sebaliknya.

Dua hal sengaja tidak ditolak. Tanggal yang tertukar dibalik, bukan dibiarkan
menghasilkan daftar kosong — rentang yang dimaksud sudah jelas, dan daftar kosong terbaca
sebagai catatan hilang. Tanggal yang tidak terbaca sama sekali jatuh ke bulan berjalan,
sama seperti nilai `periode` yang salah ketik.

Rentang khusus tidak punya panah bulan tetangga. Panah pada rentang harus melangkah
sejauh rentangnya sendiri, dan "bulan berikutnya" dari 3–17 Juli tidak punya jawaban yang
bisa ditebak orang yang menekannya.

Kelima penyaring — jenis, kategori, periode, rentang, pencarian — saling menumpuk, dan
setiap tautan di halaman itu dirakit oleh `txURL` yang membawa serta penyaring lain yang
sedang aktif. Parameternya didaftar tertutup di `txParams`, jadi parameter asing tidak
ikut menempel dari satu tautan ke tautan berikutnya. Ketiga penyaring waktu — `periode`,
`dari`, `sampai` — dikecualikan dari aturan itu dan justru saling melepas: ketiganya
menjawab pertanyaan yang sama, dan rentang yang masih menempel akan mengalahkan bulan
yang baru dipilih, membuat tautannya seolah tidak melakukan apa-apa.

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
harga.go       harga saham, ETF, dan emas dunia; cache di memori, plus pencarian ticker
nab.go         NAB seluruh reksadana Indonesia; penyegar latar, cache dan pencarian di memori
investasi.go   model posisi dan lot, kuantitas eksak, untung-rugi, handler
view.go        view model dan pelabelan tanggal
auth.go        sesi cookie, bcrypt, login dan pendaftaran per keluarga
kartu.go       siklus tagihan kartu kredit: tanggal cetak, jatuh tempo, tagihan
hutang.go      handler hutang piutang dan pembayaran per pihak
anggota.go     halaman akun: ganti sandi, dan pencabutan akses anggota
templates/     layout + satu berkas per halaman
static/        CSS design system Classical, app.css, app.js, ikon, manifest
```

## Yang sengaja belum ada

Realtime sync, mode offline, laporan dan grafik lintas bulan, ekspor, anggaran per
kategori, cicilan berjadwal, dan bunga kartu kredit. Semuanya ditambahkan kalau memang
terasa kurang setelah dipakai, bukan sebelumnya.

Investasi fase 1 hanya mencatat pembelian. **Penjualan belum ada**, jadi posisi yang
sudah dilepas belum bisa ditutup dan keuntungan yang sudah direalisasi belum punya
tempat. Itu bukan kelalaian: penjualan membawa serta pemilihan lot mana yang dilepas
(FIFO, rata-rata, atau pilih sendiri), dan pilihan itu menentukan angka untung-rugi yang
dilaporkan — terlalu menentukan untuk diputuskan sambil lalu. Dividen dan pemecahan
saham juga belum ada, dengan alasan yang sama: keduanya mengubah kuantitas atau modal
lot lama, bukan menambah lot baru.

Peran per anggota juga masih sebatas satu pembedaan: kepala keluarga boleh mencabut akses,
selebihnya semua anggota sama persis. Belum ada anggota yang hanya bisa melihat, atau yang
dibatasi ke dompet tertentu. Satu pembedaan itu ada karena pencabutan akses memang tidak
boleh bisa dilakukan siapa saja — bukan karena sistem izin sudah mulai dibangun.

Pengingat jatuh tempo di atas juga sebatas yang terlihat saat aplikasi dibuka. Notifikasi
yang mendorong diri sendiri ke ponsel butuh push server dan izin per perangkat — belum
ada, dan pengingat yang muncul saat dilihat sudah menutup sebagian besar kasusnya.

Migrasi sekarang punya penerapnya sendiri di `migrate.go`: berkas `migrations/*.sql`
dijalankan berurutan, sekali saja, satu transaksi per berkas, dengan advisory lock
supaya dua instance tidak berebut saat Render menjalankan versi baru sebelum yang lama
berhenti. Yang belum ada di sana: migrasi turun, dan migrasi yang tidak boleh dibungkus
transaksi seperti `CREATE INDEX CONCURRENTLY`. Begitu salah satunya dibutuhkan, pindah
ke `golang-migrate` — tabel `schema_migrations` yang sekarang sudah berformat sama.

Berkas migrasi yang sudah pernah diterapkan tidak boleh diubah lagi. Perubahan
berikutnya ditulis sebagai berkas baru bernomor lebih besar.
