// Service worker paling minimal yang masih membuat aplikasi bisa dipasang ke
// home screen: browser hanya mensyaratkan adanya handler fetch, bukan cache.
//
// Sengaja tidak menyimpan apa pun. Data keuangan diubah anggota keluarga lain
// kapan saja, dan saldo basi lebih berbahaya daripada layar kosong. Tidak
// meng-cache aset juga berarti tidak ada versi cache yang harus dinaikkan
// setiap kali CSS berubah — satu sumber kesalahan yang hilang selamanya.
//
// ponytail: kalau nanti benar-benar butuh mode offline, tambahkan cache khusus
// untuk /static/ saja dan biarkan halaman tetap network-only.

self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()));
self.addEventListener('fetch', () => {});
