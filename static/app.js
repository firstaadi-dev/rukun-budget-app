// Rukun — skrip pelengkap form. Semua halaman tetap berfungsi tanpa file ini;
// yang hilang cuma isian otomatis, bukan kemampuan menyimpan data.

const EXP = { JPY: 0, KRW: 0, VND: 0, CLP: 0, ISK: 0, BHD: 3, KWD: 3, JOD: 3, OMR: 3, TND: 3 };
const exp = (cur) => (cur in EXP ? EXP[cur] : 2);

// parseNum mengikuti aturan ParseAmount di money.go: pemisah desimal adalah
// titik/koma terakhir yang diikuti 1-2 digit, sisanya pemisah ribuan.
function parseNum(s) {
  s = String(s || '').replace(/\s/g, '').replace(/^[^\d,.-]+/, '');
  if (!s) return null;
  const neg = s.startsWith('-');
  s = s.replace(/^[+-]/, '');
  let whole = s, frac = '';
  const i = Math.max(s.lastIndexOf('.'), s.lastIndexOf(','));
  if (i >= 0) {
    const d = s.length - i - 1;
    if (d >= 1 && d <= 2) { whole = s.slice(0, i); frac = s.slice(i + 1); }
  }
  whole = whole.replace(/[.,]/g, '');
  if (!/^\d*$/.test(whole) || !/^\d*$/.test(frac)) return null;
  if (whole === '' && frac === '') return null;
  const v = Number((whole || '0') + '.' + (frac || '0'));
  return Number.isFinite(v) ? (neg ? -v : v) : null;
}

// floorTo membulatkan ke bawah pada satuan terkecil mata uang. Nominal diterima
// tidak boleh dibulatkan ke atas: uang yang keluar tidak cukup membeli sen
// tambahan itu, dan server akan menolaknya sebagai "diterima melebihi keluar".
// Epsilon-nya menutup galat biner, bukan kelebihan yang sungguhan.
function floorTo(v, cur) {
  const p = Math.pow(10, exp(cur));
  return Math.floor(v * p + 1e-6) / p;
}

// fmtPlain mencerminkan FormatPlain di money.go.
function fmtPlain(v, cur) {
  if (v === null || !Number.isFinite(v)) return '';
  const e = exp(cur);
  const neg = v < 0;
  const fixed = Math.abs(v).toFixed(e);
  const [w, f] = fixed.split('.');
  let out = w.replace(/\B(?=(\d{3})+(?!\d))/g, '.');
  if (e > 0 && !(cur === 'IDR' && Number(f) === 0)) out += ',' + f;
  return (neg ? '-' : '') + out;
}

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
const curOf = (sel) => sel.selectedOptions[0]?.dataset.currency || 'IDR';
const symOf = (sel) => sel.selectedOptions[0]?.dataset.symbol || '';

// ---------- kolom nominal: hanya angka, dengan pemisah ribuan ----------

// inputmode="decimal" cuma memberi saran keyboard di ponsel; di desktop huruf
// tetap bisa diketik dan salahnya baru ketahuan setelah menekan Simpan.
// Atribut pattern menjaga saat skrip mati, penyaring ini menjaga saat hidup —
// sekaligus menyisipkan pemisah ribuan supaya nominal besar bisa dibaca sambil
// diketik, bukan cuma setelah disimpan.

function bersihkanAngka(s) {
  const negatif = s.startsWith('-'); // saldo awal kartu kredit boleh minus
  return (negatif ? '-' : '') + s.replace(/[^\d.,]/g, '');
}

// pisahRibuan memformat ulang isian mengikuti konvensi Indonesia: koma milik
// user sebagai pemisah desimal, titik disisipkan sendiri sebagai pemisah
// ribuan. Titik yang diketik user diserap, bukan diartikan desimal — kalau
// tidak, mengetik "1." pada "1.234" akan berubah jadi "1," di tengah jalan.
function pisahRibuan(s) {
  const negatif = s.startsWith('-');
  const isi = negatif ? s.slice(1) : s;

  let bulat = isi, desimal = null;
  const koma = isi.indexOf(',');
  if (koma >= 0) {
    bulat = isi.slice(0, koma);
    desimal = isi.slice(koma + 1).replace(/[.,]/g, '').slice(0, 2);
  }
  bulat = bulat.replace(/[.,]/g, '').replace(/\B(?=(\d{3})+(?!\d))/g, '.');

  return (negatif ? '-' : '') + bulat + (desimal === null ? '' : ',' + desimal);
}

// Kursor dijaga lewat jumlah karakter bermakna sebelumnya — digit, koma, dan
// tanda minus — bukan lewat indeks mentah. Pemisah ribuan muncul dan hilang
// sendiri saat angkanya tumbuh, jadi indeks mentah akan meleset.
const PENTING = /[\d,-]/;

function hitungPenting(s) {
  let n = 0;
  for (const ch of s) if (PENTING.test(ch)) n++;
  return n;
}

function posisiSetelah(s, jumlah) {
  if (jumlah <= 0) return 0;
  let n = 0;
  for (let i = 0; i < s.length; i++) {
    if (PENTING.test(s[i])) n++;
    if (n >= jumlah) return i + 1;
  }
  return s.length;
}

$$('input[inputmode="decimal"]').forEach((el) => {
  // Menghapus mundur tepat di belakang pemisah ribuan seharusnya menghapus
  // angkanya, bukan pemisahnya: pemisah itu kita yang menaruh, dan
  // menghilangkannya sendirian membuat tombol backspace terasa tidak berfungsi.
  el.addEventListener('beforeinput', (e) => {
    if (e.inputType !== 'deleteContentBackward') return;
    const p = el.selectionStart;
    if (p === null || p !== el.selectionEnd || p < 2 || el.value[p - 1] !== '.') return;
    e.preventDefault();
    el.value = el.value.slice(0, p - 2) + el.value.slice(p);
    el.setSelectionRange(p - 2, p - 2);
    el.dispatchEvent(new Event('input', { bubbles: true }));
  });

  el.addEventListener('input', () => {
    const asli = el.value;
    const hasil = pisahRibuan(bersihkanAngka(asli));
    if (hasil === asli) return;
    const pos = el.selectionStart ?? asli.length;
    const penting = hitungPenting(asli.slice(0, pos));
    el.value = hasil;
    el.setSelectionRange(posisiSetelah(hasil, penting), posisiSetelah(hasil, penting));
  });
});

// ---------- form dompet: daftar penyedia mengikuti jenis ----------

(function walletForm() {
  const form = $('#wallet-form');
  if (!form) return;

  const providers = JSON.parse($('#provider-data')?.dataset.providers || '{}');
  const list = $('#provider-list', form);
  const field = $('#provider-field', form);
  const currency = $('#mata_uang', form);

  function sync() {
    const jenis = $$('[data-jenis]', form).find((r) => r.checked)?.value || 'bank';
    const opts = providers[jenis] || [];
    field.hidden = opts.length === 0; // Tunai tidak punya penyedia
    list.innerHTML = opts.map((p) => `<option value="${p}">`).join('');
  }
  function syncSymbol() {
    $$('[data-currency-symbol]', form).forEach((el) => {
      el.textContent = currency.selectedOptions[0].value === 'IDR' ? 'Rp' : currency.value;
    });
  }

  function syncSiklus() {
    const jenis = $$('[data-jenis]', form).find((r) => r.checked)?.value;
    const box = $('#siklus-kartu', form);
    if (box) box.hidden = jenis !== 'credit';
  }

  $$('[data-jenis]', form).forEach((r) => {
    r.addEventListener('change', sync);
    r.addEventListener('change', syncSiklus);
  });
  currency.addEventListener('change', syncSymbol);
  sync();
  syncSiklus();
})();

// ---------- form pengeluaran / pemasukan: prefix ikut mata uang dompet ----------

(function amountForm() {
  const form = $('[data-amount-form]');
  if (!form) return;
  const wallet = $('[data-wallet-select]', form);

  function sync() {
    $$('[data-currency-symbol]', form).forEach((el) => (el.textContent = symOf(wallet)));
    $$('[data-currency-code]', form).forEach((el) => (el.textContent = curOf(wallet)));
  }
  wallet.addEventListener('change', sync);
  sync();
})();

// ---------- form hutang piutang ----------

// Mata uang hanya dipilih sendiri saat tidak ada dompet. Begitu dompet dipilih,
// mata uangnya mengikuti dompet itu — dua sumber kebenaran untuk hal yang sama
// adalah cara paling mudah membuat nominal tersimpan dengan satuan yang salah.
(function hutangForm() {
  const form = $('#hutang-form');
  if (!form) return;
  const wallet = $('[data-wallet-select]', form);
  const curField = $('[data-currency-field]', form);
  const curSelect = $('#mata_uang', form);

  function sync() {
    const pakaiDompet = wallet.value !== '';
    curField.hidden = pakaiDompet;
    const cur = pakaiDompet ? curOf(wallet) : curSelect.value;
    const sym = pakaiDompet ? symOf(wallet) : (cur === 'IDR' ? 'Rp' : cur);
    $$('[data-currency-symbol]', form).forEach((el) => (el.textContent = sym));
  }
  wallet.addEventListener('change', sync);
  curSelect.addEventListener('change', sync);
  sync();
})();

// ---------- form transfer ----------

(function transferForm() {
  const form = $('#transfer-form');
  if (!form) return;

  const raw = JSON.parse(form.dataset.rates || '{}');
  const base = form.dataset.base || 'IDR';
  const from = $('[data-from]', form);
  const to = $('[data-to]', form);
  const outEl = $('#nominal_keluar', form);
  const rateEl = $('#kurs', form);
  const inEl = $('#nominal_diterima', form);
  const feeEl = $('#biaya_admin', form);
  const rateField = $('[data-rate-field]', form);
  const crossEl = $('[data-cross]', form);
  const customTag = $('[data-rate-custom]', form);
  const hintEl = $('[data-rate-default]', form);

  // Kolom mana yang diturunkan dari kolom lain. Defaultnya nominal diterima:
  // user mengetik nominal keluar, kurs mengisi sisanya, biaya admin nol.
  // Begitu user mengetik sendiri di nominal diterima, dialah yang dipegang dan
  // biaya admin yang menyesuaikan — itu memang arti selisihnya.
  let derive = 'in';

  // "fromMinor:toMinor:powFrom:powTo:sumber"
  function known(a, b) {
    const s = raw[a + '>' + b];
    if (!s) return null;
    const [fm, tm, pf, pt, src] = s.split(':');
    if (!Number(fm) || !Number(tm)) return null;
    return { fromMajor: Number(fm) / Number(pf), toMajor: Number(tm) / Number(pt), src };
  }

  // Arah kutipan kurs harus sama persis dengan quoteDirection di handlers.go,
  // kalau tidak angka yang diketik user akan ditafsirkan terbalik oleh server.
  function quoteDir(a, b) {
    const r = known(a, b);
    if (r) return r.fromMajor >= r.toMajor ? [a, b] : [b, a];
    if (b === base) return [base, a];
    return [a, b];
  }

  function defaultFactor(a, b) {
    const r = known(a, b);
    return r ? r.toMajor / r.fromMajor : null;
  }

  let priceCur = '', perCur = '', defFactor = null;

  const outCur = () => curOf(from);
  const inCur = () => curOf(to);

  // Faktor konversi sumber -> tujuan. Sesama mata uang selalu 1, sehingga
  // seluruh perhitungan di bawah tidak perlu cabang khusus.
  function factor() {
    if (outCur() === inCur()) return 1;
    const v = parseNum(rateEl.value);
    if (v === null || v <= 0) return defFactor;
    if (perCur === outCur() && priceCur === inCur()) return v;
    if (perCur === inCur() && priceCur === outCur()) return 1 / v;
    return defFactor;
  }

  function setRateFromFactor(f) {
    if (!f) { rateEl.value = ''; return; }
    rateEl.value = fmtPlain(perCur === outCur() ? f : 1 / f, priceCur);
  }

  // Satu-satunya tempat yang menulis angka: arah penurunannya ditentukan
  // `derive`, jadi dua kolom tidak pernah saling menimpa.
  function recalc() {
    const out = parseNum(outEl.value);
    const f = factor();
    if (out === null || !f) return;

    if (derive === 'in') {
      const fee = parseNum(feeEl.value) || 0;
      inEl.value = fmtPlain(floorTo(Math.max(0, (out - fee) * f), inCur()), inCur());
      return;
    }
    const got = parseNum(inEl.value);
    if (got === null) return;
    feeEl.value = fmtPlain(out - got / f, outCur());
  }

  function syncPair() {
    const a = outCur(), b = inCur();
    const cross = a !== b;

    $$('[data-sym-from]', form).forEach((el) => (el.textContent = symOf(from)));
    $$('[data-sym-to]', form).forEach((el) => (el.textContent = symOf(to)));

    rateField.hidden = !cross;
    crossEl.hidden = !cross;
    if (!cross) {
      rateEl.value = '';
      defFactor = null;
      if (customTag) customTag.hidden = true;
      return;
    }

    crossEl.textContent = `Transfer lintas mata uang: ${a} \u2192 ${b}`;
    [priceCur, perCur] = quoteDir(a, b);
    defFactor = defaultFactor(a, b);

    $$('[data-sym-price]', form).forEach((el) => {
      el.textContent = priceCur === 'IDR' ? 'Rp' : priceCur;
    });
    $$('[data-rate-per]', form).forEach((el) => (el.textContent = `1 ${perCur} =`));

    if (defFactor) {
      const price = fmtPlain(perCur === a ? defFactor : 1 / defFactor, priceCur);
      const info = known(a, b);
      const asal = info && info.src === 'pasar'
        ? `kurs pasar${form.dataset.rateUpdated ? ' ' + form.dataset.rateUpdated : ''}`
        : 'kurs transfer terakhir';
      hintEl.textContent = `\u00b7 ${asal}: ${price}`;
      if (!rateEl.value) setRateFromFactor(defFactor);
    } else {
      hintEl.textContent = '\u00b7 belum ada kurs acuan, isi manual';
    }
    markCustom();
  }

  function markCustom() {
    if (!customTag) return;
    const f = factor();
    customTag.hidden = !defFactor || !f || Math.abs(f - defFactor) < defFactor * 1e-9;
  }

  // Ganti dompet berarti mata uangnya bisa berubah. Nominal diterima dan biaya
  // admin yang lama sudah tidak punya arti di mata uang baru, jadi dikosongkan
  // dan penurunan dikembalikan ke keadaan awal.
  function onPairChange() {
    derive = 'in';
    inEl.value = '';
    feeEl.value = '';
    syncPair();
    recalc();
  }

  from.addEventListener('change', onPairChange);
  to.addEventListener('change', onPairChange);
  outEl.addEventListener('input', recalc);
  rateEl.addEventListener('input', () => { markCustom(); recalc(); });
  inEl.addEventListener('input', () => { derive = 'fee'; recalc(); });
  feeEl.addEventListener('input', () => { derive = 'in'; recalc(); });

  // Saat mengubah transfer lama, nominal diterima sudah tersimpan apa adanya:
  // jangan dihitung ulang, biarkan biaya admin yang menyesuaikan.
  if (inEl.value) derive = 'fee';
  syncPair();
})();

// ---------- filter yang mengirim sendiri saat pilihannya berubah ----------

$$('[data-auto-submit] select').forEach((sel) => {
  sel.addEventListener('change', () => sel.form.submit());
});

// ---------- PWA ----------

if ('serviceWorker' in navigator) {
  window.addEventListener('load', () => navigator.serviceWorker.register('/sw.js'));
}
